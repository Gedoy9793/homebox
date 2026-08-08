package localsvc

import (
	"errors"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// EnvLabelServiceURL is Homebox's own setting for an external label service.
// When the operator has not set it, Install points it at the bundled service, so
// labels come out with a layout embedded for Bluetooth printing by default.
const EnvLabelServiceURL = "HBOX_LABEL_MAKER_LABEL_SERVICE_URL"

// labelServiceFlag is the command line form of the same setting.
const labelServiceFlag = "label-maker-label-service-url"

// Install starts the bundled services and hooks them into Homebox.
//
// It has to run before config.New parses the configuration, which is why the
// caller does this from an init function. Returned is the base URL to register
// as a barcode source; the caller does that part because the list of sources is
// private to the handler package.
func Install() (barcodeBaseURL string, ok bool) {
	base, err := Start()
	if err != nil {
		if !errors.Is(err, ErrDisabled) {
			log.Error().Err(err).Msg("Can not start the bundled label/barcode service")
		}

		return "", false
	}

	installLabelService(base)

	return base, true
}

// installLabelService makes the bundled service the default label service.
//
// Configuration is only ever read from the environment and the command line
// (config.New uses ardanlabs/conf, which has no file source), so setting the
// variable is enough — and an operator's own value always wins, whichever way
// they supplied it.
func installLabelService(base string) {
	if os.Getenv(EnvLabelServiceURL) != "" {
		log.Debug().Msg("Keeping the configured label service; not using the bundled one")
		return
	}

	if flagProvided(labelServiceFlag) {
		return
	}

	if err := os.Setenv(EnvLabelServiceURL, base+LabelPath); err != nil {
		log.Error().Err(err).Msg("Can not select the bundled label service")
		return
	}

	log.Info().Msg("Using the bundled label service; set " + EnvLabelServiceURL + " to override")
}

// flagProvided reports whether a conf flag was passed on the command line, in
// any of the forms ardanlabs/conf accepts.
func flagProvided(name string) bool {
	for _, arg := range os.Args[1:] {
		trimmed := strings.TrimLeft(arg, "-")
		if trimmed == name || strings.HasPrefix(trimmed, name+"=") {
			return true
		}
	}

	return false
}
