package localsvc

import (
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

// labelSpec mirrors the layout contract in frontend/lib/labels/label-spec.ts.
// Only the item kinds this service emits are modelled; the browser accepts more.
//
// All geometry is in millimetres. The browser replays it through the printer's
// own text and QR commands, so the print comes out at the print head's real
// resolution instead of being an upscaled copy of the PNG preview.
type labelSpec struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`

	// Rotation turns the whole layout when it is printed, so a tall label can be
	// drawn — and read — the long way round. Width and Height describe the canvas
	// the items are positioned on, which is the label turned by this angle.
	Rotation int `json:"rotation,omitempty"`

	Items []labelItem `json:"items"`
}

// labelItem is the union of the item shapes this service emits. Zero values are
// omitted, which is safe because the browser treats a missing field as its
// default, and every default here is zero.
type labelItem struct {
	Type string `json:"type"`

	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`

	// text
	Text       string  `json:"text,omitempty"`
	FontHeight float64 `json:"fontHeight,omitempty"`
	Bold       bool    `json:"bold,omitempty"`
	Align      string  `json:"align,omitempty"`
	VAlign     string  `json:"valign,omitempty"`
	Wrap       string  `json:"wrap,omitempty"`

	// line
	X1 float64 `json:"x1,omitempty"`
	Y1 float64 `json:"y1,omitempty"`
	X2 float64 `json:"x2,omitempty"`
	Y2 float64 `json:"y2,omitempty"`

	LineWidth float64 `json:"lineWidth,omitempty"`

	// previewOnly keeps an item out of the printed layout. The fold hint is a
	// guide for the eye when checking the label on screen; printing it would put
	// a line down the middle of the actual label. Unexported, so it cannot leak
	// into the JSON either way.
	previewOnly bool
}

// printable is the layout as the printer should draw it, without the parts that
// only exist to make the preview readable.
func (s labelSpec) printable() labelSpec {
	items := make([]labelItem, 0, len(s.Items))

	for _, item := range s.Items {
		if !item.previewOnly {
			items = append(items, item)
		}
	}

	s.Items = items

	return s
}

const (
	itemText   = "text"
	itemQRCode = "qrcode"
	itemLine   = "line"
)

// Environment overrides. These belong to this service rather than to Homebox's
// own labelmaker settings, which describe a pixel canvas for a desktop printer
// and say nothing about the physical label stock a Bluetooth printer is loaded
// with.
const (
	// EnvProfile selects the default label stock: "standard" or "cable".
	EnvProfile = "HBOX_LOCAL_SVC_LABEL_PROFILE"

	// EnvSize overrides the label size of the selected profile, as "WxH" in
	// millimetres, e.g. "50x30".
	EnvSize = "HBOX_LOCAL_SVC_LABEL_SIZE"

	// EnvDPI sets the resolution the PNG preview is rasterised at. It has no
	// effect on Bluetooth output, which is drawn from the millimetre layout.
	EnvDPI = "HBOX_LOCAL_SVC_DPI"

	defaultDPI = 203.0
	maxDPI     = 1200.0
	mmPerInch  = 25.4
)

// profile describes a physical label and how much text fits on it. widthMM and
// heightMM are always the label as it comes off the roll; rotation says how the
// content sits on it.
type profile struct {
	name string

	widthMM   float64
	heightMM  float64
	paddingMM float64

	// rotation turns the content relative to the label. 90 lets a tall, narrow
	// label be laid out — and read — lengthwise.
	rotation int

	// qrMM is the QR code edge length. Zero fits it to the label height.
	qrMM float64

	titleMM float64
	bodyMM  float64

	// footerMM is the size used for the bottom strip (location path). Zero means
	// use bodyMM. Location stock sets this a step below the title so a long path
	// can wrap onto a second line without dominating the label.
	footerMM float64

	// flag lays the label out as a cable flag: the printed content is repeated
	// on both halves so it stays readable once the tail is folded back on
	// itself, with the fold marked in the middle.
	flag bool
}

