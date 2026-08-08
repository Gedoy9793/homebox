package localsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// The provider bills per call, including the "no data" reply, so results are
// cached in a local JSON file — a negative result is cached too, otherwise
// repeatedly scanning an unknown barcode keeps costing money. Entries are keyed
// by barcode inside one file, which avoids deriving file names from request
// input; the volume involved (a home inventory) is far too small for the
// read-modify-write cost to matter next to a network call.
//
// Product images are stored as separate files next to that JSON, because the
// image URLs the provider returns expire after ten days — by which time the
// cached entry would otherwise be useless for anything but text. Keeping the
// bytes out of the JSON also stops the file that is parsed on every lookup from
// growing into the hundreds of megabytes.

const (
	EnvCachePath     = "HBOX_BARCODE_ALIYUN_CACHE_PATH"
	EnvCacheTTL      = "HBOX_BARCODE_ALIYUN_CACHE_TTL"
	EnvCacheDisabled = "HBOX_BARCODE_ALIYUN_CACHE_DISABLED"

	cacheFileName     = "barcode-cache-aliyun.json"
	cacheImageDirName = "barcode-cache-images"
	cacheFileMode     = 0o600
	cacheDirMode      = 0o750

	// imageMaxBytes caps what is downloaded from the provider's image host.
	imageMaxBytes = 8 << 20

	mimeJPEG = "image/jpeg"
	mimePNG  = "image/png"
)

// cacheMu serializes the read-modify-write cycle against the cache file.
var cacheMu sync.Mutex

type cacheEntry struct {
	CachedAt time.Time `json:"cachedAt"`

	// Products is empty for a barcode the API has no data for. The presence of
	// the entry is what marks a cache hit, so an empty list still prevents a
	// billed call.
	Products []barcodeProduct `json:"products"`

	// ImageFile names the cached image inside the image directory. The API
	// returns at most one product per barcode, so a single file per entry is
	// enough. Empty when there was no image or it could not be downloaded.
	ImageFile string `json:"imageFile,omitempty"`
}

type barcodeCache struct {
	Entries map[string]cacheEntry `json:"entries"`
}

func cacheDisabled() bool {
	return envEnabled(EnvCacheDisabled)
}

func cachePath() string {
	if path := envString(EnvCachePath); path != "" {
		return path
	}

	// The Docker image persists /data as a volume while its working directory is
	// /app, so a relative default would be discarded whenever the container is
	// recreated — which for a paid API means paying twice for the same barcode.
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		return filepath.Join("/data", cacheFileName)
	}

	return filepath.Join(".data", cacheFileName)
}

// cacheTTL returns the configured entry lifetime. Zero means entries never
// expire, which suits product data that does not change.
func cacheTTL() time.Duration {
	raw := envString(EnvCacheTTL)
	if raw == "" {
		return 0
	}

	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl < 0 {
		log.Warn().Str(EnvCacheTTL, raw).Msg("Ignoring invalid barcode cache TTL")
		return 0
	}

	return ttl
}

// readCache loads the cache file. A missing file is not an error, and a corrupt
// one is reported and treated as empty so it gets rebuilt rather than blocking
// lookups.
func readCache(path string) barcodeCache {
	cache := barcodeCache{Entries: make(map[string]cacheEntry)}

	body, err := os.ReadFile(path) //nolint:gosec // operator-configured cache location
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Err(err).Str("path", path).Msg("Can not read barcode cache")
		}

		return cache
	}

	var stored barcodeCache
	if err := json.Unmarshal(body, &stored); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("Ignoring corrupt barcode cache")
		return cache
	}

	if stored.Entries != nil {
		cache.Entries = stored.Entries
	}

	return cache
}

