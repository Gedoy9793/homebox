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
	testAssetID      = "000-042"
	testCableID      = "SW1-P24"
	testItemName     = "Netgear switch"
	testLocationName = "Rack 3"

	// The label URL Homebox builds for the asset ID above.
	testAssetURL = "https://homebox.example.com/a/000-042"
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
		title:  testAssetID,
		detail: "Network switch",
		url:    "https://homebox.example.com/item/abc",
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
	if spec.GapType != 2 || spec.GapLength != 6 {
		t.Fatalf("expected 25x15mm gap stock with a 6mm gap, got type %d and gap %gmm", spec.GapType, spec.GapLength)
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

func TestLabelStockSettingsFollowProfile(t *testing.T) {
	cableAtStandardSize := profiles[profileCable]
	cableAtStandardSize.widthMM = 25
	cableAtStandardSize.heightMM = 15

	tests := []struct {
		name    string
		profile profile
		wantGap bool
	}{
		{name: profileStandard, profile: profiles[profileStandard], wantGap: true},
		{name: profileLocation, profile: profiles[profileLocation], wantGap: true},
		{name: profileCable, profile: profiles[profileCable]},
		{name: "cable with 25x15 override", profile: cableAtStandardSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := buildSpec(labelRequest{title: "Test label"}, tt.profile)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			raw, err := json.Marshal(spec.printable())
			if err != nil {
				t.Fatalf("could not encode the label spec: %v", err)
			}

			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("could not decode the label spec: %v", err)
			}

			gapType, hasGapType := payload["gapType"]
			gapLength, hasGapLength := payload["gapLength"]
			if !tt.wantGap {
				if hasGapType || hasGapLength {
					t.Fatalf("expected no stock gap settings, got %s", raw)
				}
				return
			}

			if !hasGapType || string(gapType) != "2" {
				t.Fatalf("expected gapType 2, got %s", raw)
			}
			if !hasGapLength || string(gapLength) != "6" {
				t.Fatalf("expected gapLength 6, got %s", raw)
			}
		})
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

// The name is what you glance at on the shelf, so it takes the bold line; the
// asset ID sits under it in smaller type. Without an asset ID — or when the title
// already is the ID — the name keeps the bold line alone.
func TestHeadlineLeadsWithTheName(t *testing.T) {
	cases := map[string]struct {
		request            labelRequest
		primary, secondary string
	}{
		"asset id and name": {
			request:   labelRequest{title: testItemName, assetID: testAssetID},
			primary:   testItemName,
			secondary: testAssetID,
		},
		"name only": {
			request: labelRequest{title: testItemName},
			primary: testItemName,
		},
		// An asset label's title already is the asset ID; printing it twice would
		// waste the one line the small stock has.
		"title is the asset id": {
			request: labelRequest{title: testAssetID, assetID: testAssetID},
			primary: testAssetID,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			primary, secondary := headline(testCase.request)
			if primary != testCase.primary || secondary != testCase.secondary {
				t.Fatalf("expected %q/%q, got %q/%q", testCase.primary, testCase.secondary, primary, secondary)
			}
		})
	}
}

// Item labels lead with the name on the right and put the smaller asset ID under
// the QR code on the left.
func TestItemLabelLeadsWithTheName(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:   "Switch",
		assetID: testAssetID,
		url:     "https://example.com/a/000-042",
	}, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected one QR code, got %d", len(codes))
	}
	codeRight := codes[0].X + codes[0].Width
	codeBottom := codes[0].Y + codes[0].Height

	var name, id *labelItem
	for _, item := range itemsOfType(spec, itemText) {
		switch item.Text {
		case "Switch":
			cp := item
			name = &cp
		case testAssetID:
			cp := item
			id = &cp
		}
	}
	if name == nil {
		t.Fatal("expected the name on the label")
	}
	if id == nil {
		t.Fatal("expected the asset ID on the label")
	}
	if !name.Bold || name.X+0.01 < codeRight {
		t.Errorf("expected the name bold in the right column, got %+v", name)
	}
	if id.Bold || id.X > codes[0].X+0.01 || id.Y+0.01 < codeBottom {
		t.Errorf("expected the asset ID under the QR code, got %+v", id)
	}
	if id.FontHeight >= name.FontHeight {
		t.Errorf("expected the asset ID to be smaller than the name, got %g and %g",
			id.FontHeight, name.FontHeight)
	}
}

