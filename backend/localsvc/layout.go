package localsvc

import (
	"strings"

	"golang.org/x/image/font"
)

// Line breaks are computed here rather than left to the printer or the browser,
// and the layout carries one text item per line. That is what keeps the PNG
// preview and the Bluetooth print identical: both replay the same list of
// positioned lines instead of each running its own word wrapping.
const (
	// measureScale is the pixels-per-millimetre the text measurement runs at. It
	// is independent of the preview DPI so that changing the preview resolution
	// cannot move a line break.
	measureScale = 8.0

	// lineSpacing is the line height as a multiple of the character height.
	lineSpacing = 1.25

	// gapMM separates the QR code from the text block.
	gapMM = 1.2

	maxTitleLines = 2

	// maxSubtitleLines caps the asset ID under the name. The ID is short, so one
	// line is enough; on a 25x15mm label any leftover room goes to the description.
	maxSubtitleLines = 1

	// maxFooterLines caps the strip across the bottom. It holds one value — a
	// location path, or where an item lives — so two lines is generous.
	maxFooterLines = 2

	// maxTagLines caps how many lines of tags fit on an item label. Tags are a
	// secondary cue, so they should not crowd out the name on the small stock.
	maxTagLines = 2

	// maxCableTagLines is more generous: the cable front face has room beside
	// the QR code for several short tag lines before the fold.
	maxCableTagLines = 3

	// qrWidthShare caps how much of the label width the QR code may take. Fitting
	// it to the full height instead leaves the text a column too narrow to break
	// sensibly, and wastes the space under the code.
	qrWidthShare = 0.4

	// foldLineWidthMM draws the fold hint on a cable flag. It is preview-only, so
	// the line never reaches the label itself.
	foldLineWidthMM = 0.2
)

// headline splits the identifying text into the bold line and the smaller one
// under it.
//
// The name (item or location) leads in large type so it can be read at a glance.
// When there is a distinct asset ID it sits underneath in smaller type. If the
// title already is the asset ID, it is printed once to avoid wasting a line.
func headline(req labelRequest) (primary string, secondary string) {
	if req.assetID != "" && req.assetID != req.title {
		return req.title, req.assetID
	}

	return req.title, ""
}

// labelRequest is the label content, split by where it goes rather than by where
// it came from.
//
// On item labels the right column holds the name, then tags, then detail. On
// location labels it holds the name and footer (the full path). The asset ID
// always sits under the QR code on the left.
type labelRequest struct {
	title   string
	assetID string
	detail  string
	footer  string
	tags    []string
	url     string
}

// formatTags joins tag names for a label line.
func formatTags(tags []string) string {
	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		cleaned = append(cleaned, tag)
	}
	return strings.Join(cleaned, " / ")
}

// buildSpec turns the request into a positioned layout for the given stock.
func buildSpec(req labelRequest, prof profile) (labelSpec, error) {
	set := loadedFonts()

	titleFace, err := newFace(set.bold, prof.titleMM*measureScale)
	if err != nil {
		return labelSpec{}, err
	}
	defer func() { _ = titleFace.Close() }()

	bodyFace, err := newFace(set.regular, prof.bodyMM*measureScale)
	if err != nil {
		return labelSpec{}, err
	}
	defer func() { _ = bodyFace.Close() }()

	footerMM := footerSize(prof)
	footerFace := bodyFace
	if footerMM != prof.bodyMM {
		parsed, err := newFace(set.regular, footerMM*measureScale)
		if err != nil {
			return labelSpec{}, err
		}
		defer func() { _ = parsed.Close() }()
		footerFace = parsed
	}

	// Items are positioned on the canvas the printer draws on, which for a rotated
	// label is the label with its sides swapped.
	canvas := prof
	if prof.rotation == 90 || prof.rotation == 270 {
		canvas.widthMM, canvas.heightMM = prof.heightMM, prof.widthMM
	}

	spec := labelSpec{Width: canvas.widthMM, Height: canvas.heightMM, Rotation: prof.rotation}

	if canvas.flag {
		tagMM := cableFrontTagMM(canvas)
		tagFace := bodyFace
		if tagMM != prof.bodyMM {
			parsed, err := newFace(set.regular, tagMM*measureScale)
			if err != nil {
				return labelSpec{}, err
			}
			defer func() { _ = parsed.Close() }()
			tagFace = parsed
		}
		spec.Items = layoutFlag(req, canvas, titleFace, bodyFace, tagFace, tagMM)
	} else if canvas.name == profileLocation {
		spec.Items = layoutLocation(req, canvas, titleFace, bodyFace, footerFace, footerMM)
	} else {
		spec.Items = layoutStandard(req, canvas, titleFace, bodyFace, footerFace, footerMM)
	}

	return spec, nil
}

