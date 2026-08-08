package localsvc

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// EnvFontPath and EnvFontBoldPath override font discovery. Homebox's own
// labelmaker.*_font_path settings are deliberately not consulted: which font
// renders the preview is this service's business, and the two would otherwise
// have to agree on a format.
const (
	EnvFontPath     = "HBOX_LOCAL_SVC_FONT"
	EnvFontBoldPath = "HBOX_LOCAL_SVC_FONT_BOLD"
)

// cjkFontCandidates are the usual install locations of a CJK-capable sans font.
// The Go fonts that ship with the binary cover Latin only, so without one of
// these a Chinese item name renders as a row of empty boxes. Globs are matched
// in order and the first hit wins, so more complete fonts come first.
var cjkFontCandidates = []struct {
	regular string
	bold    string
}{
	// Alpine: font-noto-cjk / font-wqy-zenhei. The Docker image installs the
	// latter, but which subdirectory it lands in has moved between releases, so
	// the plain paths are backed up by globs below.
	{regular: "/usr/share/fonts/noto/NotoSansCJK-Regular.ttc", bold: "/usr/share/fonts/noto/NotoSansCJK-Bold.ttc"},
	{regular: "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc", bold: "/usr/share/fonts/noto-cjk/NotoSansCJK-Bold.ttc"},
	{regular: "/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc"},
	{regular: "/usr/share/fonts/*/wqy*.tt[cf]"},
	{regular: "/usr/share/fonts/*/*zenhei*.tt[cf]"},
	// Debian/Ubuntu.
	{regular: "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", bold: "/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc"},
	{regular: "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc", bold: "/usr/share/fonts/truetype/noto/NotoSansCJK-Bold.ttc"},
	{regular: "/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc"},
	{regular: "/usr/share/fonts/truetype/arphic/uming.ttc"},
	// Anything else that looks like a CJK font. filepath.Glob has no "**", so the
	// nesting levels are spelled out.
	{regular: "/usr/share/fonts/*CJK*.tt[cf]"},
	{regular: "/usr/share/fonts/*/*CJK*.tt[cf]"},
	{regular: "/usr/share/fonts/*/*/*CJK*.tt[cf]"},
	// macOS, for development.
	{regular: "/System/Library/Fonts/PingFang.ttc"},
	{regular: "/System/Library/Fonts/Hiragino Sans GB.ttc"},
}

type fontSet struct {
	regular *sfnt.Font
	bold    *sfnt.Font

	// syntheticBold records that bold fell back to the regular face, so the
	// renderer knows it has to fake the weight by over-drawing.
	syntheticBold bool
}

var (
	fontsOnce sync.Once
	fonts     fontSet
)

func loadedFonts() fontSet {
	fontsOnce.Do(func() {
		fonts = discoverFonts()
	})

	return fonts
}

func discoverFonts() fontSet {
	set := fontSet{}

	if path := envString(EnvFontPath); path != "" {
		set.regular = parseFontFile(path)
	}
	if path := envString(EnvFontBoldPath); path != "" {
		set.bold = parseFontFile(path)
	}

	if set.regular == nil {
		set.regular, set.bold = discoverCJKFont()
	}

	if set.regular == nil {
		// Latin-only fallback. Better than failing the request outright: asset
		// IDs, URLs and English names still come out right.
		log.Warn().Msg("No CJK font found for label previews; non-Latin text will render as boxes. " +
			"Install font-wqy-zenhei or font-noto-cjk, or point " + EnvFontPath + " at a font file. " +
			"Bluetooth printing is unaffected: the printer draws that text with its own font")
		set.regular = mustParseFont(goregular.TTF)
		set.bold = mustParseFont(gobold.TTF)
	}

	if set.bold == nil {
		set.bold = set.regular
		set.syntheticBold = true
	}

	return set
}

func discoverCJKFont() (regular *sfnt.Font, bold *sfnt.Font) {
	for _, candidate := range cjkFontCandidates {
		path := firstExistingPath(candidate.regular)
		if path == "" {
			continue
		}

		parsed := parseFontFile(path)
		if parsed == nil {
			continue
		}

		log.Debug().Str("font", path).Msg("Using font for label previews")

		return parsed, parseFontFile(firstExistingPath(candidate.bold))
	}

	return nil, nil
}

// firstExistingPath resolves a candidate that may be a plain path or a glob.
func firstExistingPath(pattern string) string {
	if pattern == "" {
		return ""
	}

	if !hasGlobMeta(pattern) {
		if info, err := os.Stat(pattern); err == nil && !info.IsDir() {
			return pattern
		}
		return ""
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return ""
	}

	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && !info.IsDir() {
			return match
		}
	}

	return ""
}

func hasGlobMeta(pattern string) bool {
	for _, r := range pattern {
		switch r {
		case '*', '?', '[':
			return true
		}
	}

	return false
}

// parseFontFile reads a .ttf/.otf/.ttc. Collections hold several faces; the
// first one is the upright regular in every CJK collection worth using.
func parseFontFile(path string) *sfnt.Font {
	if path == "" {
		return nil
	}

	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied or well-known font path
	if err != nil {
		log.Warn().Err(err).Str("font", path).Msg("Can not read font")
		return nil
	}

	collection, err := sfnt.ParseCollection(raw)
	if err != nil {
		log.Warn().Err(err).Str("font", path).Msg("Can not parse font")
		return nil
	}

	parsed, err := collection.Font(0)
	if err != nil {
		log.Warn().Err(err).Str("font", path).Msg("Can not read first face of font")
		return nil
	}

	return parsed
}

func mustParseFont(raw []byte) *sfnt.Font {
	parsed, err := sfnt.Parse(raw)
	if err != nil {
		// These are compiled into the binary, so a failure here is a programming
		// error rather than a runtime condition.
		panic(err)
	}

	return parsed
}

// newFace builds a face whose em size is sizePx pixels. DPI is fixed at 72 so
// that one point equals one pixel and callers can think purely in pixels.
func newFace(parsed *sfnt.Font, sizePx float64) (font.Face, error) {
	return opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}
