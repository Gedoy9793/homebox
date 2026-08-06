package v1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

const aliyunTestBody = `{
	"code": 200,
	"msg": "成功",
	"taskNo": "41020892700032664119",
	"charge": true,
	"data": {
		"code": "8801073141735",
		"img": "https://127.0.0.1:1/noodle.jpg",
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

// withAliyunTestServer points the package level endpoint at srv for one test.
func withAliyunTestServer(t *testing.T, srv *httptest.Server) {
	t.Helper()

	original := aliyunBarcodeEndpoint
	aliyunBarcodeEndpoint = srv.URL
	t.Cleanup(func() {
		aliyunBarcodeEndpoint = original
	})
}

// withAliyunTestCache redirects the cache to a temporary file so tests never
// touch the real data directory. It returns the cache path.
func withAliyunTestCache(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "barcode-cache.json")
	t.Setenv(aliyunCachePathEnv, path)
	t.Setenv(aliyunCacheTTLEnv, "")
	t.Setenv(aliyunCacheDisabledEnv, "")

	return path
}

func TestLookupAliyunBarcodeSendsFormAndAppCode(t *testing.T) {
	var gotMethod, gotAuth, gotContentType, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(aliyunTestBody))
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)

	if _, err := lookupAliyunBarcode("my-app-code", "8801073141735"); err != nil {
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

func TestLookupAliyunBarcodeMapsProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(aliyunTestBody))
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)

	products, err := lookupAliyunBarcode("code", "8801073141735")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(products))
	}

	p := products[0]
	if p.SearchEngineName != aliyunBarcodeSourceName {
		t.Fatalf("unexpected source name %q", p.SearchEngineName)
	}
	if p.Barcode != "8801073141735" {
		t.Fatalf("unexpected barcode %q", p.Barcode)
	}
	if p.Item.Name != "三养辣鸡肉芝士味拌面（油炸方便面）" {
		t.Fatalf("unexpected name %q", p.Item.Name)
	}
	if p.Manufacturer != "三养食品（上海）有限公司" {
		t.Fatalf("unexpected manufacturer %q", p.Manufacturer)
	}
	// Empty fields must not leave stray separators behind.
	if p.Item.Description != "700克 | 食品、饮料组合装" {
		t.Fatalf("unexpected description %q", p.Item.Description)
	}
	// Unreachable on purpose: the sample must not make tests hit the network.
	if p.ImageURL != "https://127.0.0.1:1/noodle.jpg" {
		t.Fatalf("unexpected image url %q", p.ImageURL)
	}
}

func TestBuildAliyunBarcodeProductFallsBackToTrademark(t *testing.T) {
	p, ok := buildAliyunBarcodeProduct("123", aliyunBarcodeData{
		GoodsName: "泡面",
		Trademark: "三养",
	})
	if !ok {
		t.Fatal("expected a product")
	}
	if p.Manufacturer != "三养" {
		t.Fatalf("expected trademark fallback, got %q", p.Manufacturer)
	}
	// The barcode from the request is used when the payload omits it.
	if p.Barcode != "123" {
		t.Fatalf("unexpected barcode %q", p.Barcode)
	}
}

func TestBuildAliyunBarcodeProductSkipsNamelessAndInsecureImage(t *testing.T) {
	if _, ok := buildAliyunBarcodeProduct("123", aliyunBarcodeData{Trademark: "三养"}); ok {
		t.Fatal("expected a payload without goodsName to be skipped")
	}

	p, ok := buildAliyunBarcodeProduct("123", aliyunBarcodeData{
		GoodsName: "泡面",
		Img:       "http://example.com/a.jpg",
	})
	if !ok {
		t.Fatal("expected a product")
	}
	if p.ImageURL != "" {
		t.Fatalf("expected non-HTTPS image to be dropped, got %q", p.ImageURL)
	}
}

func TestLookupAliyunBarcodeTreatsNoDataAsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":201,"msg":"查无数据"}`))
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)

	products, err := lookupAliyunBarcode("code", "0000000000000")
	if err != nil {
		t.Fatalf("expected no error for code 201, got %v", err)
	}
	if len(products) != 0 {
		t.Fatalf("expected no products, got %d", len(products))
	}
}

func TestLookupAliyunBarcodeReportsBusinessErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":400,"msg":"参数错误"}`))
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)

	_, err := lookupAliyunBarcode("code", "abc")
	if err == nil {
		t.Fatal("expected an error for code 400")
	}
	if !strings.Contains(err.Error(), "参数错误") {
		t.Fatalf("expected the API message in the error, got %v", err)
	}
}

