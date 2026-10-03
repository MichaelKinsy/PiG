package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func settingsEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func settingsOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertSettingsFileJSON(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	settingsOK(t, err)
	var gotValue, wantValue any
	settingsOK(t, json.Unmarshal(data, &gotValue))
	settingsOK(t, json.Unmarshal([]byte(want), &wantValue))
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("settings = %s, want %s", data, want)
	}
}

func TestSettingsManagerOriginalExternalChanges(t *testing.T) {
	for _, tc := range []struct {
		name, initial, external, want string
		change                        func(*SettingsManager) error
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:29
		{"should preserve enabledModels when changing thinking level", `{"theme":"dark","defaultModel":"claude-sonnet"}`, `{"theme":"dark","defaultModel":"claude-sonnet","enabledModels":["claude-opus-4-5","gpt-5.2-codex"]}`, `{"theme":"dark","defaultModel":"claude-sonnet","enabledModels":["claude-opus-4-5","gpt-5.2-codex"],"defaultThinkingLevel":"high"}`, func(sm *SettingsManager) error { return sm.SetDefaultThinkingLevel("high") }},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:60
		{"should preserve custom settings when changing theme", `{"defaultModel":"claude-sonnet"}`, `{"defaultModel":"claude-sonnet","shellPath":"/bin/zsh","extensions":["/path/to/extension.ts"]}`, `{"defaultModel":"claude-sonnet","shellPath":"/bin/zsh","extensions":["/path/to/extension.ts"],"theme":"light"}`, func(sm *SettingsManager) error { return sm.SetTheme("light") }},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:88
		{"should let in-memory changes override file changes for same key", `{"theme":"dark"}`, `{"theme":"dark","defaultThinkingLevel":"low"}`, `{"theme":"dark","defaultThinkingLevel":"high"}`, func(sm *SettingsManager) error { return sm.SetDefaultThinkingLevel("high") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := writeSettingsLayers(t, tc.initial, `{}`)
			writeSettingsFixture(t, sm.GlobalPath(), tc.external)
			settingsOK(t, tc.change(sm))
			settingsOK(t, sm.Flush())
			assertSettingsFileJSON(t, sm.GlobalPath(), tc.want)
		})
	}
}

