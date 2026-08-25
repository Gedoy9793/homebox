package localsvc

import (
	"encoding/json"
	"math"
	"os"
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

	// GapType and GapLength describe the physical stock, not content spacing.
	// Presence is kept separately because zero is meaningful for continuous
	// stock, while a profile may configure only one of these two values.
	GapType             int     `json:"gapType,omitempty"`
	GapLength           float64 `json:"gapLength,omitempty"`
	gapTypeConfigured   bool
	gapLengthConfigured bool

	// Rotation turns the whole layout when it is printed, so a tall label can be
	// drawn — and read — the long way round. Width and Height describe the canvas
	// the items are positioned on, which is the label turned by this angle.
	Rotation int `json:"rotation,omitempty"`

	Items []labelItem `json:"items"`
}

// MarshalJSON preserves an explicitly configured zero gap type/length while
// keeping either field independently optional.
func (s labelSpec) MarshalJSON() ([]byte, error) {
	type wire struct {
		Width     float64     `json:"width"`
		Height    float64     `json:"height"`
		GapType   *int        `json:"gapType,omitempty"`
		GapLength *float64    `json:"gapLength,omitempty"`
		Rotation  int         `json:"rotation,omitempty"`
		Items     []labelItem `json:"items"`
	}

	var gapType *int
	var gapLength *float64
	if s.gapTypeConfigured || s.GapType != 0 {
		gapType = &s.GapType
	}
	if s.gapLengthConfigured || s.GapLength != 0 {
		gapLength = &s.GapLength
	}

	return json.Marshal(wire{
		Width:     s.Width,
		Height:    s.Height,
		GapType:   gapType,
		GapLength: gapLength,
		Rotation:  s.Rotation,
		Items:     s.Items,
	})
}

// UnmarshalJSON records whether the optional stock fields were present, so a
// decode/encode round trip does not turn an explicit continuous setting back
// into an omitted setting.
func (s *labelSpec) UnmarshalJSON(data []byte) error {
	type wire struct {
		Width     float64     `json:"width"`
		Height    float64     `json:"height"`
		GapType   *int        `json:"gapType"`
		GapLength *float64    `json:"gapLength"`
		Rotation  int         `json:"rotation"`
		Items     []labelItem `json:"items"`
	}

	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	s.Width = decoded.Width
	s.Height = decoded.Height
	s.Rotation = decoded.Rotation
	s.Items = decoded.Items
	s.gapTypeConfigured = decoded.GapType != nil
	s.gapLengthConfigured = decoded.GapLength != nil
	if decoded.GapType != nil {
		s.GapType = *decoded.GapType
	} else {
		s.GapType = 0
	}
	if decoded.GapLength != nil {
		s.GapLength = *decoded.GapLength
	} else {
		s.GapLength = 0
	}

	return nil
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

	// EnvGapType overrides the printer's paper-detection mode. Values are the
	// lpapi-ble modes: 0 continuous, 1 positioning-hole, 2 die-cut gap, 3 black
	// mark, 4 transparent black mark, and 255 printer default.
	EnvGapType = "HBOX_LOCAL_SVC_LABEL_GAP_TYPE"

	// EnvGapLength overrides the physical gap between labels, in millimetres.
	// It is sent to the browser layout and then to lpapi-ble at 0.01mm
	// precision.
	EnvGapLength = "HBOX_LOCAL_SVC_LABEL_GAP_MM"

	// EnvGapLengthLegacy is kept for deployments that used the original, less
	// explicit name before the setting was documented.
	EnvGapLengthLegacy = "HBOX_LOCAL_SVC_LABEL_GAP_LENGTH"

	// EnvGapMM is an expressive alias for callers that prefer the unit in the
	// constant name.
	EnvGapMM = EnvGapLength

	defaultDPI = 203.0
	maxDPI     = 1200.0
	mmPerInch  = 25.4

	gapTypeContinuous           = 0
	gapTypeHole                 = 1
	gapTypeDieCut               = 2
	gapTypeBlackMark            = 3
	gapTypeTransparentBlackMark = 4
	gapTypePrinterDefault       = 255
	maxGapLengthMM              = 163.83 // lpapi-ble's 14-bit 0.01mm limit
)

// profile describes a physical label and how much text fits on it. widthMM and
// heightMM are always the label as it comes off the roll; rotation says how the
// content sits on it.
type profile struct {
	name string

	widthMM   float64
	heightMM  float64
	paddingMM float64

	// gapType and stockGapMM describe how the printer finds the next label on
	// this stock. They are separate from gapMM, which only spaces the content.
	gapType    int
	stockGapMM float64
	// The presence flags distinguish an explicit zero from an omitted setting.
	gapTypeConfigured   bool
	gapLengthConfigured bool

	// gapMM separates the QR code from the text column and from the footer path.
	// Zero falls back to the shared default used by the small item and cable stock.
	gapMM float64

	// rotation turns the content relative to the label. 90 lets a tall, narrow
	// label be laid out — and read — lengthwise.
	rotation int

	// qrMM is the QR code edge length. Zero fits it to the label height.
	qrMM float64

	// qrShare caps how much of the label width the QR code may take. Zero falls
	// back to the shared default used by item and cable stock.
	qrShare float64

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

	gapStockType       = 2
	standardStockGapMM = 6
)

