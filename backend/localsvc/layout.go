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

	// maxSubtitleLines caps the name printed under the asset ID. A second line is
	// usually a truncated fragment anyway, and on a 25x15mm label that room is
	// better spent on the description.
	maxSubtitleLines = 1

	// maxFooterLines caps the strip across the bottom. It holds one value — a
	// location path, or where an item lives — so two lines is generous.
	maxFooterLines = 2

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
// The asset ID leads when there is one: it is the number written on a shelf list
// and read back off the label, so it earns the large type, with the name below it
// for humans. Without an asset ID the name leads, which is how labels looked
// before the ID was available here.
func headline(req labelRequest) (primary string, secondary string) {
	if req.assetID != "" && req.assetID != req.title {
		return req.assetID, req.title
	}

	return req.title, ""
}

// labelRequest is the label content, split by where it goes rather than by where
// it came from.
//
// detail sits in the column beside the QR code, under the name. footer runs across
// the bottom of the label, which is the only place wide enough for something that
// reads as a sentence — a location path, or where an item lives.
type labelRequest struct {
	title   string
	assetID string
	detail  string
	footer  string
	url     string
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

	// Items are positioned on the canvas the printer draws on, which for a rotated
	// label is the label with its sides swapped.
	canvas := prof
	if prof.rotation == 90 || prof.rotation == 270 {
		canvas.widthMM, canvas.heightMM = prof.heightMM, prof.widthMM
	}

	spec := labelSpec{Width: canvas.widthMM, Height: canvas.heightMM, Rotation: prof.rotation}

	if canvas.flag {
		spec.Items = layoutFlag(req, canvas, titleFace, bodyFace)
	} else {
		spec.Items = layoutStandard(req, canvas, titleFace, bodyFace)
	}

	return spec, nil
}

// layoutStandard puts the QR code top left, the headline and detail in the column
// beside it, and the footer across the bottom of the label.
//
// The footer is placed first because it is anchored to the bottom edge; the column
// then gets everything above it. Whatever does not fit is dropped — a label is a
// summary.
func layoutStandard(req labelRequest, prof profile, titleFace, bodyFace font.Face) []labelItem {
	var items []labelItem

	qrSize := 0.0
	if req.url != "" {
		qrSize = prof.qrMM
		if qrSize <= 0 {
			qrSize = prof.heightMM - 2*prof.paddingMM
		}
		qrSize = min(qrSize, prof.widthMM*qrWidthShare)

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

	bodyLineHeight := prof.bodyMM * lineSpacing
	fullWidth := prof.widthMM - 2*prof.paddingMM

	// The footer is measured up front: it sits on the bottom edge, and where its
	// top lands decides how much height the column above has.
	footerLines := wrapText(req.footer, bodyFace, fullWidth, maxFooterLines)
	footerTop := prof.heightMM - prof.paddingMM - float64(len(footerLines))*bodyLineHeight

	primary, secondary := headline(req)

	cursor := appendLines(&items, wrapText(primary, titleFace, textWidth, maxTitleLines),
		textX, prof.paddingMM, textWidth, prof.titleMM, true)

	if secondary != "" {
		cursor = appendLines(&items, wrapText(secondary, bodyFace, textWidth, maxSubtitleLines),
			textX, cursor, textWidth, prof.bodyMM, false)
	}

	// The column may run past the bottom of the QR code — it is to the right of it
	// — but not into the footer.
	appendLines(&items, wrapText(req.detail, bodyFace, textWidth, int((footerTop-cursor)/bodyLineHeight)),
		textX, cursor, textWidth, prof.bodyMM, false)

	appendLines(&items, footerLines, prof.paddingMM, footerTop, fullWidth, prof.bodyMM, false)

	return items
}

// layoutFlag lays out a cable flag. The label is folded in half, so it has two
// faces and one of them always points away from the reader.
//
// The first face identifies the thing: QR code, asset ID and name. The second
// carries what it is for across the full width, where it has several times the
// room of a column squeezed in beside the QR code — its own description, or where
// it lives when it has none. Whichever way round the flag ends up folded, one
// useful side faces out.
//
// prof is the rotated canvas, so the fold that runs across the label's width is a
// horizontal line here, splitting the canvas into two wide, short faces stacked
// on top of each other.
func layoutFlag(req labelRequest, prof profile, titleFace, bodyFace font.Face) []labelItem {
	faceHeight := prof.heightMM / 2
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

	primary, secondary := headline(req)

	cursor := appendLines(&items, wrapText(primary, titleFace, textWidth, maxTitleLines),
		textX, prof.paddingMM, textWidth, prof.titleMM, true)

	if secondary != "" {
		remaining := faceHeight - prof.paddingMM - cursor
		appendLines(&items, wrapText(secondary, bodyFace, textWidth, min(maxSubtitleLines, int(remaining/bodyLineHeight))),
			textX, cursor, textWidth, prof.bodyMM, false)
	}

	// The second face: description only, across the full width.
	fullWidth := prof.widthMM - 2*prof.paddingMM
	appendLines(&items,
		wrapText(firstNonEmptyString(req.detail, req.footer), bodyFace, fullWidth,
			int((faceHeight-2*prof.paddingMM)/bodyLineHeight)),
		prof.paddingMM, faceHeight+prof.paddingMM, fullWidth, prof.bodyMM, false)

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