// cableFrontTagMM picks a tag size that fits maxCableTagLines under a one-line
// name on the front face. The face is short (~12.5mm), so bodyMM alone is often
// too tall for three tag lines.
func cableFrontTagMM(prof profile) float64 {
	usable := prof.heightMM/2 - 2*prof.paddingMM
	remaining := usable - prof.titleMM*lineSpacing
	if remaining <= 0 {
		return prof.bodyMM
	}

	fitted := remaining / (float64(maxCableTagLines) * lineSpacing)
	if fitted >= prof.bodyMM {
		return prof.bodyMM
	}
	const minTagMM = 1.4
	if fitted < minTagMM {
		return minTagMM
	}
	return fitted
}

// layoutStandard packs an item onto 25x15mm stock with the same left/right split
// as a location label: QR code over the asset ID on the left, name over tags and
// description on the right. The location path is left off — that belongs on a
// location label.
func layoutStandard(req labelRequest, prof profile, titleFace, bodyFace, _ font.Face, _ float64) []labelItem {
	var items []labelItem

	gap := contentGap(prof)
	bodyLineHeight := prof.bodyMM * lineSpacing

	idText := ""
	if req.assetID != "" && req.assetID != req.title {
		idText = req.assetID
	}

	idBlock := 0.0
	if idText != "" {
		idBlock = bodyLineHeight
	}

	qrSize := 0.0
	if req.url != "" {
		maxQR := prof.heightMM - 2*prof.paddingMM
		if idBlock > 0 {
			maxQR -= idBlock + gap*0.4
		}
		qrSize = prof.qrMM
		if qrSize <= 0 {
			qrSize = maxQR
		}
		qrSize = min(qrSize, maxQR, prof.widthMM*qrWidthCap(prof))

		items = append(items, labelItem{
			Type:   itemQRCode,
			X:      prof.paddingMM,
			Y:      prof.paddingMM,
			Width:  qrSize,
			Height: qrSize,
			Text:   req.url,
		})
	}

	leftWidth := qrSize
	if leftWidth <= 0 && idText != "" {
		// No code: give the asset ID a narrow left column so the name still owns
		// most of the width on the right.
		leftWidth = min(prof.widthMM*qrWidthCap(prof), prof.widthMM/2)
	}

	if idText != "" {
		idY := prof.paddingMM + qrSize
		if qrSize > 0 {
			idY += gap * 0.4
		}
		appendLines(&items, wrapText(idText, bodyFace, leftWidth, 1),
			prof.paddingMM, idY, leftWidth, prof.bodyMM, false)
	}

	textX := prof.paddingMM
	if leftWidth > 0 {
		textX += leftWidth + gap
	}
	textWidth := prof.widthMM - textX - prof.paddingMM
	if textWidth <= 0 {
		return items
	}

	primary, _ := headline(req)
	cursor := appendLines(&items, wrapText(primary, titleFace, textWidth, maxTitleLines),
		textX, prof.paddingMM, textWidth, prof.titleMM, true)

	if tagText := formatTags(req.tags); tagText != "" {
		remaining := int((prof.heightMM - prof.paddingMM - cursor) / bodyLineHeight)
		cursor = appendLines(&items, wrapText(tagText, bodyFace, textWidth, min(maxTagLines, remaining)),
			textX, cursor, textWidth, prof.bodyMM, false)
	}

	if req.detail != "" {
		remaining := int((prof.heightMM - prof.paddingMM - cursor) / bodyLineHeight)
		if remaining > 0 {
			appendLines(&items, wrapText(req.detail, bodyFace, textWidth, remaining),
				textX, cursor, textWidth, prof.bodyMM, false)
		}
	}

	return items
}

