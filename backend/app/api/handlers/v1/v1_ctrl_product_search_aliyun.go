package v1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// Barcode lookups against the Chinese product database published on Alibaba
// Cloud Marketplace (jmjksptmcx). Unlike the providers in
// v1_ctrl_product_search.go this API is a POST with a form encoded body and an
// APPCODE authorization header.
//
// Because it is billed per call, results are cached in a local JSON file. The
// provider also bills the "no data" reply (business code 201), so negative
// results are cached too — otherwise repeatedly scanning an unknown barcode
// keeps costing money. Cache entries are keyed by barcode inside one file, which
// avoids deriving file names from request input; the volume involved (a home
// inventory) is far too small for the read-modify-write cost to matter next to a
// network call.
//
// Product images are stored as separate files next to that JSON, because the
// image URLs the provider returns expire after ten days — by which time the
// cached entry would otherwise be useless for anything but text. Keeping the
// bytes out of the JSON also stops the file that is parsed on every lookup from
// growing into the hundreds of megabytes.
//
// Configuration is read from the environment rather than from the config
// package, so that upstream merges only touch the single aliyunBarcodeProducts
// call in HandleProductSearchFromBarcode. Without an AppCode set the lookup is
// skipped and Homebox behaves exactly as upstream.

const (
	aliyunBarcodeAppCodeEnv = "HBOX_BARCODE_ALIYUN_APPCODE"

	aliyunCachePathEnv     = "HBOX_BARCODE_ALIYUN_CACHE_PATH"
	aliyunCacheTTLEnv      = "HBOX_BARCODE_ALIYUN_CACHE_TTL"
	aliyunCacheDisabledEnv = "HBOX_BARCODE_ALIYUN_CACHE_DISABLED"

	// aliyunBarcodeSourceName is shown in the "source" column of the import dialog.
	aliyunBarcodeSourceName = "market.alicloudapi.com"

	// aliyunBarcodeMaxBody caps the response size read from the API.
	aliyunBarcodeMaxBody = 1 << 20

	// Documented business codes. Anything else is treated as a failure.
	aliyunBarcodeCodeOK     = "200"
	aliyunBarcodeCodeNoData = "201"

	aliyunCacheFileName     = "barcode-cache-aliyun.json"
	aliyunCacheImageDirName = "barcode-cache-images"
	aliyunCacheFileMode     = 0o600
	aliyunCacheDirMode      = 0o750

	mimeJPEG = "image/jpeg"
	mimePNG  = "image/png"
)

// aliyunBarcodeEndpoint is a variable so tests can point it at a local server.
var aliyunBarcodeEndpoint = "https://jmjksptmcx.market.alicloudapi.com/bar-code/import/query"

// aliyunCacheMu serializes the read-modify-write cycle against the cache file.
var aliyunCacheMu sync.Mutex

// aliyunBarcodeResponse is the API envelope. code arrives as a JSON number but
// is decoded through flexibleString so a string form is tolerated too.
type aliyunBarcodeResponse struct {
	Code flexibleString    `json:"code"`
	Msg  string            `json:"msg"`
	Data aliyunBarcodeData `json:"data"`
}

