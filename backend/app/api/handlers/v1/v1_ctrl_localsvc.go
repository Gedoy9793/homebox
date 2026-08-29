package v1

import (
	"net"
	"net/url"

	"github.com/sysadminsmedia/homebox/backend/localsvc"
)

// Wires the bundled label and barcode services (see backend/localsvc) into
// Homebox.
//
// This is deliberately the only file involved: the label service is selected
// through the environment, which config.New reads later on in main, and the
// barcode service is prepended to the list of Open Facts style sources that
// HandleProductSearchFromBarcode already walks. Nothing has to be added to the
// handler itself, which keeps this fork's diff against upstream near zero.
//
//nolint:gochecknoinits // an init is what makes the wiring a pure addition
func init() {
	base, ok := localsvc.Install()
	if !ok {
		return
	}

	openFactsSources = append([]openFactsSource{{
		Name:    localsvc.BarcodeSourceName,
		BaseURL: base,
	}}, openFactsSources...)
}

// isLoopbackImageURL reports whether an image lives on the bundled service.
//
// Product images from the bundled barcode source are served from its own cache
// over loopback HTTP, because the provider's links expire after ten days. That
// makes them neither HTTPS nor one of the Open Facts image hosts, so the two
// checks guarding image fetches consult this before rejecting them.
func isLoopbackImageURL(u *url.URL) bool {
	if u == nil || u.User != nil {
		return false
	}

	if u.Scheme != "http" {
		return false
	}

	// A literal address only: resolving a name here would let a hostile reply
	// from another barcode source point this at the loopback interface and use
	// the image fetch to probe local ports.
	ip := net.ParseIP(u.Hostname())

	return ip != nil && ip.IsLoopback()
}
