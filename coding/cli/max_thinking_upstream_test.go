package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi: packages/coding-agent/src/cli/args.ts:19 (Args.thinking).
func TestMaxThinkingCodingAgentUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/max-thinking.test.ts:19
	t.Run("is accepted by CLI and settings", func(t *testing.T) {
		flags := parseArgs([]string{"--thinking", "max"})
		if flags.Thinking != "max" || len(flags.Diagnostics) != 0 {
			t.Fatalf("flags = %+v", flags)
		}
		settings := codingagent.NewSettingsManager(t.TempDir(), t.TempDir())
		if err := settings.SetDefaultThinkingLevel("max"); err != nil {
			t.Fatal(err)
		}
		if err := settings.Flush(); err != nil {
			t.Fatal(err)
		}
		if got := settings.GetDefaultThinkingLevel(); got != "max" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/max-thinking.test.ts:28
	t.Run("falls back to thinkingXhigh for legacy themes", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
		if err != nil {
			t.Fatal(err)
		}
		var theme struct {
			Name   string         `json:"name"`
			Vars   map[string]any `json:"vars"`
			Colors map[string]any `json:"colors"`
		}
		if err := json.Unmarshal(data, &theme); err != nil {
			t.Fatal(err)
		}
		theme.Name = "legacy-theme"
		delete(theme.Colors, "thinkingMax")
		data, err = json.Marshal(theme)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "legacy-theme.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		legacy, err := tui.LoadThemeFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := legacy.Fg("thinkingMax", "border"), legacy.Fg("thinkingXhigh", "border"); got != want {
			t.Fatalf("max=%q xhigh=%q", got, want)
		}
	})
}