const (
	profileStandard = "standard"
	profileCable    = "cable"
	profileLocation = "location"

	defaultProfileName = profileStandard
)

var profiles = map[string]profile{
	// A 25x15mm thermal label. There is not much room on one: the title gets a
	// line and the description whatever is left under it, so the type sizes are
	// smaller than a bigger label would want.
	profileStandard: {
		name:      profileStandard,
		widthMM:   25,
		heightMM:  15,
		paddingMM: 1,
		titleMM:   2.8,
		bodyMM:    2,
	},
	// A 60x40mm label for a location. Bigger than an item's, because a location
	// label is read from across the room and carries the path down to it as well
	// as its own description. The path sits a step below the title size so a long
	// chain can wrap onto a second line in the space under the QR code.
	profileLocation: {
		name:      profileLocation,
		widthMM:   60,
		heightMM:  40,
		paddingMM: 2,
		titleMM:   5,
		bodyMM:    3,
		footerMM:  4,
	},
	// A cable flag: 25x38mm, folded in half across its width into two 12.5x38mm
	// faces, so the same content is printed twice and stays readable from either
	// side. Printed rotated, which turns each face into a 38x12.5mm strip — wide
	// enough for the QR code and the text to sit side by side.
	profileCable: {
		name:      profileCable,
		widthMM:   25,
		heightMM:  38,
		paddingMM: 1,
		rotation:  90,
		titleMM:   3,
		bodyMM:    2.2,
		flag:      true,
	},
}

// resolveProfile picks the label stock for a request. The query parameter comes
// first so a caller can ask for a specific stock, then the environment default,
// then the built-in standard label.
func resolveProfile(requested string, size string) profile {
	name := firstNonEmptyString(requested, envString(EnvProfile), defaultProfileName)

	selected, ok := profiles[strings.ToLower(name)]
	if !ok {
		log.Warn().Str("profile", name).Msg("Unknown label profile, falling back to " + defaultProfileName)
		selected = profiles[defaultProfileName]
	}

	if width, height, ok := parseSize(firstNonEmptyString(size, envString(EnvSize))); ok {
		selected.widthMM = width
		selected.heightMM = height
		selected.qrMM = 0
	}

	return selected
}

// footerSize is the millimetre height used for the bottom path strip.
func footerSize(prof profile) float64 {
	if prof.footerMM > 0 {
		return prof.footerMM
	}
	return prof.bodyMM
}

// parseSize reads a "WxH" millimetre size. Anything unparseable is ignored so a
// typo degrades to the profile default instead of failing every label.
func parseSize(raw string) (width float64, height float64, ok bool) {
	if raw == "" {
		return 0, 0, false
	}

	widthPart, heightPart, found := strings.Cut(strings.ToLower(raw), "x")
	if !found {
		log.Warn().Str("size", raw).Msg("Ignoring label size, expected the form 50x30")
		return 0, 0, false
	}

	width, errWidth := strconv.ParseFloat(strings.TrimSpace(widthPart), 64)
	height, errHeight := strconv.ParseFloat(strings.TrimSpace(heightPart), 64)
	if errWidth != nil || errHeight != nil || width <= 0 || height <= 0 {
		log.Warn().Str("size", raw).Msg("Ignoring invalid label size")
		return 0, 0, false
	}

	return width, height, true
}

// previewDPI is the resolution the PNG preview is rendered at.
func previewDPI() float64 {
	raw := envString(EnvDPI)
	if raw == "" {
		return defaultDPI
	}

	dpi, err := strconv.ParseFloat(raw, 64)
	if err != nil || dpi <= 0 || dpi > maxDPI {
		log.Warn().Str(EnvDPI, raw).Msg("Ignoring invalid preview DPI")
		return defaultDPI
	}

	return dpi
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