func TestSettingsManagerOriginalStorage(t *testing.T) {
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:132
	t.Run("should keep local-only extensions in extensions array", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"extensions":["/local/ext.ts","./relative/ext.ts"]}`, `{}`)
		settingsEqual(t, sm.GetPackages(), []PackageSource{})
		settingsEqual(t, sm.GetExtensionPaths(), []string{"/local/ext.ts", "./relative/ext.ts"})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:147
	t.Run("should handle packages with filtering objects", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"packages":["npm:simple-pkg",{"source":"npm:shitty-extensions","extensions":["extensions/oracle.ts"],"skills":[]}]}`, `{}`)
		settingsEqual(t, sm.GetPackages(), []PackageSource{{Source: "npm:simple-pkg"}, {Source: "npm:shitty-extensions", Extensions: []string{"extensions/oracle.ts"}, Skills: []string{}, WasObject: true}})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:177
	t.Run("should reload global settings from disk", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"dark","extensions":["/before.ts"]}`, `{}`)
		writeSettingsFixture(t, sm.GlobalPath(), `{"theme":"light","extensions":["/after.ts"],"defaultModel":"claude-sonnet"}`)
		sm.Reload()
		settingsEqual(t, sm.GetTheme(), "light")
		settingsEqual(t, sm.GetExtensionPaths(), []string{"/after.ts"})
		settingsEqual(t, sm.GetDefaultModel(), "claude-sonnet")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:205
	t.Run("should keep previous settings and report the file path when the file is invalid", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"dark"}`, `{}`)
		writeSettingsFixture(t, sm.GlobalPath(), `{ invalid json`)
		sm.Reload()
		settingsEqual(t, sm.GetTheme(), "dark")
		errs := sm.DrainErrors()
		if len(errs) != 1 || errs[0].Scope != "global" || errs[0].Path != sm.GlobalPath() {
			t.Fatalf("errors=%v", errs)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:238
	t.Run("should collect and clear load errors via drainErrors", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{ invalid global json`, `{ invalid project json`)
		errs := sm.DrainErrors()
		if len(errs) != 2 {
			t.Fatalf("errors=%v", errs)
		}
		settingsEqual(t, errs[0].Scope, "global")
		settingsEqual(t, errs[0].Path, sm.GlobalPath())
		settingsEqual(t, errs[1].Scope, "project")
		settingsEqual(t, errs[1].Path, filepath.Join(ProjectConfigDir(sm.CWD()), "settings.json"))
		settingsEqual(t, sm.DrainErrors(), []SettingsError{})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:257
	t.Run("should skip project settings when project is not trusted", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"global"}`, `{"theme":"project"}`)
		sm = NewSettingsManagerWithProjectTrust(sm.CWD(), sm.AgentDir(), false)
		settingsEqual(t, sm.IsProjectTrusted(), false)
		settingsEqual(t, sm.GetTheme(), "global")
		settingsEqual(t, sm.GetProjectSettings(), Settings{})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:268
	t.Run("should reload project settings after trust changes to true", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"global"}`, `{"theme":"project"}`)
		sm = NewSettingsManagerWithProjectTrust(sm.CWD(), sm.AgentDir(), false)
		sm.SetProjectTrusted(true)
		settingsEqual(t, sm.IsProjectTrusted(), true)
		settingsEqual(t, sm.GetTheme(), "project")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:279
	t.Run("should fail project settings writes when project is not trusted", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{"packages":["npm:existing"]}`)
		sm = NewSettingsManagerWithProjectTrust(sm.CWD(), sm.AgentDir(), false)
		err := sm.SetProjectPackages([]PackageSource{{Source: "npm:new"}})
		if err == nil || !strings.Contains(err.Error(), "Project is not trusted; refusing to write project settings") {
			t.Fatalf("write error=%v", err)
		}
		settingsOK(t, sm.Flush())
		settingsEqual(t, sm.GetProjectSettings(), Settings{})
		assertSettingsFileJSON(t, filepath.Join(ProjectConfigDir(sm.CWD()), "settings.json"), `{"packages":["npm:existing"]}`)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:293
	t.Run("should read default project trust from global settings only", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"defaultProjectTrust":"always"}`, `{"defaultProjectTrust":"never"}`)
		settingsEqual(t, sm.GetDefaultProjectTrust(), "always")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:302
	t.Run("should default invalid project trust settings to ask", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"defaultProjectTrust":"sometimes"}`, `{}`)
		settingsEqual(t, sm.GetDefaultProjectTrust(), "ask")
	})
	for _, tc := range []struct {
		name  string
		write bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:312
		{"should not create .pi folder when only reading project settings", false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:330
		{"should create .pi folder when writing project settings", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, dir := t.TempDir(), t.TempDir()
			writeSettingsFixture(t, filepath.Join(dir, "settings.json"), `{"theme":"dark"}`)
			sm := NewSettingsManager(cwd, dir)
			if _, err := os.Stat(ProjectConfigDir(cwd)); !os.IsNotExist(err) {
				t.Fatalf("reading created project dir: %v", err)
			}
			settingsEqual(t, sm.GetTheme(), "dark")
			if tc.write {
				settingsOK(t, sm.SetProjectPackages([]PackageSource{{Source: "npm:test-pkg"}}))
				settingsOK(t, sm.Flush())
				_, err := os.Stat(filepath.Join(ProjectConfigDir(cwd), "settings.json"))
				settingsOK(t, err)
			}
		})
	}
}