type aliyunBarcodeData struct {
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

type aliyunBarcodeCacheEntry struct {
	CachedAt time.Time `json:"cachedAt"`

	// Products is empty for a barcode the API has no data for. The presence of
	// the entry is what marks a cache hit, so an empty list still prevents a
	// billed call.
	Products []repo.BarcodeProduct `json:"products"`

	// ImageFile names the cached image inside the image directory. The API
	// returns at most one product per barcode, so a single file per entry is
	// enough. Empty when there was no image or it could not be downloaded.
	ImageFile string `json:"imageFile,omitempty"`
}

type aliyunBarcodeCache struct {
	Entries map[string]aliyunBarcodeCacheEntry `json:"entries"`
}

// truncateRunes shortens s to at most limit runes, matching the validation
// limits on EntityCreate so an overlong field cannot block the import.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// buildAliyunBarcodeProduct maps the API payload onto a BarcodeProduct. The
// manufacturer falls back to the trademark because manuName is frequently empty
// for imported goods.
func buildAliyunBarcodeProduct(iEan string, data aliyunBarcodeData) (repo.BarcodeProduct, bool) {
	name := strings.TrimSpace(data.GoodsName)
	if name == "" {
		return repo.BarcodeProduct{}, false
	}

	var p repo.BarcodeProduct
	p.SearchEngineName = aliyunBarcodeSourceName
	p.Barcode = firstNonEmpty(data.Code, iEan)
	p.Item.Name = truncateRunes(name, 255)
	p.Manufacturer = truncateRunes(firstNonEmpty(data.ManuName, data.Trademark), 255)

	// The API has no model number, so the remaining descriptive fields are
	// collected into the description instead of being dropped.
	var parts []string
	for _, value := range []string{
		data.Spec,
		firstNonEmpty(data.GoodsType, data.GpcType),
		data.Description,
		data.Ycg,
		data.ManuAddress,
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	p.Item.Description = truncateRunes(strings.Join(parts, " | "), 1000)

	// Only HTTPS images survive the base64 fetch in the handler.
	if imageURL := strings.TrimSpace(data.Img); strings.HasPrefix(imageURL, schemeHTTPS+"://") {
		p.ImageURL = imageURL
	}

	return p, true
}

func lookupAliyunBarcode(appCode string, iEan string) ([]repo.BarcodeProduct, error) {
	if appCode == "" {
		return nil, errors.New("no app code configured for market.alicloudapi.com. " +
			"Please define the app code in environment variable " + aliyunBarcodeAppCodeEnv)
	}

	form := url.Values{"code": {iEan}}

	req, err := http.NewRequest(http.MethodPost, aliyunBarcodeEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "APPCODE "+sanitizeHeader(appCode))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: barcodeHTTPTimeoutSec * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() {
		err = errors.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode != http.StatusOK {
		// The gateway reports quota and credential problems in this header.
		if reason := resp.Header.Get("X-Ca-Error-Message"); reason != "" {
			return nil, fmt.Errorf("aliyun API returned status code %d: %s", resp.StatusCode, sanitizeHeader(reason))
		}
		return nil, fmt.Errorf("aliyun API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, aliyunBarcodeMaxBody))
	if err != nil {
		return nil, err
	}

	var result aliyunBarcodeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("can not unmarshal JSON from market.alicloudapi.com: %w", err)
	}

	switch string(result.Code) {
	case aliyunBarcodeCodeOK:
	case aliyunBarcodeCodeNoData:
		// Unknown barcode. Billed by the provider, but not an error here.
		return nil, nil
	default:
		return nil, fmt.Errorf("aliyun API returned code %s: %s", result.Code, result.Msg)
	}

	p, ok := buildAliyunBarcodeProduct(iEan, result.Data)
	if !ok {
		return nil, nil
	}

	return []repo.BarcodeProduct{p}, nil
}

func aliyunCacheDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(aliyunCacheDisabledEnv)), "true")
}

func aliyunCachePath() string {
	if path := strings.TrimSpace(os.Getenv(aliyunCachePathEnv)); path != "" {
		return path
	}
	return aliyunCacheDefaultPath()
}

// aliyunCacheDefaultPath picks a location that survives a restart. The Docker
// image persists /data as a volume while its working directory is /app, so a
// relative default would be discarded whenever the container is recreated —
// which for a paid API means paying twice for the same barcode.
func aliyunCacheDefaultPath() string {
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		return filepath.Join("/data", aliyunCacheFileName)
	}
	return filepath.Join(".data", aliyunCacheFileName)
}