// layoutLocation packs a location onto the same 25x15mm stock as an item label.
//
// The face is split left/right: QR code over the asset ID on the left, name over
// the full ancestry path on the right. Description and tags are omitted so a long
// path can still wrap in the right column.
func layoutLocation(req labelRequest, prof profile, titleFace, bodyFace, footerFace font.Face, footerMM float64) []labelItem {
	var items []labelItem

	gap := contentGap(prof)
	bodyLineHeight := prof.bodyMM * lineSpacing
	footerLineHeight := footerMM * lineSpacing

	idText := ""
	if req.assetID != "" && req.assetID != req.title {
		idText = req.assetID
	}

	idBlock := 0.0
	if idText != "" {
		idBlock = bodyLineHeight
	}

	qrSize := 0.0
	if req.url != "" {
		maxQR := prof.heightMM - 2*prof.paddingMM
		if idBlock > 0 {
			maxQR -= idBlock + gap*0.4
		}
		qrSize = prof.qrMM
		if qrSize <= 0 {
			qrSize = maxQR
		}
		qrSize = min(qrSize, maxQR, prof.widthMM*qrWidthCap(prof))

		items = append(items, labelItem{
			Type:   itemQRCode,
			X:      prof.paddingMM,
			Y:      prof.paddingMM,
			Width:  qrSize,
			Height: qrSize,
			Text:   req.url,
		})
	}

	leftWidth := qrSize
	if leftWidth <= 0 && idText != "" {
		// No code: give the asset ID a narrow left column so the path still owns
		// most of the width on the right.
		leftWidth = min(prof.widthMM*qrWidthCap(prof), prof.widthMM/2)
	}

	if idText != "" {
		idY := prof.paddingMM + qrSize
		if qrSize > 0 {
			idY += gap * 0.4
		}
		appendLines(&items, wrapText(idText, bodyFace, leftWidth, 1),
			prof.paddingMM, idY, leftWidth, prof.bodyMM, false)
	}

	textX := prof.paddingMM
	if leftWidth > 0 {
		textX += leftWidth + gap
	}
	textWidth := prof.widthMM - textX - prof.paddingMM
	if textWidth <= 0 {
		return items
	}

	primary, _ := headline(req)
	cursor := appendLines(&items, wrapText(primary, titleFace, textWidth, maxTitleLines),
		textX, prof.paddingMM, textWidth, prof.titleMM, true)

	if req.footer != "" {
		remaining := int((prof.heightMM - prof.paddingMM - cursor) / footerLineHeight)
		if remaining > 0 {
			appendLines(&items, wrapText(req.footer, footerFace, textWidth, remaining),
				textX, cursor, textWidth, footerMM, false)
		}
	}

	return items
}

// layoutFlag lays out a cable flag. The label is folded in half, so it has two
// faces and one of them always points away from the reader.
//
// The first face identifies the cable at a glance: QR code, name and tags. The
// second carries the asset ID and the description across the full width — room
// the first face does not have beside the QR code. Whichever way round the flag
// ends up folded, one useful side faces out.
//
// prof is the rotated canvas, so the fold that runs across the label's width is a
// horizontal line here, splitting the canvas into two wide, short faces stacked
// on top of each other.
func layoutFlag(req labelRequest, prof profile, titleFace, bodyFace, tagFace font.Face, tagMM float64) []labelItem {
	faceHeight := prof.heightMM / 2
	tagLineHeight := tagMM * lineSpacing
	bodyLineHeight := prof.bodyMM * lineSpacing

	items := []labelItem{{
		Type:        itemLine,
		X1:          0,
		Y1:          faceHeight,
		X2:          prof.widthMM,
		Y2:          faceHeight,
		LineWidth:   foldLineWidthMM,
		previewOnly: true,
	}}

	qrSize := 0.0
	if req.url != "" {
		qrSize = prof.qrMM
		if qrSize <= 0 {
			qrSize = faceHeight - 2*prof.paddingMM
		}
		qrSize = min(qrSize, faceHeight-2*prof.paddingMM, prof.widthMM*qrWidthShare)

		items = append(items, labelItem{
			Type:   itemQRCode,
			X:      prof.paddingMM,
			Y:      prof.paddingMM,
			Width:  qrSize,
			Height: qrSize,
			Text:   req.url,
		})
	}

	textX := prof.paddingMM
	if qrSize > 0 {
		textX += qrSize + gapMM
	}
	textWidth := prof.widthMM - textX - prof.paddingMM

	// Front face: one-line name, then up to three tag lines. The asset ID moves
	// to the back so the short face can spend its height on tags.
	cursor := appendLines(&items, wrapText(req.title, titleFace, textWidth, 1),
		textX, prof.paddingMM, textWidth, prof.titleMM, true)

	if tagText := formatTags(req.tags); tagText != "" {
		remaining := faceHeight - prof.paddingMM - cursor
		appendLines(&items, wrapText(tagText, tagFace, textWidth, min(maxCableTagLines, int(remaining/tagLineHeight))),
			textX, cursor, textWidth, tagMM, false)
	}

	// Back face: asset ID, then description (falling back to the location path).
	fullWidth := prof.widthMM - 2*prof.paddingMM
	backY := faceHeight + prof.paddingMM
	backBottom := prof.heightMM - prof.paddingMM

	if req.assetID != "" {
		backY = appendLines(&items, wrapText(req.assetID, titleFace, fullWidth, 1),
			prof.paddingMM, backY, fullWidth, prof.titleMM, true)
	}

	backText := firstNonEmptyString(req.detail, req.footer)
	if backText != "" {
		appendLines(&items, wrapText(backText, bodyFace, fullWidth, int((backBottom-backY)/bodyLineHeight)),
			prof.paddingMM, backY, fullWidth, prof.bodyMM, false)
	}

	return items
}