// Item labels omit the location path; the right column is for name, tags and
// description instead.
func TestItemLabelOmitsLocationPath(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:   testItemName,
		footer:  testLocationName,
		detail:  "Spare parts",
		assetID: testAssetID,
		url:     "https://example.com/a/000-042",
	}, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, item := range itemsOfType(spec, itemText) {
		if item.Text == testLocationName || strings.Contains(item.Text, testLocationName) {
			t.Fatalf("expected the location path to be omitted, got %q", item.Text)
		}
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

// cableSpec builds a cable flag with all the content a label can carry, so the
// tests below can each check one aspect of how the two faces divide it up.
func cableSpec(t *testing.T) labelSpec {
	t.Helper()

	spec, err := buildSpec(labelRequest{
		title:   testCableID,
		detail:  "Office AP uplink from the patch panel in rack 3",
		assetID: testAssetID,
		tags:    []string{"uplink", "office"},
		url:     "https://example.com/item/x",
	}, profiles[profileCable])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return spec
}

// A cable flag prints rotated, so the layout is built on a canvas with the
// label's sides swapped.
func TestCableProfileIsARotatedCanvas(t *testing.T) {
	cable := profiles[profileCable]
	spec := cableSpec(t)

	if spec.Rotation != 90 {
		t.Fatalf("expected the layout to be printed rotated, got %d", spec.Rotation)
	}
	if spec.Width != cable.heightMM || spec.Height != cable.widthMM {
		t.Fatalf("expected a %gx%gmm canvas, got %gx%g", cable.heightMM, cable.widthMM, spec.Width, spec.Height)
	}
}

// The first face identifies the cable: QR code, name and tags. The asset ID
// belongs on the back with the description.
func TestCableFirstFaceCarriesTheNameAndTags(t *testing.T) {
	spec := cableSpec(t)
	foldY := spec.Height / 2

	foundName := 0
	foundTag := 0
	for _, item := range itemsOfType(spec, itemText) {
		if item.Y >= foldY {
			continue
		}
		switch {
		case item.Text == testCableID:
			foundName++
		case strings.Contains(item.Text, "uplink") || strings.Contains(item.Text, "office"):
			foundTag++
		case item.Text == testAssetID:
			t.Errorf("expected the asset ID on the second face, got it at y=%g", item.Y)
		}
	}
	if foundName != 1 {
		t.Errorf("expected the name on the first face once, got %d", foundName)
	}
	if foundTag == 0 {
		t.Error("expected tags on the first face")
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected exactly one QR code, got %d", len(codes))
	}
	if codes[0].Y >= foldY {
		t.Errorf("expected the QR code on the first face, got y=%g", codes[0].Y)
	}
}

// Cable front faces may use up to three tag lines when the joined tags wrap.
func TestCableFirstFaceAllowsThreeTagLines(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:   testCableID,
		assetID: testAssetID,
		tags: []string{
			"uplink-primary",
			"office-floor-2",
			"rack-a-patch",
			"poe-injector",
			"trunk-fiber",
			"spare-cold",
		},
		url: "https://example.com/item/x",
	}, profiles[profileCable])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foldY := spec.Height / 2
	tagLines := 0
	for _, item := range itemsOfType(spec, itemText) {
		if item.Y >= foldY || item.Text == testCableID {
			continue
		}
		tagLines++
	}
	if tagLines != maxCableTagLines {
		t.Fatalf("expected %d tag lines on the first face, got %d", maxCableTagLines, tagLines)
	}
}

// The second face carries the asset ID and the description across the full width.
func TestCableSecondFaceCarriesTheIDAndDescription(t *testing.T) {
	spec := cableSpec(t)
	foldY := spec.Height / 2

	foundID := false
	foundDesc := false
	for _, item := range itemsOfType(spec, itemText) {
		if item.Y < foldY {
			continue
		}
		if item.Text == testAssetID {
			foundID = true
		}
		if strings.Contains(item.Text, "Office") || strings.Contains(item.Text, "patch") {
			foundDesc = true
		}
		if item.Width <= spec.Width/2 {
			t.Errorf("expected the second face to use the full width, got %g", item.Width)
		}
	}

	if !foundID {
		t.Error("expected the asset ID on the second face")
	}
	if !foundDesc {
		t.Error("expected the description on the second face")
	}
}

