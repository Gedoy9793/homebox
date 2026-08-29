package localsvc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Barcode lookups against the Chinese product database published on Alibaba
// Cloud Marketplace (jmjksptmcx). It is a POST with a form encoded body and an
// APPCODE authorization header, which is why it cannot be expressed as one more
// entry in upstream's list of sources directly.
//
// Instead this service re-publishes it in the shape of the Open Facts product
// API, which upstream already knows how to read, so it joins the other barcode
// sources through configuration rather than through a code change.
//
// Successful lookups are cached locally; "no data" replies are not. See
// barcode_cache.go.

const (
	// EnvAppCode holds the Alibaba Cloud Marketplace APPCODE. Without it the
	// lookup is skipped and this source simply reports "no data".
	EnvAppCode = "HBOX_BARCODE_ALIYUN_APPCODE"

	// BarcodeSourceName is what shows up in the "source" column of the import
	// dialog. It names the provider rather than this service, because that is
	// where the data comes from.
	BarcodeSourceName = "market.alicloudapi.com"

	// barcodeMaxBody caps the response size read from the API.
	barcodeMaxBody = 1 << 20

	barcodeTimeout = 10 * time.Second

	// Documented business codes. Anything else is treated as a failure.
	codeOK     = "200"
	codeNoData = "201"

	// Field limits on EntityCreate, so an overlong value cannot block an import.
	maxNameRunes        = 255
	maxDescriptionRunes = 1000
)

// barcodeEndpoint is a variable so tests can point it at a local server.
var barcodeEndpoint = "https://jmjksptmcx.market.alicloudapi.com/bar-code/import/query"

// flexibleString accepts a JSON string or number. The provider documents code
// as a number but has been seen returning it quoted.
type flexibleString string

func (f *flexibleString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*f = ""
		return nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		*f = flexibleString(text)

		return nil
	}

	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return err
	}
	*f = flexibleString(number.String())

	return nil
}

type barcodeResponse struct {
	Code flexibleString `json:"code"`
	Msg  string         `json:"msg"`
	Data barcodeData    `json:"data"`
}

type barcodeData struct {
	Code        string `json:"code"`
	Img         string `json:"img"`
	GoodsType   string `json:"goodsType"`
	Trademark   string `json:"trademark"`
	GoodsName   string `json:"goodsName"`
	Spec        string `json:"spec"`
	Ycg         string `json:"ycg"`
	ManuName    string `json:"manuName"`
	ManuAddress string `json:"manuAddress"`
	Description string `json:"description"`
	GpcType     string `json:"gpcType"`
	Keyword     string `json:"keyword"`
}

// barcodeProduct deliberately mirrors the JSON layout of repo.BarcodeProduct,
// which is the format the cache file was written in before this service existed.
// Keeping the field names means those entries stay cache hits, and every one of
// them was a paid lookup.
type barcodeProduct struct {
	Manufacturer string             `json:"manufacturer"`
	ImageURL     string             `json:"imageURL"`
	Item         barcodeProductItem `json:"item"`
}

type barcodeProductItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// buildProduct maps the API payload. The manufacturer falls back to the
// trademark because manuName is frequently empty for imported goods.
func buildProduct(data barcodeData) (barcodeProduct, bool) {
	name := strings.TrimSpace(data.GoodsName)
	if name == "" {
		return barcodeProduct{}, false
	}

	// The API has no model number, so the remaining descriptive fields are
	// collected into the description rather than dropped.
	var details []string
	for _, value := range []string{
		data.Spec,
		firstNonEmptyString(data.GoodsType, data.GpcType),
		data.Description,
		data.Ycg,
		data.ManuAddress,
	} {
		if value = strings.TrimSpace(value); value != "" {
			details = append(details, value)
		}
	}

	product := barcodeProduct{
		Manufacturer: truncateRunes(firstNonEmptyString(data.ManuName, data.Trademark), maxNameRunes),
		Item: barcodeProductItem{
			Name:        truncateRunes(name, maxNameRunes),
			Description: truncateRunes(strings.Join(details, " | "), maxDescriptionRunes),
		},
	}

	if image := strings.TrimSpace(data.Img); strings.HasPrefix(image, "https://") {
		product.ImageURL = image
	}

	return product, true
}

// lookupBarcode queries the provider. A nil product with a nil error means the
// provider answered that it has no data for this barcode.
func lookupBarcode(appCode string, barcode string) (*barcodeProduct, error) {
	form := url.Values{"code": {barcode}}

	request, err := http.NewRequest(http.MethodPost, barcodeEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	request.Header.Set("Authorization", "APPCODE "+sanitizeHeader(appCode))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: barcodeTimeout}

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, response.Body.Close())
	}()

	if response.StatusCode != http.StatusOK {
		// The gateway reports quota and credential problems in this header.
		if reason := response.Header.Get("X-Ca-Error-Message"); reason != "" {
			return nil, fmt.Errorf("aliyun API returned status %d: %s", response.StatusCode, sanitizeHeader(reason))
		}

		return nil, fmt.Errorf("aliyun API returned status code: %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, barcodeMaxBody))
	if err != nil {
		return nil, err
	}

	var result barcodeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("can not unmarshal JSON from %s: %w", BarcodeSourceName, err)
	}

	switch string(result.Code) {
	case codeOK:
	case codeNoData:
		// Unknown barcode. Billed by the provider, but not an error here.
		return nil, nil
	default:
		return nil, fmt.Errorf("aliyun API returned code %s: %s", result.Code, result.Msg)
	}

	product, ok := buildProduct(result.Data)
	if !ok {
		return nil, nil
	}

	return &product, nil
}

// truncateRunes shortens text to at most limit runes.
func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	return string(runes[:limit])
}

// sanitizeHeader removes control characters that could cause header injection.
func sanitizeHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}

		return r
	}, value)
}