// appendLines adds one text item per line and returns the y coordinate the next
// block starts at. Each line gets its own box with the text centred vertically,
// which keeps the browser's idea of a character's height from shifting the line
// off its row.
func appendLines(items *[]labelItem, lines []string, x float64, y float64, width float64, fontHeight float64, bold bool) float64 {
	lineHeight := fontHeight * lineSpacing

	for _, line := range lines {
		*items = append(*items, labelItem{
			Type:       itemText,
			X:          x,
			Y:          y,
			Width:      width,
			Height:     lineHeight,
			Text:       line,
			FontHeight: fontHeight,
			Bold:       bold,
			VAlign:     "center",
			Wrap:       "none",
		})
		y += lineHeight
	}

	return y
}

// wrapText breaks text to fit maxWidthMM, preferring word boundaries but
// splitting inside a run when a single word is too long — which is the normal
// case for Chinese, where there are no spaces. maxLines <= 0 means unlimited.
func wrapText(text string, face font.Face, maxWidthMM float64, maxLines int) []string {
	maxWidthPx := maxWidthMM * measureScale
	if maxWidthPx <= 0 {
		return nil
	}

	var lines []string

	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}

		for _, line := range wrapParagraph(paragraph, face, maxWidthPx) {
			if maxLines > 0 && len(lines) >= maxLines {
				return lines
			}
			lines = append(lines, line)
		}
	}

	return lines
}

func wrapParagraph(paragraph string, face font.Face, maxWidthPx float64) []string {
	var (
		lines   []string
		current string
	)

	// Trailing spaces are what let a break fall after a space rather than before
	// the next word, but they must not survive into the printed line: they would
	// widen a centred line and double up if the line is re-joined later.
	flush := func() {
		if trimmed := strings.TrimRight(current, " "); trimmed != "" {
			lines = append(lines, trimmed)
		}
		current = ""
	}

	for _, word := range splitWords(paragraph) {
		candidate := current + word
		if textWidth(face, candidate) <= maxWidthPx {
			current = candidate
			continue
		}

		flush()

		// A single word wider than the label still has to go somewhere, so it is
		// broken by character.
		for textWidth(face, word) > maxWidthPx {
			head, tail := splitToWidth(word, face, maxWidthPx)
			if head == "" {
				break
			}
			lines = append(lines, head)
			word = tail
		}

		current = word
	}

	flush()

	return lines
}

// splitWords keeps trailing spaces attached to the word before them, so that a
// line break falls after the space rather than before the next word.
func splitWords(paragraph string) []string {
	var (
		words []string
		start int
		seen  bool
	)

	for i, r := range paragraph {
		switch {
		case r == ' ':
			seen = true
		case seen:
			words = append(words, paragraph[start:i])
			start = i
			seen = false
		}
	}

	if start < len(paragraph) {
		words = append(words, paragraph[start:])
	}

	return words
}

// splitToWidth cuts word at the last character that still fits. Iterating a
// string yields rune boundaries, so a multi-byte character is never halved.
func splitToWidth(word string, face font.Face, maxWidthPx float64) (head string, tail string) {
	previous := 0

	for i := range word {
		if i == 0 {
			continue
		}

		if textWidth(face, word[:i]) > maxWidthPx {
			if previous == 0 {
				// Even one character overflows the label. Emit it regardless, so
				// that wrapping makes progress instead of looping forever.
				return word[:i], word[i:]
			}
			return word[:previous], word[previous:]
		}

		previous = i
	}

	return word, ""
}

func textWidth(face font.Face, text string) float64 {
	return float64(font.MeasureString(face, text)) / 64
}