// Item labels print tags in the right column under the name.
func TestItemLabelIncludesTags(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:   "Switch",
		assetID: testAssetID,
		tags:    []string{"network", "core"},
		url:     "https://example.com/a/000-042",
	}, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected one QR code, got %d", len(codes))
	}
	codeRight := codes[0].X + codes[0].Width

	joined := ""
	foundTag := false
	for _, item := range itemsOfType(spec, itemText) {
		joined += item.Text + " "
		if strings.Contains(item.Text, "network") {
			foundTag = true
			if item.X+0.01 < codeRight {
				t.Errorf("expected tags in the right column, got x=%g", item.X)
			}
		}
	}
	if !foundTag {
		t.Fatalf("expected tags on the item label, got %q", joined)
	}
}

// The fold runs across the label's width, which on the rotated canvas is a
// horizontal line splitting it into two equal faces. It is a hint for reading the
// preview only, and nothing may straddle it.
func TestCableFoldSplitsTheFacesCleanly(t *testing.T) {
	spec := cableSpec(t)

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

	for _, item := range spec.Items {
		if item.Type == itemLine {
			continue
		}
		if item.Y < fold.Y1 && item.Y+item.Height > fold.Y1+0.01 {
			t.Errorf("item straddles the fold: %+v", item)
		}
		if item.X+item.Width > spec.Width+0.01 || item.Y+item.Height > spec.Height+0.01 {
			t.Errorf("item outside the label: %+v", item)
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
		labelRequest{title: testCableID, detail: "Office AP uplink", url: "https://example.com/item/x"},
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
		"AdditionalInformation": {testLocationName},
		"URL":                   {testAssetURL},
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

	for _, want := range []string{testAssetID, "Network switch", testLocationName} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q on the label, got %q", want, joined)
		}
	}
}

// The label is built from the record, not from the text labelmaker assembled, so
// it gets the name and asset ID from the database instead of the request text.
func TestLabelEndpointPrefersTheRecordOverTheRequestText(t *testing.T) {
	bindTestRecord(t, "endpoint")

	service := httptest.NewServer(newMux())
	defer service.Close()

	query := url.Values{
		"URL":             {testAssetURL},
		"TitleText":       {"ignored title"},
		"DescriptionText": {"Location: ignored place"},
		// The stock is chosen from the record's type too, and the test record is a
		// 线缆, so ask for the small one to keep this about the text.
		"LabelProfile": {profileStandard},
	}

	response, err := http.Get(service.URL + LabelPath + "?" + query.Encode())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var text []string
	for _, item := range itemsOfType(embeddedSpec(t, raw), itemText) {
		text = append(text, item.Text)
	}
	joined := strings.Join(text, " ")

	for _, want := range []string{testAssetID, "Office AP"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q on the label, got %q", want, joined)
		}
	}
	for _, unwanted := range []string{"ignored", "Location", testLocationName} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("expected %q to be left out, got %q", unwanted, joined)
		}
	}
}

// The footer is the full path for location records; item records do not use it
// on the printed label.
func TestRecordFooter(t *testing.T) {
	location := entityRecord{
		name:        "Shelf 2",
		description: "Spare parts",
		location:    "Cupboard A",
		path:        []string{"Garage", "Cupboard A"},
		isLocation:  true,
	}

	if got := recordFooter(location); got != "Garage / Cupboard A / Shelf 2" {
		t.Fatalf("unexpected footer %q", got)
	}

	// An item still has a path in the record, used only if something asks for it.
	item := entityRecord{
		location: testLocationName,
		path:     []string{"Garage", "Cupboard A", testLocationName},
	}
	if got := recordFooter(item); got != "Garage / Cupboard A / "+testLocationName {
		t.Fatalf("unexpected footer %q", got)
	}

	// Without a path, fall back to the parent name alone.
	if got := recordFooter(entityRecord{location: testLocationName}); got != testLocationName {
		t.Fatalf("unexpected footer %q", got)
	}
}