var profiles = map[string]profile{
	// A 25x15mm thermal label. There is not much room on one: the title gets a
	// line and the description whatever is left under it, so the type sizes are
	// smaller than a bigger label would want.
	profileStandard: {
		name:                profileStandard,
		widthMM:             25,
		heightMM:            15,
		paddingMM:           1,
		gapType:             gapStockType,
		stockGapMM:          standardStockGapMM,
		gapTypeConfigured:   true,
		gapLengthConfigured: true,
		titleMM:             2.8,
		bodyMM:              2,
	},
	// A 25x15mm location label on the same stock as item labels. Split left/right:
	// QR above the asset ID on the left, name above the full path on the right.
	// Description and tags are left off so the path can stay readable.
	profileLocation: {
		name:                profileLocation,
		widthMM:             25,
		heightMM:            15,
		paddingMM:           1,
		gapType:             gapStockType,
		stockGapMM:          standardStockGapMM,
		gapTypeConfigured:   true,
		gapLengthConfigured: true,
		gapMM:               1,
		qrShare:             0.42,
		titleMM:             2.8,
		bodyMM:              1.8,
		footerMM:            1.7,
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

	selected = applyStockOverrides(selected)

	return selected
}

// applyStockOverrides applies the physical-stock settings after the profile
// and size have been selected. Environment values are deliberately parsed here
// instead of when the package is loaded, so tests and long-running processes
// see the same behaviour as the other local-service settings.
func applyStockOverrides(selected profile) profile {
	typeRaw, typeSet := lookupEnvValue(EnvGapType)
	lengthRaw, lengthEnv := lookupFirstEnvValue(EnvGapLength, EnvGapLengthLegacy)
	lengthSet := lengthEnv != ""

	validType := false
	gapType := selected.gapType
	if typeSet {
		parsed, err := strconv.Atoi(typeRaw)
		if err != nil || !validGapType(parsed) {
			log.Warn().Str(EnvGapType, typeRaw).Msg("Ignoring invalid label gap type")
		} else {
			gapType = parsed
			validType = true
		}
	}

	validLength := false
	var gapLength float64
	if lengthSet {
		parsed, err := strconv.ParseFloat(lengthRaw, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > maxGapLengthMM {
			log.Warn().Str("variable", lengthEnv).Str("value", lengthRaw).Msg("Ignoring invalid label gap length")
		} else {
			gapLength = parsed
			validLength = true
		}
	}

	if validType {
		selected.gapType = gapType
		selected.gapTypeConfigured = true
	}
	if validLength {
		selected.stockGapMM = gapLength
		selected.gapLengthConfigured = true
	} else if validType && (gapType == gapTypeContinuous || gapType == gapTypePrinterDefault) {
		// A profile's 6mm default is meaningful only for die-cut gap stock. If
		// the operator explicitly asks the printer to use continuous paper or
		// its own stored setting, omit the stale profile gap length.
		selected.stockGapMM = 0
		selected.gapLengthConfigured = false
	}

	return selected
}

// lookupFirstEnvValue returns the first non-empty variable and its name. This
// lets the preferred spelling win while keeping the original deployment knob
// working during upgrades.
func lookupFirstEnvValue(names ...string) (string, string) {
	for _, name := range names {
		if value, ok := lookupEnvValue(name); ok {
			return value, name
		}
	}

	return "", ""
}

func lookupEnvValue(name string) (string, bool) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return "", false
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	return raw, true
}

func validGapType(value int) bool {
	switch value {
	case gapTypeContinuous, gapTypeHole, gapTypeDieCut, gapTypeBlackMark, gapTypeTransparentBlackMark, gapTypePrinterDefault:
		return true
	default:
		return false
	}
}

// footerSize is the millimetre height used for the bottom path strip.
func footerSize(prof profile) float64 {
	if prof.footerMM > 0 {
		return prof.footerMM
	}
	return prof.bodyMM
}

// contentGap is the space between the QR code and neighbouring text blocks.
func contentGap(prof profile) float64 {
	if prof.gapMM > 0 {
		return prof.gapMM
	}
	return gapMM
}

// qrWidthCap is the fraction of label width the QR code may occupy.
func qrWidthCap(prof profile) float64 {
	if prof.qrShare > 0 {
		return prof.qrShare
	}
	return qrWidthShare
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
