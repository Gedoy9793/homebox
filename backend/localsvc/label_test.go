package localsvc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Stand-ins for what Homebox puts in the label title.
const (
	testAssetID = "000-042"
	testCableID = "SW1-P24"
)

// embeddedSpec pulls the layout back out of a rendered label, the same way the
// browser does. Decoding the PNG first also proves the inserted chunk is valid:
// image/png verifies every chunk's CRC.
func embeddedSpec(t *testing.T, raw []byte) labelSpec {
	t.Helper()

	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("rendered label is not a valid PNG: %v", err)
	}

	text, ok := findTextChunk(raw, specKeyword)
	if !ok {
		t.Fatalf("no %q text chunk in the rendered label", specKeyword)
	}

	var spec labelSpec
	if err := json.Unmarshal([]byte(text), &spec); err != nil {
		t.Fatalf("embedded layout is not valid JSON: %v", err)
	}

	return spec
}

func findTextChunk(raw []byte, keyword string) (string, bool) {
	offset := len(pngSignature)

	for offset+8 <= len(raw) {
		length := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
		chunkType := string(raw[offset+4 : offset+8])
		data := raw[offset+8 : offset+8+length]

		if chunkType == "iTXt" {
			// keyword \0 compressionFlag compressionMethod language \0 translated \0 text
			name, rest, _ := bytes.Cut(data, []byte{0})
			if string(name) == keyword && len(rest) >= 2 {
				rest = rest[2:]
				_, rest, _ = bytes.Cut(rest, []byte{0})
				_, text, _ := bytes.Cut(rest, []byte{0})

				return string(text), true
			}
		}

		offset += 8 + length + 4
	}

	return "", false
}

func itemsOfType(spec labelSpec, kind string) []labelItem {
	var found []labelItem

	for _, item := range spec.Items {
		if item.Type == kind {
			found = append(found, item)
		}
	}

	return found
}