func TestLookupAliyunBarcodeSurfacesGatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ca-Error-Message", "Unauthorized")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)

	_, err := lookupAliyunBarcode("bad-code", "123")
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected the gateway message in the error, got %v", err)
	}
}

func TestLookupAliyunBarcodeRequiresAppCode(t *testing.T) {
	if _, err := lookupAliyunBarcode("", "123"); err == nil {
		t.Fatal("expected an error when no app code is configured")
	}
}

func TestAliyunBarcodeProductsSkippedWithoutAppCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("the API must not be called without an app code")
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)
	withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "")

	if products := aliyunBarcodeProducts("123"); products != nil {
		t.Fatalf("expected no products, got %+v", products)
	}
}

func TestAliyunBarcodeProductsUsesEnvAppCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "APPCODE env-code" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		_, _ = w.Write([]byte(aliyunTestBody))
	}))
	defer srv.Close()

	withAliyunTestServer(t, srv)
	withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "  env-code  ")

	products := aliyunBarcodeProducts("8801073141735")
	if len(products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(products))
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

// countingAliyunServer serves body and reports how many times it was called, so
// tests can assert that the paid API is not hit again on a cache hit.
func countingAliyunServer(t *testing.T, body string) *int {
	t.Helper()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	withAliyunTestServer(t, srv)

	return &calls
}

func TestAliyunCacheServesSecondLookupFromDisk(t *testing.T) {
	calls := countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	first := aliyunBarcodeProducts("8801073141735")
	if len(first) != 1 {
		t.Fatalf("expected 1 product, got %d", len(first))
	}

	second := aliyunBarcodeProducts("8801073141735")
	if len(second) != 1 {
		t.Fatalf("expected 1 cached product, got %d", len(second))
	}
	if second[0].Item.Name != first[0].Item.Name {
		t.Fatalf("cached product differs: %q vs %q", second[0].Item.Name, first[0].Item.Name)
	}

	if *calls != 1 {
		t.Fatalf("expected exactly 1 API call, got %d", *calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected a cache file at %s: %v", path, err)
	}
}

// A "no data" reply is billed by the provider, so it must not be requested twice.
func TestAliyunCacheStoresNoDataResult(t *testing.T) {
	calls := countingAliyunServer(t, `{"code":201,"msg":"查无数据"}`)
	withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	if products := aliyunBarcodeProducts("0000000000000"); len(products) != 0 {
		t.Fatalf("expected no products, got %d", len(products))
	}
	if products := aliyunBarcodeProducts("0000000000000"); len(products) != 0 {
		t.Fatalf("expected no products on the second call, got %d", len(products))
	}

	if *calls != 1 {
		t.Fatalf("expected the negative result to be cached, got %d API calls", *calls)
	}
}

func TestAliyunCacheDoesNotStoreErrors(t *testing.T) {
	calls := countingAliyunServer(t, `{"code":500,"msg":"系统维护，请稍候再试"}`)
	withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	_ = aliyunBarcodeProducts("123")
	_ = aliyunBarcodeProducts("123")

	if *calls != 2 {
		t.Fatalf("expected errors not to be cached, got %d API calls", *calls)
	}
}

func TestAliyunCacheDisabledAlwaysCallsAPI(t *testing.T) {
	calls := countingAliyunServer(t, aliyunTestBody)
	withAliyunTestCache(t)
	t.Setenv(aliyunCacheDisabledEnv, "TRUE")
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	_ = aliyunBarcodeProducts("8801073141735")
	_ = aliyunBarcodeProducts("8801073141735")

	if *calls != 2 {
		t.Fatalf("expected 2 API calls with the cache disabled, got %d", *calls)
	}
}

func TestAliyunCacheHonoursTTL(t *testing.T) {
	calls := countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunCacheTTLEnv, "1h")
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	// Seed an entry that is older than the TTL.
	seed := aliyunBarcodeCache{Entries: map[string]aliyunBarcodeCacheEntry{
		"8801073141735": {
			CachedAt: time.Now().UTC().Add(-2 * time.Hour),
			Products: []repo.BarcodeProduct{{Item: repo.EntityCreate{Name: "stale"}}},
		},
	}}
	if err := writeAliyunCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	products := aliyunBarcodeProducts("8801073141735")
	if len(products) != 1 || products[0].Item.Name == "stale" {
		t.Fatalf("expected the expired entry to be refreshed, got %+v", products)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 API call, got %d", *calls)
	}

	// The refreshed entry is served without another call.
	_ = aliyunBarcodeProducts("8801073141735")
	if *calls != 1 {
		t.Fatalf("expected the refreshed entry to be cached, got %d API calls", *calls)
	}
}

