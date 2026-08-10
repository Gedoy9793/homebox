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

	request := labelRequest{url: labelURL}

	if record.name != "" {
		request.title = record.name
		request.description = recordDescription(record)
		request.assetID = record.assetID.String()
	} else {
		// Fallback: the text labelmaker assembled. Its newlines are flattened
		// because they join fields that a label reads better side by side.
		request.title = query.Get("TitleText")
		request.description = strings.ReplaceAll(query.Get("DescriptionText"), "\n", " ")
	}

	// Operator-configured extra text is not part of any record, so it can only
	// come from the request.
	if additional := query.Get("AdditionalInformation"); additional != "" {
		request.description = strings.TrimSpace(request.description + " " + additional)
	}

	// An explicit request wins; otherwise the record decides, so a cable gets a
	// flag, a location the large stock, and everything else the default.
	requested := query.Get("LabelProfile")
	if requested == "" {
		requested = profileForRecord(record)
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

// recordDescription is what goes under the headline.
//
// A location gets the path down to it and then its own description: on its own,
// "Shelf 2" does not say which cupboard. Anything else gets the location it sits
// in, which is the one thing you want a label to tell you when the thing is not
// where you expected.
func recordDescription(record entityRecord) string {
	if !record.isLocation {
		return record.location
	}

	var parts []string

	if path := strings.Join(record.path, " / "); path != "" {
		parts = append(parts, path)
	}
	if record.description != "" {
		parts = append(parts, record.description)
	}

	return strings.Join(parts, "\n")
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
