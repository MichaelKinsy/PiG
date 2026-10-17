//go:build !pig_strip_export_html

package codingagent_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/suite/regressions/5596-missing-theme-export.test.ts:25-95. ExportSessionToHTML is the Session export path called by /export and RPC export_html; it uses the active theme without rewriting the configured preference.
func TestMissingConfiguredThemeExportsWithActiveFallback(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(func() { tui.SetTheme("dark") })
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	settings := services.SettingsManager()
	if err := settings.SetTheme("missing-theme"); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{replies: []scriptedReply{reply("hello", ai.Usage{})}}
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SystemPrompt: "You are a test assistant.", SkipBuiltinTools: true, SessionDir: filepath.Join(cwd, "sessions")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	if text := session.LastAssistantText(); text == nil || *text != "hello" {
		t.Fatalf("faux reply = %v, want hello", text)
	}
	tui.SetThemeSettingPresence(settings.GetThemeSetting())
	// upstream 0.99.1 theme.ts:772-790 setTheme falls back to the system theme; the regression test's initTheme("dark") only resets the theme between cases.
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("fallback theme = %q, want system", got)
	}

	outputPath := filepath.Join(cwd, "export.html")
	got, err := icodingagent.ExportSessionToHTML(session.Path(), outputPath, nil, cwd, icodingagent.ShareState{}, "")
	if err != nil || got != outputPath {
		t.Fatalf("export = %q, %v, want %q", got, err, outputPath)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatal(err)
	}
	if got := settings.GetTheme(); got != "missing-theme" {
		t.Fatalf("configured theme changed to %q", got)
	}
	// The original asserts file presence. Also verify that a real HTML document uses the fallback's colors, not an empty success artifact.
	html, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!DOCTYPE html>", "--accent: " + tui.ActiveTheme().GetResolvedThemeColors()["accent"] + ";"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("export is missing %q", want)
		}
	}
	// upstream 0.99.1 theme.ts getThemeExportColors returns no export colors for the system theme, so export-html/index.ts:42-107,121 derives the page background from userMessageBg as an rgb() value.
	if tui.ActiveTheme().ExportPageBg != "" || !regexp.MustCompile(`--exportPageBg: rgb\(\d+, \d+, \d+\);`).Match(html) {
		t.Errorf("the system theme's export must derive its page background (theme export color %q)", tui.ActiveTheme().ExportPageBg)
	}
}

// agent-session.ts exportToHtml: the export theme is the first of options.themeName and the settings theme that names a registered theme (getThemeByName), else the active theme; the page carries that theme's resolved colors.
// Pi: packages/coding-agent/src/core/agent-session.ts:4273 (Session.exportToHtml).
func TestExportToHTMLChoosesTheNamedThenSettingsTheme(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(func() { tui.SetTheme("dark") })
	tui.SetTheme("dark")
	dark, light := tui.ThemeByName("dark"), tui.ThemeByName("light")
	if dark == nil || light == nil || dark.GetResolvedThemeColors()["accent"] == light.GetResolvedThemeColors()["accent"] {
		t.Fatalf("the built-in dark and light themes must differ in accent: %v %v", dark, light)
	}
	accent := func(th *tui.Theme) string { return "--accent: " + th.GetResolvedThemeColors()["accent"] + ";" }
	tests := []struct {
		name     string
		settings string
		option   []coding.ExportToHTMLOptions
		want     *tui.Theme
	}{
		{"settings theme beats the active theme", "light", nil, light},
		{"option beats the settings theme", "light", []coding.ExportToHTMLOptions{{ThemeName: "dark"}}, dark},
		{"missing option falls to the settings theme", "light", []coding.ExportToHTMLOptions{{ThemeName: "nope"}}, light},
		{"missing settings theme falls to the active theme", "nope", nil, dark},
		{"no settings theme uses the active theme", "", nil, dark},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if tc.settings != "" {
				if err := services.SettingsManager().SetTheme(tc.settings); err != nil {
					t.Fatal(err)
				}
			}
			provider := &scriptedProvider{replies: []scriptedReply{reply("hello", ai.Usage{})}}
			model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SystemPrompt: "You are a test assistant.", SkipBuiltinTools: true, SessionDir: filepath.Join(cwd, "sessions")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := session.Send(t.Context(), "hi"); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(cwd, "export.html")
			if _, err := session.ExportToHTML(out, tc.option...); err != nil {
				t.Fatal(err)
			}
			html, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(html), accent(tc.want)) {
				t.Errorf("export lacks %q", accent(tc.want))
			}
		})
	}
}
