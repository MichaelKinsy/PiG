package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func capabilityOverridesString(o tui.CapabilityOverrides) string {
	format := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	return "images=" + format(o.Images) + " trueColor=" + format(o.TrueColor) + " hyperlinks=" + format(o.Hyperlinks)
}

// Mirrors settings-manager.ts getTerminalCapabilityOverrides: only a boolean
// hyperlinks/trueColor and exactly "kitty", "iterm2", or false for images
// override detection; "auto" and every other value leave detection alone.
func TestGetTerminalCapabilityOverrides(t *testing.T) {
	cases := []struct {
		name     string
		terminal string
		want     string
	}{
		{"absent", `{}`, "images=null trueColor=null hyperlinks=null"},
		{"booleans", `{"hyperlinks": true, "trueColor": false}`, "images=null trueColor=false hyperlinks=true"},
		{"auto strings", `{"hyperlinks": "auto", "trueColor": "auto", "images": "auto"}`, "images=null trueColor=null hyperlinks=null"},
		{"kitty", `{"images": "kitty"}`, `images="kitty" trueColor=null hyperlinks=null`},
		{"iterm2", `{"images": "iterm2"}`, `images="iterm2" trueColor=null hyperlinks=null`},
		{"images false means none", `{"images": false}`, `images="" trueColor=null hyperlinks=null`},
		{"images true is ignored", `{"images": true}`, "images=null trueColor=null hyperlinks=null"},
		{"images is case-sensitive", `{"images": "KITTY"}`, "images=null trueColor=null hyperlinks=null"},
		{"string booleans are ignored", `{"hyperlinks": "true", "trueColor": 1}`, "images=null trueColor=null hyperlinks=null"},
		{"null is ignored", `{"hyperlinks": null, "images": null}`, "images=null trueColor=null hyperlinks=null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Settings
			if err := json.Unmarshal([]byte(`{"terminal": `+tc.terminal+`}`), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := capabilityOverridesString(s.GetTerminalCapabilityOverrides()); got != tc.want {
				t.Fatalf("GetTerminalCapabilityOverrides() = %s, want %s", got, tc.want)
			}
		})
	}
}

// Mirrors deepMergeSettings: a project terminal value replaces the global one
// field by field, including "auto" and null, which then leave detection alone.
func TestTerminalCapabilitySettingsProjectMerge(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	writeJSON := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(filepath.Join(agentDir, "settings.json"), `{"terminal": {"hyperlinks": false, "trueColor": true, "images": "kitty"}}`)
	writeJSON(filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), `{"terminal": {"hyperlinks": "auto", "images": null}}`)
	sm := NewSettingsManager(cwd, agentDir)
	want := "images=null trueColor=true hyperlinks=null"
	if got := capabilityOverridesString(sm.GetTerminalCapabilityOverrides()); got != want {
		t.Fatalf("merged overrides = %s, want %s", got, want)
	}
}

// Setting another terminal field must keep the hand-written capability values
// byte-for-byte, as upstream saves only modified fields.
func TestTerminalCapabilitySettingsSurviveUnrelatedSave(t *testing.T) {
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"terminal": {"hyperlinks": "auto", "images": false, "trueColor": "sometimes"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(t.TempDir(), agentDir)
	if err := sm.SetShowImages(false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Terminal map[string]json.RawMessage `json:"terminal"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"hyperlinks": `"auto"`, "images": `false`, "trueColor": `"sometimes"`, "showImages": `false`} {
		if got := string(saved.Terminal[key]); got != want {
			t.Fatalf("terminal.%s = %s, want %s (file %s)", key, got, want, data)
		}
	}
	if got := capabilityOverridesString(sm.GetTerminalCapabilityOverrides()); got != `images="" trueColor=null hyperlinks=null` {
		t.Fatalf("overrides after save = %s", got)
	}
}
