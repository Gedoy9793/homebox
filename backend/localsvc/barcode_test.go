package localsvc

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAPIBody = `{
	"code": 200,
	"msg": "成功",
	"taskNo": "41020892700032664119",
	"charge": true,
	"data": {
		"code": "8801073141735",
		"img": "%IMAGE%",
		"goodsType": "",
		"trademark": "三养",
		"goodsName": "三养辣鸡肉芝士味拌面（油炸方便面）",
		"spec": "700克",
		"ycg": "",
		"manuName": "三养食品（上海）有限公司",
		"manuAddress": "",
		"gpcType": "食品、饮料组合装"
	}
}`

// unreachableImage keeps the sample payload from making tests hit the network.
const unreachableImage = "https://127.0.0.1:1/noodle.jpg"

func apiBody(imageURL string) string {
	return strings.ReplaceAll(testAPIBody, "%IMAGE%", imageURL)
}

// withTestEndpoint points the package level endpoint at srv for one test.
func withTestEndpoint(t *testing.T, srv *httptest.Server) {
	t.Helper()

	original := barcodeEndpoint
	barcodeEndpoint = srv.URL
	t.Cleanup(func() {
		barcodeEndpoint = original
	})
}

// withTestCache redirects the cache to a temporary file so tests never touch the
// real data directory. It returns the cache path.
func withTestCache(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "barcode-cache.json")
	t.Setenv(EnvCachePath, path)
	t.Setenv(EnvCacheTTL, "")
	t.Setenv(EnvCacheDisabled, "")

	return path
}

// countingServer serves body and reports how many times it was called, so tests
// can assert that the paid API is not hit again on a cache hit.
func countingServer(t *testing.T, body string) *int {
	t.Helper()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	withTestEndpoint(t, srv)

	return &calls
}

func TestLookupBarcodeSendsFormAndAppCode(t *testing.T) {
	var gotMethod, gotAuth, gotContentType, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(apiBody(unreachableImage)))
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)

	if _, err := lookupBarcode("my-app-code", "8801073141735"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotAuth != "APPCODE my-app-code" {
		t.Fatalf("unexpected Authorization header %q", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Fatalf("unexpected Content-Type %q", gotContentType)
	}
	if gotBody != "code=8801073141735" {
		t.Fatalf("unexpected request body %q", gotBody)
	}
}

func TestLookupBarcodeMapsProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(apiBody(unreachableImage)))
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)

	product, err := lookupBarcode("code", "8801073141735")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if product == nil {
		t.Fatal("expected a product")
	}

	if product.Item.Name != "三养辣鸡肉芝士味拌面（油炸方便面）" {
		t.Fatalf("unexpected name %q", product.Item.Name)
	}
	if product.Manufacturer != "三养食品（上海）有限公司" {
		t.Fatalf("unexpected manufacturer %q", product.Manufacturer)
	}
	// Empty fields must not leave stray separators behind.
	if product.Item.Description != "700克 | 食品、饮料组合装" {
		t.Fatalf("unexpected description %q", product.Item.Description)
	}
	if product.ImageURL != unreachableImage {
		t.Fatalf("unexpected image url %q", product.ImageURL)
	}
}

func TestBuildProductFallsBackToTrademark(t *testing.T) {
	product, ok := buildProduct(barcodeData{GoodsName: "泡面", Trademark: "三养"})
	if !ok {
		t.Fatal("expected a product")
	}
	if product.Manufacturer != "三养" {
		t.Fatalf("expected trademark fallback, got %q", product.Manufacturer)
	}
}

func TestBuildProductSkipsNamelessAndInsecureImage(t *testing.T) {
	if _, ok := buildProduct(barcodeData{Trademark: "三养"}); ok {
		t.Fatal("expected a payload without goodsName to be skipped")
	}

	product, ok := buildProduct(barcodeData{GoodsName: "泡面", Img: "http://example.com/a.jpg"})
	if !ok {
		t.Fatal("expected a product")
	}
	if product.ImageURL != "" {
		t.Fatalf("expected non-HTTPS image to be dropped, got %q", product.ImageURL)
	}
}

func TestLookupBarcodeTreatsNoDataAsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":201,"msg":"查无数据"}`))
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)

	product, err := lookupBarcode("code", "0000000000000")
	if err != nil {
		t.Fatalf("expected no error for code 201, got %v", err)
	}
	if product != nil {
		t.Fatalf("expected no product, got %+v", product)
	}
}

func TestLookupBarcodeReportsBusinessErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":400,"msg":"参数错误"}`))
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)

	if _, err := lookupBarcode("code", "abc"); err == nil {
		t.Fatal("expected an error for code 400")
	} else if !strings.Contains(err.Error(), "参数错误") {
		t.Fatalf("expected the API message in the error, got %v", err)
	}
}

func TestLookupBarcodeSurfacesGatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ca-Error-Message", "Unauthorized")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)

	if _, err := lookupBarcode("bad-code", "123"); err == nil {
		t.Fatal("expected an error for a 403 response")
	} else if !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected the gateway message in the error, got %v", err)
	}
}

func TestResolveBarcodeSkippedWithoutAppCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("the API must not be called without an app code")
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)
	withTestCache(t)
	t.Setenv(EnvAppCode, "")

	if _, ok := resolveBarcode("123"); ok {
		t.Fatal("expected no answer without an app code")
	}
}

func TestResolveBarcodeUsesEnvAppCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "APPCODE env-code" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		_, _ = w.Write([]byte(apiBody(unreachableImage)))
	}))
	defer srv.Close()

	withTestEndpoint(t, srv)
	withTestCache(t)
	t.Setenv(EnvAppCode, "  env-code  ")

	entry, ok := resolveBarcode("8801073141735")
	if !ok || len(entry.Products) != 1 {
		t.Fatalf("expected 1 product, got %+v", entry)
	}
}

func TestTruncateRunesRespectsRuneBoundaries(t *testing.T) {
	if got := truncateRunes("中文测试", 2); got != "中文" {
		t.Fatalf("expected %q, got %q", "中文", got)
	}
	if got := truncateRunes("short", 100); got != "short" {
		t.Fatalf("expected input to be returned unchanged, got %q", got)
	}
}

