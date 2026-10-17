package aotruntime

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultCacheBudget is the total size of the compiled images the store keeps: 1 GB, as the cache budget setting's default.
const defaultCacheBudget int64 = 1 << 30

// Environment variables of the provider.
const (
	// envVerbose shows a message for each build phase; the default is silence.
	envVerbose = "PIG_AOT_VERBOSE"
	// envCacheMax overrides the cache budget, for example 2GB or 500MB.
	envCacheMax = "PIG_AOT_CACHE_MAX"
	// envReleaseURL replaces the host the Pi-Bolt release downloads from, for a mirror or an offline copy. Every download is still verified against the
	// signature key and the digests pinned in this Piglet.
	envReleaseURL = "PIG_AOT_RELEASE_URL"
)

// config is <provider dir>/config.json, written by /aot-runtime budget. The environment overrides it.
type config struct {
	CacheMax string `json:"cacheMax,omitempty"`
}

func (s *store) configPath() string { return filepath.Join(s.dir, "config.json") }

func (s *store) readConfig() config {
	var c config
	if data, err := os.ReadFile(s.configPath()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	return c
}

func (s *store) writeConfig(c config) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.configPath(), append(data, '\n'), 0o644)
}

// cacheBudget returns the budget in bytes: the environment, else the config file, else the default.
func (s *store) cacheBudget() (int64, error) {
	text := os.Getenv(envCacheMax)
	if text == "" {
		text = s.readConfig().CacheMax
	}
	if text == "" {
		return defaultCacheBudget, nil
	}
	return parseSize(text)
}

// parseSize reads a size such as 1GB, 512MB, 2g or a byte count. A budget of zero keeps no image.
func parseSize(text string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(text))
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		factor int64
	}{{"gb", 1 << 30}, {"g", 1 << 30}, {"mb", 1 << 20}, {"m", 1 << 20}, {"kb", 1 << 10}, {"k", 1 << 10}, {"b", 1}} {
		if rest, ok := strings.CutSuffix(t, unit.suffix); ok {
			t, multiplier = strings.TrimSpace(rest), unit.factor
			break
		}
	}
	value, err := strconv.ParseFloat(t, 64)
	// Infinity, NaN and a size past int64 have no defined conversion; on amd64 they turn negative, and prune would then remove every image.
	if bytes := value * float64(multiplier); err != nil || !(value >= 0) || math.IsInf(value, 0) || bytes >= math.MaxInt64 {
		return 0, fmt.Errorf("%q is not a size: use a number with an optional KB, MB or GB suffix", text)
	}
	return int64(value * float64(multiplier)), nil
}

func verbose() bool {
	value := os.Getenv(envVerbose)
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

func offline() bool {
	for _, name := range []string{"PIG_OFFLINE", "PI_OFFLINE"} {
		if value := os.Getenv(name); value != "" && value != "0" && !strings.EqualFold(value, "false") {
			return true
		}
	}
	return false
}