// A location label on 25x15mm stock is split left/right: QR over asset ID, name
// over the full path. Description is omitted so the path keeps the room it needs.
func TestLocationLabelShowsNameIDAndPath(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:   "Shelf 2",
		detail:  "Spare parts",
		footer:  "Garage / Cupboard A",
		assetID: testAssetID,
		url:     "https://homebox.example.com/location/abc",
	}, profiles[profileLocation])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Width != 25 || spec.Height != 15 {
		t.Fatalf("expected a 25x15mm label, got %gx%g", spec.Width, spec.Height)
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected one QR code, got %d", len(codes))
	}
	code := codes[0]
	codeRight := code.X + code.Width
	codeBottom := code.Y + code.Height

	var text []string
	foundID := false
	foundPath := false
	for _, item := range itemsOfType(spec, itemText) {
		text = append(text, item.Text)
		switch {
		case item.Text == "Spare parts":
			t.Errorf("expected the description to be omitted on a location label, got %q", item.Text)
		case item.Text == testAssetID:
			foundID = true
			if item.X > code.X+0.01 {
				t.Errorf("expected the asset ID in the left column, got x=%g", item.X)
			}
			if item.Y+0.01 < codeBottom {
				t.Errorf("expected the asset ID under the QR code, got y=%g (code ends at %g)", item.Y, codeBottom)
			}
		case strings.Contains(item.Text, "Garage"):
			foundPath = true
			if item.X+0.01 < codeRight {
				t.Errorf("expected the path in the right column, got x=%g (code ends at %g)", item.X, codeRight)
			}
			want := footerSize(profiles[profileLocation])
			if item.FontHeight < want-0.01 || item.FontHeight > want+0.01 {
				t.Errorf("expected the path at %gmm, got %g", want, item.FontHeight)
			}
		case item.Text == "Shelf 2":
			if item.X+0.01 < codeRight {
				t.Errorf("expected the name in the right column, got x=%g", item.X)
			}
		}
	}
	if !foundID {
		t.Error("expected the asset ID under the QR code")
	}
	if !foundPath {
		t.Error("expected the path in the right column")
	}

	joined := strings.Join(text, " ")
	for _, want := range []string{testAssetID, "Shelf 2", "Garage"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q on the label, got %q", want, joined)
		}
	}
	if strings.Contains(joined, "Homebox") {
		t.Errorf("expected the placeholder to be gone, got %q", joined)
	}
}

// Item labels omit the location path so the right column can hold tags and detail.
func TestItemLabelOmitsPathEvenWhenProvided(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:   "Switch",
		footer:  "Garage / Cupboard A / Rack 3",
		detail:  "24-port gigabit",
		assetID: testAssetID,
		url:     "https://example.com/a/000-042",
	}, profiles[profileStandard])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := ""
	for _, item := range itemsOfType(spec, itemText) {
		joined += item.Text + " "
		if strings.Contains(item.Text, "Garage") || strings.Contains(item.Text, "Rack") {
			t.Fatalf("expected no location path on the item label, got %q", item.Text)
		}
	}
	if !strings.Contains(joined, "24-port") {
		t.Fatalf("expected the description on the item label, got %q", joined)
	}
}

// A long location path wraps in the right column at the dedicated footer size.
func TestLocationPathWrapsOntoSecondLine(t *testing.T) {
	spec, err := buildSpec(labelRequest{
		title:  "Shelf 2",
		footer: "Garage / Cupboard A / Left wall / Top shelf / Spare parts bin",
		url:    "https://homebox.example.com/location/abc",
	}, profiles[profileLocation])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	codes := itemsOfType(spec, itemQRCode)
	if len(codes) != 1 {
		t.Fatalf("expected one QR code, got %d", len(codes))
	}
	codeRight := codes[0].X + codes[0].Width

	var pathLines []labelItem
	for _, item := range itemsOfType(spec, itemText) {
		if item.FontHeight != footerSize(profiles[profileLocation]) {
			continue
		}
		if item.X+0.01 < codeRight {
			t.Errorf("expected path lines in the right column, got x=%g", item.X)
		}
		pathLines = append(pathLines, item)
	}
	if len(pathLines) < 2 {
		t.Fatalf("expected the path to wrap onto two lines, got %d", len(pathLines))
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
