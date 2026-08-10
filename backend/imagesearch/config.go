package imagesearch

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvURL          = "HBOX_IMAGE_SEARCH_URL"
	EnvSyncInterval = "HBOX_IMAGE_SEARCH_SYNC_INTERVAL"
	EnvTopK         = "HBOX_IMAGE_SEARCH_TOP_K"

	defaultSyncInterval = 5 * time.Minute
	defaultTopK         = 20
)

// Config holds runtime settings for the image-search sidecar bridge.
// Values come from environment variables only (no conf.go changes).
type Config struct {
	URL          string
	SyncInterval time.Duration
	TopK         int
}

// Enabled reports whether the sidecar URL is configured.
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.URL) != ""
}

// LoadConfig reads image-search settings from the environment.
func LoadConfig() Config {
	cfg := Config{
		URL:          strings.TrimRight(strings.TrimSpace(os.Getenv(EnvURL)), "/"),
		SyncInterval: defaultSyncInterval,
		TopK:         defaultTopK,
	}

	if v := strings.TrimSpace(os.Getenv(EnvSyncInterval)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.SyncInterval = d
		}
	}

	if v := strings.TrimSpace(os.Getenv(EnvTopK)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.TopK = n
		}
	}

	return cfg
}
