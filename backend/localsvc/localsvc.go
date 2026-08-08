// Package localsvc runs the label and barcode services that ship with this fork
// inside the Homebox process, on a loopback-only HTTP listener.
//
// They are HTTP endpoints rather than direct function calls on purpose. Homebox
// already knows how to talk to an external label service and to Open Facts
// style barcode sources, so exposing these two as such lets them be wired in
// through configuration instead of code, and keeps this fork's changes to
// upstream files down to a handful of lines. See localsvc.Install for where that
// wiring happens.
//
// The listener binds 127.0.0.1 on a port picked by the kernel, so it is never
// reachable from outside the container and cannot collide with anything.
package localsvc

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// EnvDisabled switches the bundled services off, which restores plain
	// upstream behaviour without needing a different build.
	EnvDisabled = "HBOX_LOCAL_SVC_DISABLED"

	// LabelPath serves label images. BarcodePath mimics the Open Facts product
	// API so upstream's existing client can read it, and BarcodeImagePath serves
	// the cached product photos those replies point at.
	LabelPath        = "/label"
	BarcodePath      = "/api/v2/product/"
	BarcodeImagePath = "/barcode-image/"

	readHeaderTimeout = 5 * time.Second
)

// ErrDisabled is returned by Start when EnvDisabled is set.
var ErrDisabled = errors.New("localsvc: disabled by " + EnvDisabled)

var (
	startOnce sync.Once
	baseURL   string
	startErr  error
)

// Start brings the loopback server up and returns its base URL, e.g.
// "http://127.0.0.1:39481". It is idempotent, so callers that only need the
// address can call it again instead of passing the string around.
func Start() (string, error) {
	startOnce.Do(func() {
		baseURL, startErr = listenAndServe()
	})

	return baseURL, startErr
}

func listenAndServe() (string, error) {
	if envEnabled(EnvDisabled) {
		return "", ErrDisabled
	}

	// Port 0 lets the kernel pick a free port, which avoids both collisions with
	// whatever else runs in the container and a config knob nobody wants to set.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("localsvc: listen: %w", err)
	}

	server := &http.Server{
		Handler:           newMux(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() {
		// The listener lives as long as the process, so a returning Serve always
		// means something went wrong.
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("bundled label/barcode service stopped")
		}
	}()

	address := "http://" + listener.Addr().String()
	log.Info().Str("address", address).Msg("Started bundled label/barcode service")

	return address, nil
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+LabelPath, handleLabel)
	mux.HandleFunc("GET "+BarcodePath+"{file}", handleBarcodeLookup)
	mux.HandleFunc("GET "+BarcodeImagePath+"{name}", handleBarcodeImage)

	return mux
}

// envEnabled reports whether an on/off environment variable is set to a truthy
// value.
func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envString(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