func TestSettingsManagerOriginalValues(t *testing.T) {
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:356
	t.Run("maps explicit values and omits auto values", func(t *testing.T) {
		no, yes := false, true
		disabled, kitty := tui.ImageProtocol(""), tui.ImageProtocol("kitty")
		for _, tc := range []struct {
			input string
			want  tui.CapabilityOverrides
		}{
			{`{"terminal":{"images":false,"trueColor":false,"hyperlinks":false}}`, tui.CapabilityOverrides{Images: &disabled, TrueColor: &no, Hyperlinks: &no}},
			{`{"terminal":{"images":"kitty","trueColor":true,"hyperlinks":true}}`, tui.CapabilityOverrides{Images: &kitty, TrueColor: &yes, Hyperlinks: &yes}},
			{`{"terminal":{"images":"auto","trueColor":"auto","hyperlinks":"auto"}}`, tui.CapabilityOverrides{}},
		} {
			settingsEqual(t, memorySettingsJSON(t, tc.input).GetTerminalCapabilityOverrides(), tc.want)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:375
	t.Run("defaults and overrides agent retry delay cap", func(t *testing.T) {
		settingsEqual(t, memorySettingsJSON(t, `{}`).GetRetrySettings(), RetryConfig{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxDelayMs: 60000})
		settingsEqual(t, memorySettingsJSON(t, `{"retry":{"enabled":true,"maxRetries":10,"baseDelayMs":500,"maxAgentDelayMs":5000}}`).GetRetrySettings(), RetryConfig{Enabled: true, MaxRetries: 10, BaseDelayMs: 500, MaxDelayMs: 5000})
	})
	for _, tc := range []struct {
		name, g, p string
		want       int
		invalid    bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:391
		{"should default to 5 minutes", `{}`, `{}`, 300000, false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:396
		{"should use merged global and project settings", `{"httpIdleTimeoutMs":300000}`, `{"httpIdleTimeoutMs":0}`, 0, false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:405
		{"should reject invalid timeout values", `{"httpIdleTimeoutMs":-1}`, `{}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := writeSettingsLayers(t, tc.g, tc.p)
			got, err := sm.GetHttpIdleTimeoutMs()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "Invalid httpIdleTimeoutMs setting") {
					t.Fatalf("timeout error=%v", err)
				}
			} else {
				settingsOK(t, err)
				settingsEqual(t, got, tc.want)
			}
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:414
	t.Run("defaults to streaming and ignores project settings", func(t *testing.T) {
		for _, tc := range []struct{ g, p, want string }{{`{}`, `{}`, "streaming"}, {`{}`, `{"cacheWarming":"idle"}`, "streaming"}, {`{"cacheWarming":"idle"}`, `{"cacheWarming":"idle"}`, "idle"}, {`{"cacheWarming":"bogus"}`, `{"cacheWarming":"idle"}`, "streaming"}} {
			settingsEqual(t, string(writeSettingsLayers(t, tc.g, tc.p).GetCacheWarmingMode()), tc.want)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:427
	t.Run("persists the mode globally", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{}`)
		settingsOK(t, sm.SetCacheWarmingMode("off"))
		settingsOK(t, sm.Flush())
		settingsEqual(t, string(NewSettingsManager(sm.CWD(), sm.AgentDir()).GetCacheWarmingMode()), "off")
		assertSettingsFileJSON(t, sm.GlobalPath(), `{"cacheWarming":"off"}`)
	})
	// .upstream/v1.0.0/packages/coding-agent/test/settings-manager.test.ts:481
	t.Run("defaults to fullscreen and persists regular mode", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{}`)
		settingsEqual(t, sm.GetTuiMode(), "fullscreen")
		settingsOK(t, sm.SetTuiMode("regular"))
		settingsOK(t, sm.Flush())
		settingsEqual(t, sm.GetTuiMode(), "regular")
		assertSettingsFileJSON(t, sm.GlobalPath(), `{"tuiMode":"regular"}`)
	})
	for _, tc := range []struct{ name, initial string }{
		// .upstream/v1.0.0/packages/coding-agent/test/settings-manager.test.ts:494
		{"falls back to fullscreen for unsupported values", `{"tuiMode":"other"}`},
		// .upstream/v1.0.0/packages/coding-agent/test/settings-manager.test.ts:502
		{"does not recognize the old uiMode setting", `{"uiMode":"regular"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settingsEqual(t, writeSettingsLayers(t, tc.initial, `{}`).GetTuiMode(), "fullscreen")
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:511
	t.Run("validates and persists fullscreen settings", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{}`)
		settingsEqual(t, sm.GetFullscreenExitOutput(), "transcript")
		settingsEqual(t, sm.GetFullscreenScrollbar(), "auto")
		settingsEqual(t, sm.GetFullscreenCopyOnSelect(), true)
		settingsOK(t, sm.SetFullscreenExitOutput("resume-hint"))
		settingsOK(t, sm.SetFullscreenScrollbar("hidden"))
		settingsOK(t, sm.SetFullscreenCopyOnSelect(false))
		settingsOK(t, sm.Flush())
		assertSettingsFileJSON(t, sm.GlobalPath(), `{"fullscreenExitOutput":"resume-hint","fullscreenScrollbar":"hidden","fullscreenCopyOnSelect":false}`)
		writeSettingsFixture(t, sm.GlobalPath(), `{"fullscreenExitOutput":"nothing","fullscreenScrollbar":"sometimes"}`)
		sm = NewSettingsManager(sm.CWD(), sm.AgentDir())
		settingsEqual(t, sm.GetFullscreenExitOutput(), "transcript")
		settingsEqual(t, sm.GetFullscreenScrollbar(), "auto")
		settingsEqual(t, sm.GetFullscreenCopyOnSelect(), true)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:558
	t.Run("should default to 1 and persist binary values", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{}`)
		settingsEqual(t, sm.GetOutputPad(), 1)
		settingsOK(t, sm.SetOutputPad(0))
		settingsOK(t, sm.Flush())
		settingsEqual(t, sm.GetOutputPad(), 0)
		assertSettingsFileJSON(t, sm.GlobalPath(), `{"outputPad":0}`)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:571
	t.Run("should treat unsupported outputPad values as default padding", func(t *testing.T) {
		settingsEqual(t, writeSettingsLayers(t, `{"outputPad":2}`, `{}`).GetOutputPad(), 1)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:581
	t.Run("defaults to streaming and persists rendering modes", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{}`)
		settingsEqual(t, sm.GetMermaidRenderingMode(), "streaming")
		settingsOK(t, sm.SetMermaidRenderingMode("final"))
		settingsOK(t, sm.Flush())
		settingsEqual(t, sm.GetMermaidRenderingMode(), "final")
		assertSettingsFileJSON(t, sm.GlobalPath(), `{"markdown":{"mermaid":"final"}}`)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:594
	t.Run("falls back to streaming for unsupported values", func(t *testing.T) {
		settingsEqual(t, writeSettingsLayers(t, `{"markdown":{"mermaid":"sometimes"}}`, `{}`).GetMermaidRenderingMode(), "streaming")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:602
	t.Run("should load shellCommandPrefix from settings", func(t *testing.T) {
		settingsEqual(t, writeSettingsLayers(t, `{"shellCommandPrefix":"shopt -s expand_aliases"}`, `{}`).GetShellCommandPrefix(), "shopt -s expand_aliases")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:611
	t.Run("should return undefined when shellCommandPrefix is not set", func(t *testing.T) {
		settingsEqual(t, writeSettingsLayers(t, `{"theme":"dark"}`, `{}`).GetShellCommandPrefix(), "")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:620
	t.Run("should preserve shellCommandPrefix when saving unrelated settings", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"shellCommandPrefix":"shopt -s expand_aliases"}`, `{}`)
		settingsOK(t, sm.SetTheme("light"))
		settingsOK(t, sm.Flush())
		assertSettingsFileJSON(t, sm.GlobalPath(), `{"shellCommandPrefix":"shopt -s expand_aliases","theme":"light"}`)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:635
	t.Run("loads global defaults and lets project settings replace them", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"defaultTools":["read","bash"]}`, `{}`)
		settingsEqual(t, sm.GetDefaultTools(), []string{"read", "bash"})
		writeSettingsFixture(t, filepath.Join(ProjectConfigDir(sm.CWD()), "settings.json"), `{"defaultTools":["grep"]}`)
		settingsEqual(t, NewSettingsManager(sm.CWD(), sm.AgentDir()).GetDefaultTools(), []string{"grep"})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:645
	t.Run("preserves an empty tool list", func(t *testing.T) {
		settingsEqual(t, memorySettingsJSON(t, `{"defaultTools":[]}`).GetDefaultTools(), []string{})
		settingsEqual(t, memorySettingsJSON(t, `{}`).GetDefaultTools(), []string(nil))
	})
}

// Pi settings-manager.ts:getOutputPad uses strict equality with zero, not clamping.
// Extends settings-manager.test.ts:533 to the lower invalid boundary and decoded JSON types.
func TestSettingsOutputPadStrictZero(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{`{}`, 1},
		{`{"outputPad":0}`, 0},
		{`{"outputPad":1}`, 1},
		{`{"outputPad":2}`, 1},
		{`{"outputPad":-1}`, 1},
		{`{"outputPad":null}`, 1},
		{`{"outputPad":false}`, 1},
		{`{"outputPad":"0"}`, 1},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			sm := writeSettingsLayers(t, tc.raw, `{}`)
			settingsEqual(t, sm.GetOutputPad(), tc.want)
			settingsEqual(t, sm.Get().GetOutputPad(), tc.want)
		})
	}
}

// Pi interactive-mode.ts:handleReloadCommand reads getOutputPad before rebuilding the transcript.
func TestSettingsOutputPadReloadRejectsNegative(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	dir := t.TempDir()
	sm := writeSettingsLayers(t, `{"outputPad":0}`, `{}`)
	session := NewSession("padding", dir)
	_, err := session.AppendMessage(assistantMsg("", ai.TextContent{Text: "answer"}))
	settingsOK(t, err)
	m := reloadTestMode(InteractiveOptions{
		SettingsManager: sm,
		Settings:        sm.Get(),
		SessionHandle:   &recordingCompactHandle{inner: session},
	})
	m.outputPad = 0
	writeSettingsFixture(t, sm.GlobalPath(), `{"outputPad":-1}`)
	settingsOK(t, reloadHandler(m.buildSlashContext(t.Context())))
	settingsEqual(t, m.outputPad, 1)
	if len(m.assistantBlocks) != 1 {
		t.Fatalf("rebuilt blocks = %d, want the one persisted assistant", len(m.assistantBlocks))
	}
	// AssistantMessageComponent.render prefixes OSC markers to the already padded Markdown line.
	want := "\x1b]133;B\x07\x1b]133;C\x07 answer" + strings.Repeat(" ", 100-1-len("answer"))
	settingsEqual(t, m.assistantBlocks[0].Render(100), []string{"\x1b]133;A\x07", want})
}

func TestSettingsManagerOriginalPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	settingsOK(t, err)
	for _, tc := range []struct {
		name, g, p, want string
		shell            bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:694
		{"getSessionDir/should return undefined when not set", `{"theme":"dark"}`, `{}`, "", false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:700
		{"should return global sessionDir", `{"sessionDir":"/tmp/sessions"}`, `{}`, "/tmp/sessions", false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:706
		{"should return project sessionDir overriding global", `{"sessionDir":"/global/sessions"}`, `{"sessionDir":"./sessions"}`, "./sessions", false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:713
		{"should expand ~ in sessionDir", `{"sessionDir":"~/sessions"}`, `{}`, filepath.Join(home, "sessions"), false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:694
		{"getShellPath/should return undefined when not set", `{"theme":"dark"}`, `{}`, "", true},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:727
		{"should return an absolute shellPath unchanged", `{"shellPath":"/bin/zsh"}`, `{}`, "/bin/zsh", true},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:733
		{"should expand ~ in shellPath", `{"shellPath":"~/.local/bin/agent-shell-sandbox"}`, `{}`, filepath.Join(home, ".local/bin/agent-shell-sandbox"), true},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:742
		{"should expand a bare ~ in shellPath", `{"shellPath":"~"}`, `{}`, home, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := writeSettingsLayers(t, tc.g, tc.p)
			get := sm.GetSessionDir
			if tc.shell {
				get = sm.GetShellPath
			}
			got, err := get()
			settingsOK(t, err)
			settingsEqual(t, got, tc.want)
		})
	}
}

func TestInMemorySettingsClonesAndTrustScopes(t *testing.T) {
	t.Chdir(t.TempDir())
	sm := memorySettingsJSON(t, `{"defaultTools":[],"extensions":[],"skills":[],"prompts":[],"themes":[],"enabledModels":[],"npmCommand":[]}`)
	for _, path := range []string{"defaultTools", "extensions", "skills", "prompts", "themes", "enabledModels", "npmCommand"} {
		data, err := json.Marshal(sm.GetGlobalSettings())
		settingsOK(t, err)
		var values map[string]json.RawMessage
		settingsOK(t, json.Unmarshal(data, &values))
		if string(values[path]) != "[]" {
			t.Errorf("%s=%s, want explicit []; settings=%s", path, values[path], data)
		}
	}
	settingsOK(t, sm.SetProjectPackages([]PackageSource{{Source: "npm:project"}}))
	sm.SetProjectTrusted(false)
	settingsEqual(t, sm.GetProjectSettings(), Settings{})
	if err := sm.SetProjectPackages(nil); err == nil {
		t.Fatal("untrusted memory write succeeded")
	}
	sm.SetProjectTrusted(true)
	settingsEqual(t, sm.GetProjectSettings().Packages, []PackageSource{{Source: "npm:project"}})
	for _, get := range []func() []string{memorySettingsJSON(t, `{}`).GetExtensionPaths, memorySettingsJSON(t, `{}`).GetSkillPaths, memorySettingsJSON(t, `{}`).GetPromptTemplatePaths, memorySettingsJSON(t, `{}`).GetThemePaths} {
		settingsEqual(t, get(), []string{})
	}
	sm.ApplyOverrides(Settings{Theme: "transient"})
	sm.Reload()
	settingsEqual(t, sm.GetTheme(), "")
}