// writeCache writes through a temporary file so an interrupted write cannot
// leave a truncated cache behind.
func writeCache(path string, cache barcodeCache) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
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
	if err := tmp.Chmod(cacheFileMode); err != nil {
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

func cacheImageDir(path string) string {
	return filepath.Join(filepath.Dir(path), cacheImageDirName)
}

// cacheImageName derives the image file name from the barcode. The barcode
// arrives in a request parameter, so it is hashed rather than used as a path
// element.
func cacheImageName(barcode string, mime string) string {
	sum := sha256.Sum256([]byte(barcode))

	extension := ".jpg"
	if mime == mimePNG {
		extension = ".png"
	}

	return hex.EncodeToString(sum[:]) + extension
}

// downloadImage fetches the provider's image. Failure only costs a picture, so
// the caller logs and carries on.
func downloadImage(imageURL string) (mime string, raw []byte, err error) {
	client := &http.Client{Timeout: barcodeTimeout}

	response, err := client.Get(imageURL)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		err = errors.Join(err, response.Body.Close())
	}()

	if response.StatusCode != http.StatusOK {
		return "", nil, errors.New("image fetch returned status " + response.Status)
	}

	raw, err = io.ReadAll(io.LimitReader(response.Body, imageMaxBytes))
	if err != nil {
		return "", nil, err
	}

	switch detected := http.DetectContentType(raw); detected {
	case mimeJPEG, mimePNG:
		return detected, raw, nil
	default:
		return "", nil, errors.New("unsupported image type: " + detected)
	}
}

// storeImage downloads and stores a product image, returning the file name to
// record in the cache entry, or an empty string if anything went wrong.
func storeImage(path string, barcode string, imageURL string) string {
	mime, raw, err := downloadImage(imageURL)
	if err != nil {
		log.Warn().Err(err).Str("barcode", barcode).Msg("Can not cache product image")
		return ""
	}

	dir := cacheImageDir(path)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		log.Warn().Err(err).Str("dir", dir).Msg("Can not create image cache")
		return ""
	}

	name := cacheImageName(barcode, mime)
	if err := os.WriteFile(filepath.Join(dir, name), raw, cacheFileMode); err != nil {
		log.Warn().Err(err).Str("image", name).Msg("Can not write cached image")
		return ""
	}

	return name
}

// readImage loads a cached image by file name.
func readImage(path string, name string) (mime string, raw []byte, err error) {
	// Names are generated by cacheImageName, but the cache file is on disk and
	// could have been edited, so reject anything that is not a bare name.
	if name == "" || name != filepath.Base(name) {
		return "", nil, fs.ErrNotExist
	}

	raw, err = os.ReadFile(filepath.Join(cacheImageDir(path), name)) //nolint:gosec // name is validated above
	if err != nil {
		return "", nil, err
	}

	mime = mimeJPEG
	if strings.HasSuffix(name, ".png") {
		mime = mimePNG
	}

	return mime, raw, nil
}

// cacheLookup reports whether the barcode has already been queried. The second
// return value distinguishes a cached "no data" result from a miss.
func cacheLookup(barcode string) (entry cacheEntry, hit bool) {
	if cacheDisabled() {
		return cacheEntry{}, false
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()

	entry, ok := readCache(cachePath()).Entries[barcode]
	if !ok {
		return cacheEntry{}, false
	}

	if ttl := cacheTTL(); ttl > 0 && time.Since(entry.CachedAt) > ttl {
		return cacheEntry{}, false
	}

	return entry, true
}

// cacheStore records a lookup result together with its image. Cache problems are
// logged but never propagated: failing to cache is not a reason to fail the
// lookup.
func cacheStore(barcode string, product *barcodeProduct) cacheEntry {
	entry := cacheEntry{CachedAt: time.Now().UTC()}
	if product != nil {
		entry.Products = []barcodeProduct{*product}
	}

	if cacheDisabled() {
		return entry
	}

	path := cachePath()

	// Downloaded outside the lock so a slow image cannot block other lookups.
	if product != nil && product.ImageURL != "" {
		entry.ImageFile = storeImage(path, barcode, product.ImageURL)
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()

	cache := readCache(path)

	// Drop a previously cached image whose extension no longer matches.
	if previous, ok := cache.Entries[barcode]; ok && previous.ImageFile != "" && previous.ImageFile != entry.ImageFile {
		if previous.ImageFile == filepath.Base(previous.ImageFile) {
			_ = os.Remove(filepath.Join(cacheImageDir(path), previous.ImageFile))
		}
	}

	cache.Entries[barcode] = entry

	if err := writeCache(path, cache); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("Can not write barcode cache")
		return entry
	}

	log.Debug().Str("barcode", barcode).Str("path", path).Msg("Cached barcode lookup")

	return entry
}
