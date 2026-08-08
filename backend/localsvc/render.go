package localsvc

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/skip2/go-qrcode"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The preview is greyscale: labels are black on white, and 8-bit grey keeps the
// anti-aliased glyph edges that make small text legible on screen while staying
// a fraction of the size of an RGBA PNG.
const (
	white = 255
	black = 0

	// qrRecoveryLevel matches what Homebox's own renderer uses.
	qrRecoveryLevel = qrcode.Medium
)

// renderPreview rasterises a layout at the given resolution. The layout is the
// single source of truth for both outputs, so what comes out here is what the
// printer draws.
//
// The image is the layout canvas as it stands, which for a rotated label is the
// label as it reads once it is folded and fitted — not the shape it has coming
// off the roll. The rotation is left for the printer to apply, so anything that
// prints this picture instead of the layout (the browser print dialog, a
// configured print command) will need the paper turned to match.
func renderPreview(spec labelSpec, dpi float64) (image.Image, error) {
	scale := dpi / mmPerInch

	bounds := image.Rect(0, 0, mmToPx(spec.Width, scale), mmToPx(spec.Height, scale))
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, errInvalidSize
	}

	img := image.NewGray(bounds)
	draw.Draw(img, bounds, image.NewUniform(color.Gray{Y: white}), image.Point{}, draw.Src)

	set := loadedFonts()

	for i := range spec.Items {
		item := &spec.Items[i]

		var err error
		switch item.Type {
		case itemQRCode:
			err = drawQRCode(img, item, scale)
		case itemText:
			err = drawText(img, item, scale, set)
		case itemLine:
			drawLine(img, item, scale)
		}

		if err != nil {
			return nil, err
		}
	}

	return img, nil
}

func drawQRCode(img *image.Gray, item *labelItem, scale float64) error {
	code, err := qrcode.New(item.Text, qrRecoveryLevel)
	if err != nil {
		return err
	}

	// The layout box already accounts for the margin around the code, and the
	// printer draws it borderless too.
	code.DisableBorder = true

	box := image.Rect(
		mmToPx(item.X, scale),
		mmToPx(item.Y, scale),
		mmToPx(item.X+item.Width, scale),
		mmToPx(item.Y+item.Height, scale),
	)

	// Nearest neighbour keeps the module edges hard; smoothing them would make
	// the code harder to scan off the screen.
	source := code.Image(box.Dx())
	xdraw.NearestNeighbor.Scale(img, box, source, source.Bounds(), draw.Over, nil)

	return nil
}

func drawText(img *image.Gray, item *labelItem, scale float64, set fontSet) error {
	parsed := set.regular
	if item.Bold {
		parsed = set.bold
	}

	face, err := newFace(parsed, item.FontHeight*scale)
	if err != nil {
		return err
	}
	defer func() { _ = face.Close() }()

	drawer := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.Gray{Y: black}),
		Face: face,
	}

	x := item.X * scale
	if item.Align == "center" || item.Align == "end" {
		free := item.Width*scale - float64(drawer.MeasureString(item.Text))/64
		if free > 0 {
			if item.Align == "center" {
				x += free / 2
			} else {
				x += free
			}
		}
	}

	drawer.Dot = fixedPoint(x, baseline(item, face, scale))
	drawer.DrawString(item.Text)

	// Without a bold cut of the font, weight is faked by drawing the glyphs again
	// a fraction to the right.
	if item.Bold && set.syntheticBold {
		drawer.Dot = fixedPoint(x+math.Max(1, scale*0.1), baseline(item, face, scale))
		drawer.DrawString(item.Text)
	}

	return nil
}

// baseline centres the glyphs vertically in their box, matching the "center"
// vertical alignment the layout asks the printer for.
func baseline(item *labelItem, face font.Face, scale float64) float64 {
	metrics := face.Metrics()
	ascent := float64(metrics.Ascent) / 64
	descent := float64(metrics.Descent) / 64

	if item.Height <= 0 {
		return item.Y*scale + ascent
	}

	return (item.Y+item.Height/2)*scale + (ascent-descent)/2
}

// drawLine only handles horizontal and vertical lines, which is all the layouts
// here contain (the fold hint on a cable flag).
func drawLine(img *image.Gray, item *labelItem, scale float64) {
	width := math.Max(item.LineWidth*scale, 1)

	x1, x2 := math.Min(item.X1, item.X2)*scale, math.Max(item.X1, item.X2)*scale
	y1, y2 := math.Min(item.Y1, item.Y2)*scale, math.Max(item.Y1, item.Y2)*scale

	if x2-x1 < width {
		x2 = x1 + width
	}
	if y2-y1 < width {
		y2 = y1 + width
	}

	rect := image.Rect(int(x1), int(y1), int(math.Ceil(x2)), int(math.Ceil(y2)))
	draw.Draw(img, rect.Intersect(img.Bounds()), image.NewUniform(color.Gray{Y: black}), image.Point{}, draw.Src)
}

func mmToPx(mm float64, scale float64) int {
	return int(math.Round(mm * scale))
}

func fixedPoint(x float64, y float64) fixed.Point26_6 {
	return fixed.Point26_6{
		X: fixed.Int26_6(x * 64),
		Y: fixed.Int26_6(y * 64),
	}
}
