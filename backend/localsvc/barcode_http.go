package localsvc

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// The reply shape of the Open Facts product API, which is what upstream's
// barcode client expects. Only the fields it reads are emitted.
type openFactsResponse struct {
	Code    string           `json:"code"`
	Status  int              `json:"status"`
	Product openFactsProduct `json:"product"`
}

type openFactsProduct struct {
	ProductName string `json:"product_name"`
	Brands      string `json:"brands"`
	GenericName string `json:"generic_name"`
	ImageURL    string `json:"image_url"`
}

const (
	// statusFound / statusMissing are the values upstream checks: a zero status
	// means the barcode is unknown.
	statusFound   = 1
	statusMissing = 0

	maxBarcodeLength = 80
)

// handleBarcodeLookup answers /api/v2/product/{barcode}.json.
func handleBarcodeLookup(w http.ResponseWriter, r *http.Request) {
	barcode := strings.TrimSuffix(r.PathValue("file"), ".json")
	if !validBarcode(barcode) {
		writeJSON(w, openFactsResponse{Status: statusMissing})
		return
	}

	entry, ok := resolveBarcode(barcode)
	if !ok || len(entry.Products) == 0 {
		writeJSON(w, openFactsResponse{Code: barcode, Status: statusMissing})
		return
	}

	product := entry.Products[0]

	reply := openFactsResponse{
		Code:   barcode,
		Status: statusFound,
		Product: openFactsProduct{
			ProductName: product.Item.Name,
			Brands:      product.Manufacturer,
			GenericName: product.Item.Description,
		},
	}

	// Point at the locally cached copy rather than the provider's link, which
	// expires after about ten days. The image is then served by
	// handleBarcodeImage over loopback.
	if entry.ImageFile != "" {
		reply.Product.ImageURL = "http://" + r.Host + BarcodeImagePath + entry.ImageFile
	}

	writeJSON(w, reply)
}

// handleBarcodeImage serves a cached product image.
func handleBarcodeImage(w http.ResponseWriter, r *http.Request) {
	mime, raw, err := readImage(cachePath(), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", mime)

	if _, err := w.Write(raw); err != nil {
		log.Warn().Err(err).Msg("Can not write barcode image response")
	}
}

// resolveBarcode serves a barcode from the cache or, failing that, from the
// provider. The boolean reports whether an answer is available at all: a lookup
// error is not cached, because it may be transient and a retry is free unless
// the provider actually answered.
func resolveBarcode(barcode string) (cacheEntry, bool) {
	appCode := envString(EnvAppCode)
	if appCode == "" {
		// Nothing configured, so this source has nothing to say. Not an error:
		// Homebox queries every source on every scan.
		return cacheEntry{}, false
	}

	if entry, hit := cacheLookup(barcode); hit {
		log.Debug().Str("barcode", barcode).Msg("Serving barcode from the local cache")
		return entry, true
	}

	product, err := lookupBarcode(appCode, barcode)
	if err != nil {
		log.Error().Err(err).Str("barcode", barcode).Msg("Can not retrieve product from " + BarcodeSourceName)
		return cacheEntry{}, false
	}

	if product == nil {
		// Billed by the provider, but not cached — the next scan should retry.
		return cacheEntry{}, false
	}

	return cacheStore(barcode, product), true
}

// validBarcode keeps anything that is not plausibly a barcode from reaching the
// paid API or the cache file.
func validBarcode(barcode string) bool {
	if barcode == "" || len(barcode) > maxBarcodeLength {
		return false
	}

	for _, r := range barcode {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '-':
		default:
			return false
		}
	}

	return true
}

func writeJSON(w http.ResponseWriter, reply openFactsResponse) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(reply); err != nil {
		log.Warn().Err(err).Msg("Can not write barcode lookup response")
	}
}