func TestCacheServesSecondLookupFromDisk(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	path := withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	first, ok := resolveBarcode("8801073141735")
	if !ok || len(first.Products) != 1 {
		t.Fatalf("expected 1 product, got %+v", first)
	}

	second, ok := resolveBarcode("8801073141735")
	if !ok || len(second.Products) != 1 {
		t.Fatalf("expected 1 cached product, got %+v", second)
	}
	if second.Products[0].Item.Name != first.Products[0].Item.Name {
		t.Fatalf("cached product differs: %q", second.Products[0].Item.Name)
	}

	if *calls != 1 {
		t.Fatalf("expected exactly 1 API call, got %d", *calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected a cache file at %s: %v", path, err)
	}
}

// A "no data" reply is billed by the provider but is not cached, so each scan
// retries until a product is found or the operator stops scanning.
func TestCacheDoesNotStoreNoDataResult(t *testing.T) {
	calls := countingServer(t, `{"code":201,"msg":"查无数据"}`)
	withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	for range 2 {
		entry, ok := resolveBarcode("0000000000000")
		if ok || len(entry.Products) != 0 {
			t.Fatalf("expected no cached answer for no-data, got ok=%v entry=%+v", ok, entry)
		}
	}

	if *calls != 2 {
		t.Fatalf("expected no-data lookups not to be cached, got %d API calls", *calls)
	}
}

func TestCacheDoesNotStoreErrors(t *testing.T) {
	calls := countingServer(t, `{"code":500,"msg":"系统维护，请稍候再试"}`)
	withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	for range 2 {
		if _, ok := resolveBarcode("123"); ok {
			t.Fatal("expected no answer after an API error")
		}
	}

	if *calls != 2 {
		t.Fatalf("expected errors not to be cached, got %d API calls", *calls)
	}
}

func TestCacheDisabledAlwaysCallsAPI(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	withTestCache(t)
	t.Setenv(EnvCacheDisabled, "TRUE")
	t.Setenv(EnvAppCode, "code")

	for range 2 {
		if _, ok := resolveBarcode("8801073141735"); !ok {
			t.Fatal("expected a product")
		}
	}

	if *calls != 2 {
		t.Fatalf("expected 2 API calls with the cache disabled, got %d", *calls)
	}
}

func TestCacheHonoursTTL(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	path := withTestCache(t)
	t.Setenv(EnvCacheTTL, "1h")
	t.Setenv(EnvAppCode, "code")

	// Seed an entry that is older than the TTL.
	seed := barcodeCache{Entries: map[string]cacheEntry{
		"8801073141735": {
			CachedAt: time.Now().UTC().Add(-2 * time.Hour),
			Products: []barcodeProduct{{Item: barcodeProductItem{Name: "stale"}}},
		},
	}}
	if err := writeCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	entry, _ := resolveBarcode("8801073141735")
	if len(entry.Products) != 1 || entry.Products[0].Item.Name == "stale" {
		t.Fatalf("expected the expired entry to be refreshed, got %+v", entry)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 API call, got %d", *calls)
	}

	// The refreshed entry is served without another call.
	if _, ok := resolveBarcode("8801073141735"); !ok {
		t.Fatal("expected the refreshed entry")
	}
	if *calls != 1 {
		t.Fatalf("expected the refreshed entry to be cached, got %d API calls", *calls)
	}
}

func TestCacheWithoutTTLKeepsOldEntries(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	path := withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	seed := barcodeCache{Entries: map[string]cacheEntry{
		"123": {
			CachedAt: time.Now().UTC().AddDate(-5, 0, 0),
			Products: []barcodeProduct{{Item: barcodeProductItem{Name: "ancient"}}},
		},
	}}
	if err := writeCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	entry, _ := resolveBarcode("123")
	if len(entry.Products) != 1 || entry.Products[0].Item.Name != "ancient" {
		t.Fatalf("expected the old entry to be served, got %+v", entry)
	}
	if *calls != 0 {
		t.Fatalf("expected no API call, got %d", *calls)
	}
}

// A cache written by the previous, in-handler implementation must still be a hit:
// every entry in it was paid for.
func TestCacheReadsPreviousFormat(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	path := withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	legacy := `{"entries":{"8801073141735":{"cachedAt":"2025-01-01T00:00:00Z","products":[{` +
		`"search_engine_name":"market.alicloudapi.com","modelNumber":"","manufacturer":"三养",` +
		`"barcode":"8801073141735","imageURL":"https://expired.example.com/gone.jpg","imageBase64":"",` +
		`"item":{"name":"三养拌面","description":"700克"}}]}}}`
	if err := os.WriteFile(path, []byte(legacy), cacheFileMode); err != nil {
		t.Fatalf("could not write the legacy cache: %v", err)
	}

	entry, ok := resolveBarcode("8801073141735")
	if !ok || len(entry.Products) != 1 {
		t.Fatalf("expected the legacy entry to be served, got %+v", entry)
	}
	if entry.Products[0].Item.Name != "三养拌面" {
		t.Fatalf("unexpected name %q", entry.Products[0].Item.Name)
	}
	if entry.Products[0].Manufacturer != "三养" {
		t.Fatalf("unexpected manufacturer %q", entry.Products[0].Manufacturer)
	}
	if *calls != 0 {
		t.Fatalf("expected no API call, got %d", *calls)
	}
}

// The default must land on persistent storage: in the Docker image /data is a
// volume while the working directory is not.
func TestCachePathPrefersDataVolume(t *testing.T) {
	t.Setenv(EnvCachePath, "")

	path := cachePath()
	if filepath.Base(path) != cacheFileName {
		t.Fatalf("unexpected cache file name in %q", path)
	}

	want := filepath.Join(".data", cacheFileName)
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		want = filepath.Join("/data", cacheFileName)
	}
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestCachePathHonoursOverride(t *testing.T) {
	t.Setenv(EnvCachePath, "  /tmp/custom-barcode-cache.json  ")

	if got := cachePath(); got != "/tmp/custom-barcode-cache.json" {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestCacheIgnoresInvalidTTL(t *testing.T) {
	t.Setenv(EnvCacheTTL, "not-a-duration")

	if ttl := cacheTTL(); ttl != 0 {
		t.Fatalf("expected an invalid TTL to be ignored, got %s", ttl)
	}
}

func TestCacheRecoversFromCorruptFile(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	path := withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	if err := os.WriteFile(path, []byte("{not json"), cacheFileMode); err != nil {
		t.Fatalf("could not write the corrupt cache: %v", err)
	}

	if _, ok := resolveBarcode("8801073141735"); !ok {
		t.Fatal("expected the lookup to succeed despite the corrupt cache")
	}
	if *calls != 1 {
		t.Fatalf("expected 1 API call, got %d", *calls)
	}

	// The corrupt file must have been replaced by a usable one.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read the rebuilt cache: %v", err)
	}
	var rebuilt barcodeCache
	if err := json.Unmarshal(body, &rebuilt); err != nil {
		t.Fatalf("expected valid JSON after the rebuild: %v", err)
	}
	if _, ok := rebuilt.Entries["8801073141735"]; !ok {
		t.Fatalf("expected the barcode to be cached, got %+v", rebuilt.Entries)
	}
}

func TestWriteCacheIsAtomicAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cache.json")

	cache := barcodeCache{Entries: map[string]cacheEntry{"123": {CachedAt: time.Now().UTC()}}}
	if err := writeCache(path, cache); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected the cache file to exist: %v", err)
	}
	if perm := info.Mode().Perm(); perm != cacheFileMode {
		t.Fatalf("expected mode %o, got %o", cacheFileMode, perm)
	}

	// No temporary files may be left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("could not read the cache directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the cache file, got %d entries", len(entries))
	}
}