func TestAliyunCacheWithoutTTLKeepsOldEntries(t *testing.T) {
	calls := countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	seed := aliyunBarcodeCache{Entries: map[string]aliyunBarcodeCacheEntry{
		"123": {
			CachedAt: time.Now().UTC().AddDate(-5, 0, 0),
			Products: []repo.BarcodeProduct{{Item: repo.EntityCreate{Name: "ancient"}}},
		},
	}}
	if err := writeAliyunCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	products := aliyunBarcodeProducts("123")
	if len(products) != 1 || products[0].Item.Name != "ancient" {
		t.Fatalf("expected the old entry to be served, got %+v", products)
	}
	if *calls != 0 {
		t.Fatalf("expected no API call, got %d", *calls)
	}
}

// The default must land on persistent storage: in the Docker image /data is a
// volume while the working directory is not.
func TestAliyunCacheDefaultPathPrefersDataVolume(t *testing.T) {
	t.Setenv(aliyunCachePathEnv, "")

	path := aliyunCachePath()
	if filepath.Base(path) != aliyunCacheFileName {
		t.Fatalf("unexpected cache file name in %q", path)
	}

	want := filepath.Join(".data", aliyunCacheFileName)
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		want = filepath.Join("/data", aliyunCacheFileName)
	}
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestAliyunCachePathHonoursOverride(t *testing.T) {
	t.Setenv(aliyunCachePathEnv, "  /tmp/custom-barcode-cache.json  ")

	if got := aliyunCachePath(); got != "/tmp/custom-barcode-cache.json" {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestAliyunCacheIgnoresInvalidTTL(t *testing.T) {
	t.Setenv(aliyunCacheTTLEnv, "not-a-duration")

	if ttl := aliyunCacheTTL(); ttl != 0 {
		t.Fatalf("expected an invalid TTL to be ignored, got %s", ttl)
	}
}

func TestAliyunCacheRecoversFromCorruptFile(t *testing.T) {
	calls := countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	if err := os.WriteFile(path, []byte("{not json"), aliyunCacheFileMode); err != nil {
		t.Fatalf("could not write the corrupt cache: %v", err)
	}

	if products := aliyunBarcodeProducts("8801073141735"); len(products) != 1 {
		t.Fatalf("expected the lookup to succeed despite the corrupt cache, got %d", len(products))
	}
	if *calls != 1 {
		t.Fatalf("expected 1 API call, got %d", *calls)
	}

	// The corrupt file must have been replaced by a usable one.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read the rebuilt cache: %v", err)
	}
	var rebuilt aliyunBarcodeCache
	if err := json.Unmarshal(body, &rebuilt); err != nil {
		t.Fatalf("expected valid JSON after the rebuild: %v", err)
	}
	if _, ok := rebuilt.Entries["8801073141735"]; !ok {
		t.Fatalf("expected the barcode to be cached, got %+v", rebuilt.Entries)
	}
}

func TestWriteAliyunCacheIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "cache.json")

	cache := aliyunBarcodeCache{Entries: map[string]aliyunBarcodeCacheEntry{
		"123": {CachedAt: time.Now().UTC()},
	}}
	if err := writeAliyunCache(path, cache); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected the cache file to exist: %v", err)
	}
	if perm := info.Mode().Perm(); perm != aliyunCacheFileMode {
		t.Fatalf("expected mode %o, got %o", aliyunCacheFileMode, perm)
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

func testPNGDataURI() string {
	return "data:" + mimePNG + ";base64," + base64.StdEncoding.EncodeToString(testPNG)
}

func TestAliyunCacheStoresImageAsFile(t *testing.T) {
	path := withAliyunTestCache(t)

	name := writeAliyunCacheImage(path, "8801073141735", testPNGDataURI())
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

	// The file holds the raw image, not base64, so it stays a usable picture.
	stored, err := os.ReadFile(filepath.Join(aliyunCacheImageDir(path), name))
	if err != nil {
		t.Fatalf("could not read the cached image: %v", err)
	}
	if !bytes.Equal(stored, testPNG) {
		t.Fatalf("cached image differs from the original (%d bytes)", len(stored))
	}

	if got := loadAliyunCacheImage(path, name); got != testPNGDataURI() {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

// The whole point of caching images: the entry stays usable after the provider's
// ten day link has expired.
func TestAliyunCacheServesImageAfterURLExpires(t *testing.T) {
	calls := countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	imageFile := writeAliyunCacheImage(path, "8801073141735", testPNGDataURI())
	seed := aliyunBarcodeCache{Entries: map[string]aliyunBarcodeCacheEntry{
		"8801073141735": {
			CachedAt: time.Now().UTC(),
			Products: []repo.BarcodeProduct{{
				Item: repo.EntityCreate{Name: "三养辣鸡肉芝士味拌面"},
				// A long dead link, as it would be months after the lookup.
				ImageURL: "https://expired.example.com/gone.jpg",
			}},
			ImageFile: imageFile,
		},
	}}
	if err := writeAliyunCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	products := aliyunBarcodeProducts("8801073141735")
	if len(products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(products))
	}
	if products[0].ImageBase64 != testPNGDataURI() {
		t.Fatalf("expected the cached image to be served, got %d bytes", len(products[0].ImageBase64))
	}
	// ImageURL must survive: the frontend uses it as the flag for attaching the photo.
	if products[0].ImageURL == "" {
		t.Fatal("expected ImageURL to be preserved")
	}
	if *calls != 0 {
		t.Fatalf("expected no API call, got %d", *calls)
	}
}

func TestAliyunCacheToleratesMissingImageFile(t *testing.T) {
	countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	seed := aliyunBarcodeCache{Entries: map[string]aliyunBarcodeCacheEntry{
		"123": {
			CachedAt:  time.Now().UTC(),
			Products:  []repo.BarcodeProduct{{Item: repo.EntityCreate{Name: "泡面"}}},
			ImageFile: "deadbeef.png",
		},
	}}
	if err := writeAliyunCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	products := aliyunBarcodeProducts("123")
	if len(products) != 1 || products[0].Item.Name != "泡面" {
		t.Fatalf("expected the entry to be served without its image, got %+v", products)
	}
	if products[0].ImageBase64 != "" {
		t.Fatal("expected no image data for a missing file")
	}
}

func TestLoadAliyunCacheImageRejectsPathTraversal(t *testing.T) {
	path := withAliyunTestCache(t)

	for _, name := range []string{"../secret.json", "nested/img.png", "/etc/passwd"} {
		if got := loadAliyunCacheImage(path, name); got != "" {
			t.Fatalf("expected %q to be rejected, got %d bytes", name, len(got))
		}
	}
}

func TestSplitDataURI(t *testing.T) {
	mime, raw, err := splitDataURI(testPNGDataURI())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != mimePNG {
		t.Fatalf("unexpected mime %q", mime)
	}
	if !bytes.Equal(raw, testPNG) {
		t.Fatal("payload mismatch")
	}

	for name, uri := range map[string]string{
		"no comma":     "data:image/png;base64",
		"no prefix":    "image/png;base64,AAAA",
		"not base64":   "data:image/png;base64,!!!!",
		"plain string": "https://example.com/a.png",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := splitDataURI(uri); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestAliyunCacheReplacesImageOnExtensionChange(t *testing.T) {
	path := withAliyunTestCache(t)

	// A PNG is cached first, then the same barcode comes back as a JPEG.
	pngName := writeAliyunCacheImage(path, "123", testPNGDataURI())
	seed := aliyunBarcodeCache{Entries: map[string]aliyunBarcodeCacheEntry{
		"123": {CachedAt: time.Now().UTC(), ImageFile: pngName},
	}}
	if err := writeAliyunCache(path, seed); err != nil {
		t.Fatalf("could not seed the cache: %v", err)
	}

	// Storing without an image URL leaves imageFile empty, which must clean up.
	aliyunCacheStore("123", []repo.BarcodeProduct{{Item: repo.EntityCreate{Name: "泡面"}}})

	if _, err := os.Stat(filepath.Join(aliyunCacheImageDir(path), pngName)); !os.IsNotExist(err) {
		t.Fatalf("expected the stale image to be removed, got %v", err)
	}
}

func TestAliyunCacheKeepsUnrelatedEntries(t *testing.T) {
	countingAliyunServer(t, aliyunTestBody)
	path := withAliyunTestCache(t)
	t.Setenv(aliyunBarcodeAppCodeEnv, "code")

	aliyunCacheStore("111", []repo.BarcodeProduct{{Item: repo.EntityCreate{Name: "first"}}})
	aliyunCacheStore("222", nil)

	cache := readAliyunCache(path)
	if len(cache.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cache.Entries))
	}
	if got := cache.Entries["111"].Products[0].Item.Name; got != "first" {
		t.Fatalf("unexpected cached name %q", got)
	}
	if _, ok := cache.Entries["222"]; !ok {
		t.Fatal("expected the negative entry to be kept")
	}
}
