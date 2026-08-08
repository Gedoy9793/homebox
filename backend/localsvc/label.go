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
// Only the content parameters are used. The Width/Height/Dpi parameters describe
// a pixel canvas sized for a sheet printer and say nothing about the label stock
// in a Bluetooth printer, so the physical size comes from the profile instead.
func handleLabel(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	description := query.Get("DescriptionText")
	if additional := query.Get("AdditionalInformation"); additional != "" {
		description = strings.TrimPrefix(description+"\n"+additional, "\n")
	}

	request := labelRequest{
		title:       query.Get("TitleText"),
		description: description,
		url:         query.Get("URL"),
	}

	// An explicit request wins; otherwise the entity's type decides, so a cable
	// gets a flag label and everything else the default stock.
	requested := query.Get("LabelProfile")
	if requested == "" {
		requested = profileForLabelURL(r.Context(), request.url)
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