// aliyunCacheTTL returns the configured entry lifetime. Zero means entries never
// expire, which suits product data that does not change.
func aliyunCacheTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(aliyunCacheTTLEnv))
	if raw == "" {
		return 0
	}

	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl < 0 {
		log.Warn().Msg("Ignoring invalid " + aliyunCacheTTLEnv + " value: " + raw)
		return 0
	}

	return ttl
}

// readAliyunCache loads the cache file. A missing file is not an error, and a
// corrupt one is reported and treated as empty so it gets rebuilt rather than
// blocking lookups.
func readAliyunCache(path string) aliyunBarcodeCache {
	cache := aliyunBarcodeCache{Entries: make(map[string]aliyunBarcodeCacheEntry)}

	body, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Msg("Can not read barcode cache " + path + ": " + err.Error())
		}
		return cache
	}

	var stored aliyunBarcodeCache
	if err := json.Unmarshal(body, &stored); err != nil {
		log.Warn().Msg("Ignoring corrupt barcode cache " + path + ": " + err.Error())
		return cache
	}

	if stored.Entries != nil {
		cache.Entries = stored.Entries
	}

	return cache
}

// writeAliyunCache writes the cache through a temporary file so an interrupted
// write cannot leave a truncated cache behind.
func writeAliyunCache(path string, cache aliyunBarcodeCache) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, aliyunCacheDirMode); err != nil {
		return err
	}

	body, err := json.Marshal(cache)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".barcode-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(aliyunCacheFileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	return nil
}

func aliyunCacheImageDir(cachePath string) string {
	return filepath.Join(filepath.Dir(cachePath), aliyunCacheImageDirName)
}

// aliyunCacheImageName derives the image file name from the barcode. The barcode
// arrives in a request parameter, so it is hashed instead of being used as a
// path element.
func aliyunCacheImageName(iEan string, mime string) string {
	sum := sha256.Sum256([]byte(iEan))

	ext := ".jpg"
	if mime == mimePNG {
		ext = ".png"
	}

	return hex.EncodeToString(sum[:]) + ext
}

// splitDataURI takes apart the "data:image/png;base64,…" form produced by
// fetchImageBase64 so the image can be stored as an ordinary file.
func splitDataURI(uri string) (mime string, raw []byte, err error) {
	meta, payload, found := strings.Cut(uri, ",")
	if !found || !strings.HasPrefix(meta, "data:") || !strings.HasSuffix(meta, ";base64") {
		return "", nil, errors.New("unsupported data URI")
	}

	mime = strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64")

	raw, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, err
	}

	return mime, raw, nil
}

// writeAliyunCacheImage stores an already downloaded image and returns the file
// name to record in the cache entry, or an empty string on failure. A missing
// image only costs a picture, so problems are logged and swallowed.
func writeAliyunCacheImage(cachePath string, iEan string, dataURI string) string {
	mime, raw, err := splitDataURI(dataURI)
	if err != nil {
		log.Warn().Msg("Can not decode image for barcode " + iEan + ": " + err.Error())
		return ""
	}

	dir := aliyunCacheImageDir(cachePath)
	if err := os.MkdirAll(dir, aliyunCacheDirMode); err != nil {
		log.Warn().Msg("Can not create image cache " + dir + ": " + err.Error())
		return ""
	}

	name := aliyunCacheImageName(iEan, mime)
	if err := os.WriteFile(filepath.Join(dir, name), raw, aliyunCacheFileMode); err != nil {
		log.Warn().Msg("Can not write cached image " + name + ": " + err.Error())
		return ""
	}

	return name
}

// loadAliyunCacheImage reads a cached image back into the data URI form the
// frontend expects.
func loadAliyunCacheImage(cachePath string, name string) string {
	// Names are generated by aliyunCacheImageName, but the cache file is on disk
	// and could have been edited, so reject anything that is not a bare name.
	if name == "" || name != filepath.Base(name) {
		return ""
	}

	raw, err := os.ReadFile(filepath.Join(aliyunCacheImageDir(cachePath), name))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Msg("Can not read cached image " + name + ": " + err.Error())
		}
		return ""
	}

	mime := mimeJPEG
	if strings.HasSuffix(name, ".png") {
		mime = mimePNG
	}

	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// aliyunCacheLookup reports whether the barcode has already been queried. The
