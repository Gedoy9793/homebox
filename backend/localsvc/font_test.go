package localsvc

import (
	"os"
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestSimplifiedChineseFace(t *testing.T) {
	preferred := []string{"Noto Sans CJK SC", "Source Han Sans SC"}
	rejected := []string{
		"Noto Sans CJK JP",
		"Noto Sans CJK TC",
		// A monospace cut is the same region but the wrong shape for a label.
		"Noto Sans Mono CJK SC",
		// "SC" has to be the region suffix, not any old letters.
		"Scheherazade",
		"",
	}

	for _, family := range preferred {
		if !simplifiedChineseFace(family) {
			t.Errorf("expected %q to be preferred", family)
		}
	}
	for _, family := range rejected {
		if simplifiedChineseFace(family) {
			t.Errorf("expected %q not to be preferred", family)
		}
	}
}

func TestLooksLikeCJK(t *testing.T) {
	hinted := []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/x/SourceHanSans.otf",
	}
	plain := []string{"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/x/Arial.ttf"}

	for _, path := range hinted {
		if !looksLikeCJK(path) {
			t.Errorf("expected %q to be tried first", path)
		}
	}
	for _, path := range plain {
		if looksLikeCJK(path) {
			t.Errorf("expected %q not to be hinted", path)
		}
	}
}

// The Go fonts compiled into the binary are Latin-only, which is the whole reason
// discovery exists.
func TestBundledFontsCannotDrawChinese(t *testing.T) {
	set := discoverFonts()
	if set.regular == nil {
		t.Fatal("expected a font either way")
	}

	// Whatever was found has to be able to draw a label: digits at least, and
	// Chinese too when the host has a font for it.
	var buffer sfnt.Buffer
	if index, err := set.regular.GlyphIndex(&buffer, '0'); err != nil || index == 0 {
		t.Fatalf("expected the chosen font to have digits (index=%d, err=%v)", index, err)
	}

	if !drawsCJK(set.regular) {
		t.Skip("no CJK font installed here; nothing more to check")
	}

	// A CJK font that cannot draw Latin would leave asset IDs as empty boxes,
	// which is what a fallback-only font like Droid Sans Fallback does.
	for _, r := range []rune{'A', '/', '-'} {
		if index, err := set.regular.GlyphIndex(&buffer, r); err != nil || index == 0 {
			t.Errorf("expected the chosen font to draw %q as well as Chinese", r)
		}
	}
}

// A diagnostic for when previews come out as boxes on some host: point it at a
// font file and it reports whether this renderer can use it, and which faces it
// holds.
//
//	PROBE_FONT=/usr/share/fonts/... go test ./localsvc/ -run TestProbeFontFile -v
func TestProbeFontFile(t *testing.T) {
	path := os.Getenv("PROBE_FONT")
	if path == "" {
		t.Skip("set PROBE_FONT to inspect a font file")
	}

	raw, err := os.ReadFile(path) //nolint:gosec // a path the operator typed in
	if err != nil {
		t.Fatal(err)
	}

	collection, err := sfnt.ParseCollection(raw)
	if err != nil {
		t.Fatalf("this renderer cannot parse %s: %v", path, err)
	}

	t.Logf("%s: %d faces", path, collection.NumFonts())

	for i := range collection.NumFonts() {
		parsed, err := collection.Font(i)
		if err != nil {
			t.Logf("  face %d: unreadable: %v", i, err)
			continue
		}

		family, _ := parsed.Name(nil, sfnt.NameIDFamily)
		t.Logf("  face %d: %q chinese=%v preferred=%v",
			i, family, drawsCJK(parsed), simplifiedChineseFace(family))
	}
}
