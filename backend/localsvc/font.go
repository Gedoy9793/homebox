package localsvc

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// fontDirs are searched, recursively, for a font that can draw Chinese. The Go
// fonts compiled into the binary cover Latin only, so without one of these a
// Chinese item name comes out as a row of empty boxes.
//
// Which package puts which font where has moved between distro releases, so
// nothing here assumes a file name: every candidate is opened and asked whether
// it actually has the glyphs. That is the only check that cannot go stale.
var fontDirs = []string{
	"/usr/share/fonts",
	"/usr/local/share/fonts",
	"/usr/share/fonts/truetype",
	"/opt/fonts",
	// macOS, for development.
	"/System/Library/Fonts",
	"/Library/Fonts",
}

// fontExtensions are the formats x/image/font/sfnt can parse.
var fontExtensions = map[string]bool{".ttf": true, ".ttc": true, ".otf": true, ".otc": true}

// cjkFontHints order the search. A directory can hold hundreds of fonts and each
// one costs a parse, so files whose names suggest CJK coverage are tried first.
// This only affects the order — a font is still chosen on its glyphs.
var cjkFontHints = []string{"cjk", "noto", "wqy", "zenhei", "microhei", "hei", "song", "ming", "kai", "pingfang", "han"}

// cjkProbe is the character a font has to be able to draw to be considered. It is
// a common Han character, so any font with real CJK coverage has it.
const cjkProbe = '中'

// maxFontFiles bounds the scan, so a machine with a huge font collection cannot
// turn the first label render into a long wait.
const maxFontFiles = 400

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

// discoverCJKFont finds an installed font that can draw Chinese, along with its
// bold companion if the same directory has one.
func discoverCJKFont() (regular *sfnt.Font, bold *sfnt.Font) {
	files := collectFontFiles()

	for _, path := range files {
		parsed := parseFontFile(path)
		if parsed == nil || !drawsCJK(parsed) {
			continue
		}

		log.Info().Str("font", path).Int("scanned", len(files)).
			Msg("Using font for label previews")

		return parsed, boldCompanion(path)
	}

	log.Debug().Int("scanned", len(files)).Strs("directories", fontDirs).
		Msg("No installed font draws Chinese")

	return nil, nil
}

// collectFontFiles lists installed font files, most likely to cover CJK first.
func collectFontFiles() []string {
	var hinted, others []string

	for _, dir := range fontDirs {
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				// Unreadable or absent directory: nothing to do about it here.
				return nil //nolint:nilerr // skip, do not abort the whole walk
			}
			if entry.IsDir() || !fontExtensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}

			if len(hinted)+len(others) >= maxFontFiles {
				return fs.SkipAll
			}

			if looksLikeCJK(path) {
				hinted = append(hinted, path)
			} else {
				others = append(others, path)
			}

			return nil
		})
	}

	return append(hinted, others...)
}

func looksLikeCJK(path string) bool {
	name := strings.ToLower(filepath.Base(path))

	for _, hint := range cjkFontHints {
		if strings.Contains(name, hint) {
			return true
		}
	}

	return false
}

// drawsCJK reports whether a font has a glyph for the probe character. Asking the
// font beats guessing from its file name, which is what kept going wrong.
func drawsCJK(parsed *sfnt.Font) bool {
	var buffer sfnt.Buffer

	index, err := parsed.GlyphIndex(&buffer, cjkProbe)

	return err == nil && index != 0
}

// boldCompanion looks for a bold cut of the font next to it, e.g. the
// NotoSansCJK-Bold.ttc beside NotoSansCJK-Regular.ttc. Returns nil when there is
// none, in which case the renderer fakes the weight.
func boldCompanion(regularPath string) *sfnt.Font {
	name := filepath.Base(regularPath)

	for _, replacement := range [][2]string{
		{"-Regular", "-Bold"},
		{"regular", "bold"},
		{"Regular", "Bold"},
	} {
		if !strings.Contains(name, replacement[0]) {
			continue
		}

		candidate := filepath.Join(filepath.Dir(regularPath), strings.Replace(name, replacement[0], replacement[1], 1))
		if info, err := os.Stat(candidate); err != nil || info.IsDir() {
			continue
		}

		if parsed := parseFontFile(candidate); parsed != nil && drawsCJK(parsed) {
			return parsed
		}
	}

	return nil
}

// parseFontFile reads a .ttf/.otf/.ttc, returning nil for anything this renderer
// cannot use. Some installed fonts are not parseable by x/image/font/sfnt at all
// — wqy-zenhei.ttc is one — hence the caller trying the next candidate.
func parseFontFile(path string) *sfnt.Font {
	if path == "" {
		return nil
	}

	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied or discovered font path
	if err != nil {
		log.Debug().Err(err).Str("font", path).Msg("Can not read font")
		return nil
	}

	collection, err := sfnt.ParseCollection(raw)
	if err != nil {
		log.Debug().Err(err).Str("font", path).Msg("Can not parse font")
		return nil
	}

	return pickFace(collection, path)
}

// pickFace chooses a face out of a collection. Noto Sans CJK ships JP, KR, SC, TC
// and HK cuts plus a monospace version of each; the same code point is drawn
// differently per region, so the simplified Chinese proportional face is taken
// where there is one.
func pickFace(collection *sfnt.Collection, path string) *sfnt.Font {
	var fallback *sfnt.Font

	for i := range collection.NumFonts() {
		parsed, err := collection.Font(i)
		if err != nil {
			log.Debug().Err(err).Str("font", path).Int("face", i).Msg("Skipping unreadable face")
			continue
		}

		if family, err := parsed.Name(nil, sfnt.NameIDFamily); err == nil && simplifiedChineseFace(family) {
			return parsed
		}

		if fallback == nil {
			fallback = parsed
		}
	}

	return fallback
}

func simplifiedChineseFace(family string) bool {
	return strings.HasSuffix(family, " SC") && !strings.Contains(strings.ToLower(family), "mono")
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
