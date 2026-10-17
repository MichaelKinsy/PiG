package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi derives the setting value types from its TypeBox settings schema (settings-schema.ts:464-470: NonNullable<SettingsInput[key]>), so
// each type's closed set is the anyOf of that setting in the schema the pinned Pi publishes (packages/coding-agent/schemas/settings.schema.json).
// The Go types are separate declarations from the schema Pig renders (configschema/settings.go), so this ties each Go value set to Pi's.
func TestSettingValueTypesAreThePinnedPiSchemaSets(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".upstream", "current", "packages", "coding-agent", "schemas", "settings.schema.json"))
	if err != nil {
		t.Fatalf("pinned Pi settings schema (link .upstream/current): %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			AnyOf []struct {
				Type  string `json:"type"`
				Const any    `json:"const"`
			} `json:"anyOf"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	piSet := func(key string) []string {
		var values []string
		for _, alternative := range schema.Properties[key].AnyOf {
			switch {
			case alternative.Const != nil:
				encoded, _ := json.Marshal(alternative.Const)
				values = append(values, string(encoded))
			case alternative.Type == "boolean":
				values = append(values, "false", "true")
			}
		}
		slices.Sort(values)
		return values
	}
	goSet := func(values ...any) []string {
		var encoded []string
		for _, value := range values {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, string(data))
		}
		slices.Sort(encoded)
		return encoded
	}
	var cacheWarming []any
	for _, mode := range CacheWarmingModes {
		cacheWarming = append(cacheWarming, mode)
	}
	for _, c := range []struct {
		key string
		got []string
	}{
		{"cacheWarming", goSet(cacheWarming...)},
		{"defaultProjectTrust", goSet(DefaultProjectTrustAsk, DefaultProjectTrustAlways, DefaultProjectTrustNever)},
		{"fullscreenExitOutput", goSet(FullscreenExitOutputTranscript, FullscreenExitOutputResumeHint)},
		{"tuiMode", goSet(tui.TuiModeRegular, tui.TuiModeFullscreen)},
		{"quietStartup", goSet(QuietStartupFalse, QuietStartupTrue, QuietStartupHeader)},
	} {
		want := piSet(c.key)
		if len(want) == 0 {
			t.Errorf("%s: the pinned Pi schema has no closed set", c.key)
		}
		if !slices.Equal(c.got, want) {
			t.Errorf("%s: Go values %v, pinned Pi %v", c.key, c.got, want)
		}
	}
}