func TestRenderLabelEmbedsLayout(t *testing.T) {
	request := labelRequest{
		title:       testAssetID,
		description: "Network switch\nLocation: Rack",
		url:         "https://homebox.example.com/item/abc",
	}

	raw, err := renderLabel(request, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec := embeddedSpec(t, raw)

	standard := profiles[profileStandard]
	if spec.Width != standard.widthMM || spec.Height != standard.heightMM {
		t.Fatalf("expected a %gx%gmm label, got %gx%g", standard.widthMM, standard.heightMM, spec.Width, spec.Height)
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected exactly 1 QR code, got %d", len(codes))
	}
	if codes[0].Text != request.url {
		t.Fatalf("QR code holds %q", codes[0].Text)
	}

	// Every item has to stay inside the label, or the printer clips it.
	for _, item := range spec.Items {
		if item.X < 0 || item.Y < 0 ||
			item.X+item.Width > spec.Width+0.01 || item.Y+item.Height > spec.Height+0.01 {
			t.Fatalf("item outside the label: %+v", item)
		}
	}

	texts := itemsOfType(spec, itemText)
	if len(texts) == 0 {
		t.Fatal("expected text items")
	}
	if texts[0].Text != request.title || !texts[0].Bold {
		t.Fatalf("expected the title first and in bold, got %+v", texts[0])
	}

	// One item per line, so the printer does not re-wrap and drift away from the
	// preview.
	for _, item := range texts {
		if item.Wrap != "none" {
			t.Fatalf("expected wrapping to be resolved here, got %q", item.Wrap)
		}
	}
}

func TestRenderLabelPreviewMatchesLabelSize(t *testing.T) {
	t.Setenv(EnvDPI, "203")

	raw, err := renderLabel(labelRequest{title: testAssetID, url: "https://example.com/a/1"}, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("could not decode the preview: %v", err)
	}

	standard := profiles[profileStandard]
	scale := 203.0 / mmPerInch
	wantWidth := int(math.Round(standard.widthMM * scale))
	wantHeight := int(math.Round(standard.heightMM * scale))

	if got := decoded.Bounds().Size(); got.X != wantWidth || got.Y != wantHeight {
		t.Fatalf("expected a %dx%d preview, got %v", wantWidth, wantHeight, got)
	}

	if _, ok := decoded.(*image.Gray); !ok {
		t.Fatalf("expected a greyscale preview, got %T", decoded)
	}
}

func TestLayoutOmitsQRCodeWithoutURL(t *testing.T) {
	spec, err := buildSpec(labelRequest{title: "Shelf A"}, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if codes := itemsOfType(spec, itemQRCode); len(codes) != 0 {
		t.Fatalf("expected no QR code without a URL, got %+v", codes)
	}

	texts := itemsOfType(spec, itemText)
	if len(texts) == 0 {
		t.Fatal("expected the title to be kept")
	}
	// With no QR code the text starts at the padding rather than beside it.
	if texts[0].X != profiles[profileStandard].paddingMM {
		t.Fatalf("expected the text to use the full width, got x=%g", texts[0].X)
	}
}

// A cable flag is folded in half: the first face identifies the thing, the second
// carries the description across the full width. It also prints rotated, so the
// layout is built on a canvas with the label's sides swapped.
func TestCableProfileGivesEachFaceItsOwnContent(t *testing.T) {
	cable := profiles[profileCable]

	spec, err := buildSpec(labelRequest{
		title:       testCableID,
		description: "Office AP uplink from the patch panel in rack 3",
		url:         "https://example.com/item/x",
	}, cable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Rotation != 90 {
		t.Fatalf("expected the layout to be printed rotated, got %d", spec.Rotation)
	}
	if spec.Width != cable.heightMM || spec.Height != cable.widthMM {
		t.Fatalf("expected a %gx%gmm canvas, got %gx%g", cable.heightMM, cable.widthMM, spec.Width, spec.Height)
	}

	foldY := spec.Height / 2

	// The identity goes on the first face only; the second is for the description.
	titles := 0
	for _, item := range itemsOfType(spec, itemText) {
		if item.Text != testCableID {
			continue
		}
		titles++
		if item.Y >= foldY {
			t.Fatalf("expected the title on the first face, got y=%g", item.Y)
		}
	}
	if titles != 1 {
		t.Fatalf("expected exactly one title, got %d", titles)
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected exactly one QR code, got %d", len(codes))
	}
	if codes[0].Y >= foldY {
		t.Fatalf("expected the QR code on the first face, got y=%g", codes[0].Y)
	}

	// The second face gets the full width, so it holds more of the description
	// than the column beside the QR code does.
	var narrow, wide int
	for _, item := range itemsOfType(spec, itemText) {
		if item.Y >= foldY {
			wide++
			if item.Width <= spec.Width/2 {
				t.Fatalf("expected the second face to use the full width, got %g", item.Width)
			}
		} else if item.Text != testCableID {
			narrow++
		}
	}
	if wide == 0 {
		t.Fatal("expected the description on the second face")
	}
	if narrow == 0 {
		t.Fatal("expected the description to start on the first face too")
	}

	// The fold runs across the label's width, which on the rotated canvas is a
	// horizontal line splitting it into two equal faces. It is a hint for reading
	// the preview only.
	lines := itemsOfType(spec, itemLine)
	if len(lines) != 1 {
		t.Fatalf("expected one fold line, got %d", len(lines))
	}
	fold := lines[0]
	if fold.Y1 != spec.Height/2 || fold.Y1 != fold.Y2 || fold.X1 != 0 || fold.X2 != spec.Width {
		t.Fatalf("expected a horizontal fold line across the middle, got %+v", fold)
	}
	if !fold.previewOnly {
		t.Fatal("the fold hint must not be printed onto the label")
	}
	if printed := itemsOfType(spec.printable(), itemLine); len(printed) != 0 {
		t.Fatalf("expected no lines in the printed layout, got %d", len(printed))
	}

	// Nothing may cross the fold, or it would be cut in half.
	for _, item := range spec.Items {
		if item.Type == itemLine {
			continue
		}
		if item.Y < fold.Y1 && item.Y+item.Height > fold.Y1+0.01 {
			t.Fatalf("item straddles the fold: %+v", item)
		}
		if item.X+item.Width > spec.Width+0.01 || item.Y+item.Height > spec.Height+0.01 {
			t.Fatalf("item outside the label: %+v", item)
		}
	}
}

// The picture is the layout canvas, not the shape the label has on the roll: it
// shows the flag the way it reads once folded and fitted. The rotation stays in
// the layout for the printer to apply.
func TestCablePreviewIsTheLayoutCanvas(t *testing.T) {
	t.Setenv(EnvDPI, "203")

	cable := profiles[profileCable]

	raw, err := renderLabel(labelRequest{title: testCableID, url: "https://example.com/item/x"}, cable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("could not decode the preview: %v", err)
	}

	// The canvas is the label with its sides swapped, so the picture is too.
	scale := 203.0 / mmPerInch
	wantWidth := int(math.Round(cable.heightMM * scale))
	wantHeight := int(math.Round(cable.widthMM * scale))

	if got := decoded.Bounds().Size(); got.X != wantWidth || got.Y != wantHeight {
		t.Fatalf("expected a %dx%d picture of the %gx%gmm canvas, got %v",
			wantWidth, wantHeight, cable.heightMM, cable.widthMM, got)
	}

	spec := embeddedSpec(t, raw)
	if spec.Width != cable.heightMM || spec.Height != cable.widthMM || spec.Rotation != 90 {
		t.Fatalf("expected a rotated %gx%g layout, got %gx%g rotation %d",
			cable.heightMM, cable.widthMM, spec.Width, spec.Height, spec.Rotation)
	}
}

// The preview shows the fold so the label can be checked on screen; the embedded
// layout must not, or the printer draws a line down the middle of the label.
func TestCableLabelDoesNotEmbedTheFoldLine(t *testing.T) {
	raw, err := renderLabel(
		labelRequest{title: testCableID, description: "Office AP uplink", url: "https://example.com/item/x"},
		profiles[profileCable],
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec := embeddedSpec(t, raw)

	if lines := itemsOfType(spec, itemLine); len(lines) != 0 {
		t.Fatalf("expected no fold line in the embedded layout, got %+v", lines)
	}
	if spec.Rotation != 90 {
		t.Fatalf("expected the rotation to survive, got %d", spec.Rotation)
	}
	// The content itself still has to be there.
	if codes := itemsOfType(spec, itemQRCode); len(codes) != 1 {
		t.Fatalf("expected one QR code, got %d", len(codes))
	}
}

func TestResolveProfile(t *testing.T) {
	t.Setenv(EnvProfile, "")
	t.Setenv(EnvSize, "")

	if got := resolveProfile("", ""); got.name != defaultProfileName {
		t.Fatalf("expected the default profile, got %q", got.name)
	}
	if got := resolveProfile(profileCable, ""); got.name != profileCable {
		t.Fatalf("expected the requested profile, got %q", got.name)
	}
	// An unknown name must not fail the label; it falls back.
	if got := resolveProfile("nonsense", ""); got.name != defaultProfileName {
		t.Fatalf("expected a fallback, got %q", got.name)
	}

	custom := resolveProfile("", "60x40")
	if custom.widthMM != 60 || custom.heightMM != 40 {
		t.Fatalf("expected a 60x40 label, got %gx%g", custom.widthMM, custom.heightMM)
	}

	// A malformed size is ignored rather than fatal.
	for _, size := range []string{"60", "axb", "-1x10", "0x0"} {
		got := resolveProfile("", size)
		if got.widthMM != profiles[defaultProfileName].widthMM {
			t.Fatalf("expected %q to be ignored, got %gmm", size, got.widthMM)
		}
	}
}

func TestResolveProfileEnvironmentDefaults(t *testing.T) {
	t.Setenv(EnvProfile, profileCable)
	t.Setenv(EnvSize, "45x15")

	got := resolveProfile("", "")
	if got.name != profileCable || got.widthMM != 45 || got.heightMM != 15 {
		t.Fatalf("unexpected profile %+v", got)
	}

	// An explicit request still wins over the environment.
	if requested := resolveProfile(profileStandard, "50x25"); requested.name != profileStandard || requested.widthMM != 50 {
		t.Fatalf("unexpected profile %+v", requested)
	}
}

func TestWrapTextBreaksLongWordsAndKeepsLineLimit(t *testing.T) {
	set := loadedFonts()

	face, err := newFace(set.regular, 3*measureScale)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = face.Close() }()

	// 20mm at a 3mm character height holds only a few characters, so this single
	// word has to be split.
	lines := wrapText(strings.Repeat("W", 40), face, 20, 0)
	if len(lines) < 2 {
		t.Fatalf("expected the long word to be split, got %q", lines)
	}
	for _, line := range lines {
		if textWidth(face, line) > 20*measureScale+0.5 {
			t.Fatalf("line %q is wider than the label", line)
		}
	}

	if limited := wrapText(strings.Repeat("W", 40), face, 20, 1); len(limited) != 1 {
		t.Fatalf("expected the line limit to be honoured, got %d lines", len(limited))
	}

	// Blank paragraphs must not turn into empty lines.
	if got := wrapText("a\n\n\nb", face, 20, 0); len(got) != 2 {
		t.Fatalf("expected 2 lines, got %q", got)
	}
}

func TestLabelEndpointServesPNGWithLayout(t *testing.T) {
	service := httptest.NewServer(newMux())
	defer service.Close()

	query := url.Values{
		"TitleText":             {testAssetID},
		"DescriptionText":       {"Network switch"},
		"AdditionalInformation": {"Rack 3"},
		"URL":                   {"https://homebox.example.com/a/000-042"},
	}

	response, err := http.Get(service.URL + LabelPath + "?" + query.Encode())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", response.StatusCode)
	}
	// Homebox rejects a label service reply that is not an image.
	if got := response.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("unexpected content type %q", got)
	}

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec := embeddedSpec(t, raw)

	var text []string
	for _, item := range itemsOfType(spec, itemText) {
		text = append(text, item.Text)
	}
	joined := strings.Join(text, " ")

	for _, want := range []string{testAssetID, "Network switch", "Rack 3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q on the label, got %q", want, joined)
		}
	}
}

func TestEmbedTextRejectsNonPNG(t *testing.T) {
	for name, input := range map[string][]byte{
		"empty":         {},
		"not a png":     []byte("GIF89a"),
		"truncated png": pngSignature,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := embedText(input, specKeyword, "{}"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// The chunk has to come before the pixel data, so a reader finds the layout
// without decoding the image.
func TestEmbedTextInsertsBeforeImageData(t *testing.T) {
	var plain bytes.Buffer
	if err := png.Encode(&plain, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := embedText(plain.Bytes(), specKeyword, `{"hello":"world"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bytes.Index(raw, []byte("iTXt")) > bytes.Index(raw, []byte("IDAT")) {
		t.Fatal("expected the text chunk before the image data")
	}

	text, ok := findTextChunk(raw, specKeyword)
	if !ok || text != `{"hello":"world"}` {
		t.Fatalf("round trip mismatch: %q", text)
	}
}