// second return value distinguishes a cached "no data" result from a miss.
func aliyunCacheLookup(iEan string) ([]repo.BarcodeProduct, bool) {
	if aliyunCacheDisabled() {
		return nil, false
	}

	aliyunCacheMu.Lock()
	defer aliyunCacheMu.Unlock()

	path := aliyunCachePath()

	entry, ok := readAliyunCache(path).Entries[iEan]
	if !ok {
		return nil, false
	}

	if ttl := aliyunCacheTTL(); ttl > 0 && time.Since(entry.CachedAt) > ttl {
		return nil, false
	}

	// The handler re-downloads the image whenever ImageURL is set and only keeps
	// this value if that fails, which is exactly what is wanted: a fresh image
	// while the ten day link lives, the cached one afterwards.
	if len(entry.Products) > 0 && entry.ImageFile != "" {
		if dataURI := loadAliyunCacheImage(path, entry.ImageFile); dataURI != "" {
			entry.Products[0].ImageBase64 = dataURI
		}
	}

	return entry.Products, true
}

// aliyunCacheStore records a lookup result together with its image. Cache
// problems are logged but never propagated: failing to cache is not a reason to
// fail the lookup.
func aliyunCacheStore(iEan string, products []repo.BarcodeProduct) {
	if aliyunCacheDisabled() {
		return
	}

	path := aliyunCachePath()

	// Downloaded outside the lock so a slow image cannot block other lookups.
	var imageFile string
	if len(products) > 0 && products[0].ImageURL != "" {
		dataURI, err := fetchImageBase64(products[0].ImageURL)
		if err != nil {
			log.Warn().Msg("Can not cache image for barcode " + iEan + ": " + err.Error())
		} else {
			imageFile = writeAliyunCacheImage(path, iEan, dataURI)
		}
	}

	aliyunCacheMu.Lock()
	defer aliyunCacheMu.Unlock()

	cache := readAliyunCache(path)

	// Drop a previously cached image whose extension no longer matches.
	if previous, ok := cache.Entries[iEan]; ok && previous.ImageFile != "" && previous.ImageFile != imageFile {
		if previous.ImageFile == filepath.Base(previous.ImageFile) {
			_ = os.Remove(filepath.Join(aliyunCacheImageDir(path), previous.ImageFile))
		}
	}

	cache.Entries[iEan] = aliyunBarcodeCacheEntry{
		CachedAt:  time.Now().UTC(),
		Products:  products,
		ImageFile: imageFile,
	}

	if err := writeAliyunCache(path, cache); err != nil {
		log.Warn().Msg("Can not write barcode cache " + path + ": " + err.Error())
		return
	}

	log.Debug().Msg("Cached barcode lookup for " + iEan + " in " + path)
}

// aliyunBarcodeProducts is the entry point used by the handler. It resolves the
// AppCode, serves known barcodes from the local cache, and swallows failures so
// a problem with this provider cannot break lookups against the others. That
// keeps the upstream call site a single line.
func aliyunBarcodeProducts(iEan string) []repo.BarcodeProduct {
	appCode := strings.TrimSpace(os.Getenv(aliyunBarcodeAppCodeEnv))
	if appCode == "" {
		return nil
	}

	if products, ok := aliyunCacheLookup(iEan); ok {
		log.Debug().Msg("Serving barcode " + iEan + " from the local cache")
		return products
	}

	products, err := lookupAliyunBarcode(appCode, iEan)
	if err != nil {
		// Errors are not cached: they may be transient, and a retry is free
		// unless the provider actually answered.
		log.Error().Msg("Can not retrieve product from market.alicloudapi.com: " + err.Error())
		return nil
	}

	aliyunCacheStore(iEan, products)

	return products
}