// testPNG is a payload http.DetectContentType recognises as a PNG.
var testPNG = append([]byte("\x89PNG\r\n\x1a\n"), []byte("cached-image-bytes")...)

// imageServer serves testPNG and returns its URL.
func imageServer(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testPNG)
	}))
	t.Cleanup(srv.Close)

	return srv.URL + "/noodle.png"
}

func TestStoreImageWritesRawBytes(t *testing.T) {
	path := withTestCache(t)

	name := storeImage(path, "8801073141735", imageServer(t))
	if name == "" {
		t.Fatal("expected an image file name")
	}
	if !strings.HasSuffix(name, ".png") {
		t.Fatalf("expected a .png extension, got %q", name)
	}
	// The barcode must not leak into the file name.
	if strings.Contains(name, "8801073141735") {
		t.Fatalf("expected the name to be hashed, got %q", name)
	}

	stored, err := os.ReadFile(filepath.Join(cacheImageDir(path), name))
	if err != nil {
		t.Fatalf("could not read the cached image: %v", err)
	}
	if !bytes.Equal(stored, testPNG) {
		t.Fatalf("cached image differs from the original (%d bytes)", len(stored))
	}

	mime, raw, err := readImage(path, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != mimePNG || !bytes.Equal(raw, testPNG) {
		t.Fatalf("round trip mismatch: %s, %d bytes", mime, len(raw))
	}
}

func TestReadImageRejectsPathTraversal(t *testing.T) {
	path := withTestCache(t)

	for _, name := range []string{"../secret.json", "nested/img.png", "/etc/passwd", ""} {
		if _, _, err := readImage(path, name); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

func TestCacheReplacesImageOnExtensionChange(t *testing.T) {
	path := withTestCache(t)

	// An image is cached first, then the same barcode comes back without one.
	name := storeImage(path, "123", imageServer(t))
	seed := barcodeCache{Entries: map[string]cacheEntry{
		"123": {CachedAt: time.Now().UTC(), ImageFile: name},
	}}
	if err := writeCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	cacheStore("123", &barcodeProduct{Item: barcodeProductItem{Name: "泡面"}})

	if _, err := os.Stat(filepath.Join(cacheImageDir(path), name)); !os.IsNotExist(err) {
		t.Fatalf("expected the stale image to be removed, got %v", err)
	}
}

func TestCacheKeepsUnrelatedEntries(t *testing.T) {
	path := withTestCache(t)

	cacheStore("111", &barcodeProduct{Item: barcodeProductItem{Name: "first"}})
	cacheStore("222", nil)

	cache := readCache(path)
	if len(cache.Entries) != 1 {
		t.Fatalf("expected only successful lookups to be cached, got %d entries", len(cache.Entries))
	}
	if got := cache.Entries["111"].Products[0].Item.Name; got != "first" {
		t.Fatalf("unexpected cached name %q", got)
	}
	if _, ok := cache.Entries["222"]; ok {
		t.Fatal("expected no-data lookups not to be written to the cache file")
	}
}

// The reply has to be shaped like the Open Facts product API, because that is
// the client upstream uses to read this source.
func TestBarcodeEndpointServesOpenFactsShape(t *testing.T) {
	calls := countingServer(t, apiBody(unreachableImage))
	path := withTestCache(t)
	t.Setenv(EnvAppCode, "code")

	// Seeded rather than looked up, because the provider only ever hands out
	// HTTPS image links and a test server cannot offer one.
	seed := barcodeCache{Entries: map[string]cacheEntry{
		"8801073141735": {
			CachedAt: time.Now().UTC(),
			Products: []barcodeProduct{{
				Manufacturer: "三养食品（上海）有限公司",
				ImageURL:     "https://expired.example.com/gone.jpg",
				Item: barcodeProductItem{
					Name:        "三养辣鸡肉芝士味拌面（油炸方便面）",
					Description: "700克 | 食品、饮料组合装",
				},
			}},
			ImageFile: storeImage(path, "8801073141735", imageServer(t)),
		},
	}}
	if err := writeCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	service := httptest.NewServer(newMux())
	defer service.Close()

	response, err := http.Get(service.URL + BarcodePath + "8801073141735.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	var reply openFactsResponse
	if err := json.NewDecoder(response.Body).Decode(&reply); err != nil {
		t.Fatalf("could not decode the reply: %v", err)
	}

	if reply.Status != statusFound {
		t.Fatalf("expected status %d, got %d", statusFound, reply.Status)
	}
	if reply.Product.ProductName != "三养辣鸡肉芝士味拌面（油炸方便面）" {
		t.Fatalf("unexpected product_name %q", reply.Product.ProductName)
	}
	if reply.Product.Brands != "三养食品（上海）有限公司" {
		t.Fatalf("unexpected brands %q", reply.Product.Brands)
	}
	if reply.Product.GenericName != "700克 | 食品、饮料组合装" {
		t.Fatalf("unexpected generic_name %q", reply.Product.GenericName)
	}

	// The image must be served from the local cache rather than the provider's
	// link, which expires after about ten days.
	if !strings.Contains(reply.Product.ImageURL, BarcodeImagePath) {
		t.Fatalf("expected a local image URL, got %q", reply.Product.ImageURL)
	}

	image, err := http.Get(reply.Product.ImageURL)
	if err != nil {
		t.Fatalf("could not fetch the image: %v", err)
	}
	defer func() { _ = image.Body.Close() }()

	if got := image.Header.Get("Content-Type"); got != mimePNG {
		t.Fatalf("unexpected image content type %q", got)
	}

	raw, _ := io.ReadAll(image.Body)
	if !bytes.Equal(raw, testPNG) {
		t.Fatalf("image mismatch (%d bytes)", len(raw))
	}

	// Served from the cache, so the paid API stayed untouched.
	if *calls != 0 {
		t.Fatalf("expected no API call, got %d", *calls)
	}
}

func TestBarcodeEndpointReportsUnknownBarcode(t *testing.T) {
	withTestCache(t)
	t.Setenv(EnvAppCode, "")

	service := httptest.NewServer(newMux())
	defer service.Close()

	// Path traversal never reaches the handler: net/http cleans the request path
	// before routing. What is left to reject here is anything that is shaped
	// wrongly for a barcode.
	for _, path := range []string{"123.json", "not a barcode.json", "semi;colon.json"} {
		response, err := http.Get(service.URL + BarcodePath + path)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", path, err)
		}

		var reply openFactsResponse
		err = json.NewDecoder(response.Body).Decode(&reply)
		_ = response.Body.Close()
		if err != nil {
			t.Fatalf("could not decode the reply for %q: %v", path, err)
		}

		if reply.Status != statusMissing {
			t.Fatalf("expected %q to report no data, got status %d", path, reply.Status)
		}
	}
}

func TestValidBarcode(t *testing.T) {
	valid := []string{"8801073141735", "ABC-123"}
	invalid := []string{"", "../etc", "has space", "semi;colon", strings.Repeat("1", maxBarcodeLength+1)}

	for _, barcode := range valid {
		if !validBarcode(barcode) {
			t.Errorf("expected %q to be accepted", barcode)
		}
	}
	for _, barcode := range invalid {
		if validBarcode(barcode) {
			t.Errorf("expected %q to be rejected", barcode)
		}
	}
}
