package localsvc

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// handleLabel answers the requests Homebox's labelmaker sends to a label
// service. It returns a PNG preview with the millimetre layout embedded, so the
// same response serves the on-screen dialog and the Bluetooth printer.
//
// The label is built from the record the QR code points at, not from the text in
// the request. That text is assembled for a sheet of paper — fields joined with
// newlines, an English "Location: " in front of the parent — and none of it fits
// a 25mm label. Reading the fields from the database instead means the label can
// give each one its own line and spend no space on labelling them.
//
// The passed text is the fallback for when the record cannot be read: no database
// bound, or a URL that does not point at one.
//
// The Width/Height/Dpi parameters are ignored either way. They describe a pixel
// canvas sized for a sheet printer and say nothing about the label stock in a
// Bluetooth printer, so the physical size comes from the profile.
func handleLabel(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	labelURL := query.Get("URL")

	// One lookup: it supplies the content and decides the label stock.
	record := lookupEntity(r.Context(), labelURL)

	request := labelRequest{
		title:       firstNonEmptyString(record.name, query.Get("TitleText")),
		description: firstNonEmptyString(record.location, query.Get("DescriptionText")),
		assetID:     record.assetID.String(),
		url:         labelURL,
	}

	// Operator-configured extra text is not part of any record, so it can only
	// come from the request.
	if additional := query.Get("AdditionalInformation"); additional != "" {
		request.description = strings.TrimSpace(request.description + " " + additional)
	}

	// An explicit request wins; otherwise the entity's type decides, so a cable
	// gets a flag label and everything else the default stock.
	requested := query.Get("LabelProfile")
	if requested == "" {
		requested = profileForTypeName(record.typeName)
	}

	image, err := renderLabel(request, resolveProfile(requested, query.Get("LabelSize")))
	if err != nil {
		log.Error().Err(err).Msg("Can not render label")
		http.Error(w, "can not render label", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")

	if _, err := w.Write(image); err != nil {
		log.Warn().Err(err).Msg("Can not write label response")
	}
}

// renderLabel produces the PNG together with its embedded layout.
func renderLabel(request labelRequest, prof profile) ([]byte, error) {
	spec, err := buildSpec(request, prof)
	if err != nil {
		return nil, err
	}

	preview, err := renderPreview(spec, previewDPI())
	if err != nil {
		return nil, err
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, preview); err != nil {
		return nil, err
	}

	layout, err := json.Marshal(spec.printable())
	if err != nil {
		return nil, err
	}

	return embedText(encoded.Bytes(), specKeyword, string(layout))
}
