package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/settings-manager.test.ts case by case. Each
// subtest name is the upstream `describe > it` name. Go writes are synchronous,
// so upstream's `await manager.flush()` is Flush() (a no-op that returns nil).

type upstreamSettingsFixture struct {
	t                    *testing.T
	agentDir, projectDir string
}

func newUpstreamSettingsFixture(t *testing.T) *upstreamSettingsFixture {
	t.Helper()
	root := t.TempDir()
	f := &upstreamSettingsFixture{t: t, agentDir: filepath.Join(root, "agent"), projectDir: filepath.Join(root, "project")}
	if err := os.MkdirAll(f.agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.projectDir, CONFIG_DIR_NAME), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *upstreamSettingsFixture) global() string { return filepath.Join(f.agentDir, "settings.json") }
func (f *upstreamSettingsFixture) project() string {
	return filepath.Join(f.projectDir, CONFIG_DIR_NAME, "settings.json")
}
func (f *upstreamSettingsFixture) create() *SettingsManager {
	return NewSettingsManager(f.projectDir, f.agentDir)
}

func (f *upstreamSettingsFixture) write(path string, value any) {
	f.t.Helper()
	data, ok := value.(string)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			f.t.Fatal(err)
		}
		data = string(encoded)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *upstreamSettingsFixture) readJSON(path string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamSettingsManager(t *testing.T) {
	t.Run("preserves externally added settings", func(t *testing.T) {
		t.Run("should preserve enabledModels when changing thinking level", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark", "defaultModel": "claude-sonnet"})
			manager := f.create()
			current := f.readJSON(f.global())
			current["enabledModels"] = []string{"claude-opus-4-5", "gpt-5.2-codex"}
			f.write(f.global(), current)
			mustNoError(t, manager.SetDefaultThinkingLevel("high"))
			mustNoError(t, manager.Flush())
			saved := f.readJSON(f.global())
			if !reflect.DeepEqual(saved["enabledModels"], []any{"claude-opus-4-5", "gpt-5.2-codex"}) || saved["defaultThinkingLevel"] != "high" || saved["theme"] != "dark" || saved["defaultModel"] != "claude-sonnet" {
				t.Fatalf("saved = %#v", saved)
			}
		})
		t.Run("should preserve custom settings when changing theme", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"defaultModel": "claude-sonnet"})
			manager := f.create()
			current := f.readJSON(f.global())
			current["shellPath"] = "/bin/zsh"
			current["extensions"] = []string{"/path/to/extension.ts"}
			f.write(f.global(), current)
			mustNoError(t, manager.SetTheme("light"))
			mustNoError(t, manager.Flush())
			saved := f.readJSON(f.global())
			if saved["shellPath"] != "/bin/zsh" || !reflect.DeepEqual(saved["extensions"], []any{"/path/to/extension.ts"}) || saved["theme"] != "light" {
				t.Fatalf("saved = %#v", saved)
			}
		})
		t.Run("should let in-memory changes override file changes for same key", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			manager := f.create()
			current := f.readJSON(f.global())
			current["defaultThinkingLevel"] = "low"
			f.write(f.global(), current)
			mustNoError(t, manager.SetDefaultThinkingLevel("high"))
			mustNoError(t, manager.Flush())
			if got := f.readJSON(f.global())["defaultThinkingLevel"]; got != "high" {
				t.Fatalf("defaultThinkingLevel = %v", got)
			}
		})
	})

	t.Run("packages migration", func(t *testing.T) {
		t.Run("should keep local-only extensions in extensions array", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"extensions": []string{"/local/ext.ts", "./relative/ext.ts"}})
			manager := f.create()
			if got := manager.GetPackages(); len(got) != 0 {
				t.Fatalf("packages = %#v", got)
			}
			if got := manager.GetExtensionPaths(); !reflect.DeepEqual(got, []string{"/local/ext.ts", "./relative/ext.ts"}) {
				t.Fatalf("extensions = %#v", got)
			}
		})
		t.Run("should handle packages with filtering objects", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"packages": []any{"npm:simple-pkg", map[string]any{"source": "npm:shitty-extensions", "extensions": []string{"extensions/oracle.ts"}, "skills": []string{}}}})
			packages := f.create().GetPackages()
			if len(packages) != 2 || packages[0].Source != "npm:simple-pkg" || packages[0].WasObject || packages[0].Extensions != nil || packages[0].Skills != nil || packages[0].Prompts != nil || packages[0].Themes != nil || packages[0].Autoload != nil {
				t.Fatalf("packages = %#v", packages)
			}
			second := packages[1]
			if second.Source != "npm:shitty-extensions" || !reflect.DeepEqual(second.Extensions, []string{"extensions/oracle.ts"}) || second.Skills == nil || len(second.Skills) != 0 || second.Prompts != nil || second.Themes != nil || second.Autoload != nil || !second.WasObject {
				t.Fatalf("second package = %#v", second)
			}
		})
	})

	t.Run("reload", func(t *testing.T) {
		t.Run("should reload global settings from disk", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark", "extensions": []string{"/before.ts"}})
			manager := f.create()
			f.write(f.global(), map[string]any{"theme": "light", "extensions": []string{"/after.ts"}, "defaultModel": "claude-sonnet"})
			manager.Reload()
			if manager.GetTheme() != "light" || !reflect.DeepEqual(manager.GetExtensionPaths(), []string{"/after.ts"}) || manager.GetDefaultModel() != "claude-sonnet" {
				t.Fatalf("theme=%q extensions=%v model=%q", manager.GetTheme(), manager.GetExtensionPaths(), manager.GetDefaultModel())
			}
		})
		t.Run("should keep previous settings and report the file path when the file is invalid", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			manager := f.create()
			f.write(f.global(), "{ invalid json")
			manager.Reload()
			if manager.GetTheme() != "dark" {
				t.Fatalf("theme = %q, want the previous settings kept", manager.GetTheme())
			}
			errs := manager.DrainErrors()
			if len(errs) != 1 || errs[0].Scope != "global" || errs[0].Path != f.global() {
				t.Fatalf("errors = %#v", errs)
			}
		})
	})

	t.Run("theme setting", func(t *testing.T) {
		t.Run("stores slash-separated automatic theme settings separately from fixed theme names", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "light/dark"})
			manager := f.create()
			if manager.GetTheme() != "" {
				t.Fatalf("GetTheme = %q, want undefined", manager.GetTheme())
			}
			if setting := manager.GetThemeSetting(); setting == nil || *setting != "light/dark" {
				t.Fatalf("GetThemeSetting = %v", setting)
			}
			mustNoError(t, manager.SetTheme("solarized-light/tokyo-night"))
			mustNoError(t, manager.Flush())
			if got := f.readJSON(f.global())["theme"]; got != "solarized-light/tokyo-night" {
				t.Fatalf("theme = %v", got)
			}
		})
	})

	t.Run("error tracking", func(t *testing.T) {
		t.Run("should collect and clear load errors via drainErrors", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), "{ invalid global json")
			f.write(f.project(), "{ invalid project json")
			manager := f.create()
			errs := manager.DrainErrors()
			if len(errs) != 2 || errs[0].Scope != "global" || errs[0].Path != f.global() || errs[1].Scope != "project" || errs[1].Path != f.project() {
				t.Fatalf("errors = %#v", errs)
			}
			if again := manager.DrainErrors(); len(again) != 0 {
				t.Fatalf("second drain = %#v", again)
			}
		})
	})

	t.Run("project trust", func(t *testing.T) {
		t.Run("should skip project settings when project is not trusted", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "global"})
			f.write(f.project(), map[string]any{"theme": "project"})
			manager := NewSettingsManagerWithProjectTrust(f.projectDir, f.agentDir, false)
			if manager.IsProjectTrusted() || manager.GetTheme() != "global" || !reflect.DeepEqual(manager.GetProjectSettings(), Settings{}) {
				t.Fatalf("trusted=%v theme=%q project=%#v", manager.IsProjectTrusted(), manager.GetTheme(), manager.GetProjectSettings())
			}
		})
		t.Run("should reload project settings after trust changes to true", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "global"})
			f.write(f.project(), map[string]any{"theme": "project"})
			manager := NewSettingsManagerWithProjectTrust(f.projectDir, f.agentDir, false)
			manager.SetProjectTrusted(true)
			if !manager.IsProjectTrusted() || manager.GetTheme() != "project" {
				t.Fatalf("trusted=%v theme=%q", manager.IsProjectTrusted(), manager.GetTheme())
			}
		})
		t.Run("should fail project settings writes when project is not trusted", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.project(), map[string]any{"packages": []string{"npm:existing"}})
			manager := NewSettingsManagerWithProjectTrust(f.projectDir, f.agentDir, false)
			err := manager.SetProjectPackages([]PackageSource{{Source: "npm:new"}})
			if err == nil || !strings.Contains(err.Error(), "Project is not trusted; refusing to write project settings") {
				t.Fatalf("err = %v", err)
			}
			mustNoError(t, manager.Flush())
			if !reflect.DeepEqual(manager.GetProjectSettings(), Settings{}) {
				t.Fatalf("project settings = %#v", manager.GetProjectSettings())
			}
			if got := f.readJSON(f.project()); !reflect.DeepEqual(got, map[string]any{"packages": []any{"npm:existing"}}) {
				t.Fatalf("project file = %#v", got)
			}
		})
		t.Run("should read default project trust from global settings only", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"defaultProjectTrust": "always"})
			f.write(f.project(), map[string]any{"defaultProjectTrust": "never"})
			if got := f.create().GetDefaultProjectTrust(); got != "always" {
				t.Fatalf("GetDefaultProjectTrust = %q", got)
			}
		})
		t.Run("should default invalid project trust settings to ask", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"defaultProjectTrust": "sometimes"})
			if got := f.create().GetDefaultProjectTrust(); got != "ask" {
				t.Fatalf("GetDefaultProjectTrust = %q", got)
			}
		})
	})

	t.Run("project settings directory creation", func(t *testing.T) {
		t.Run("should not create .pi folder when only reading project settings", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			mustNoError(t, os.RemoveAll(filepath.Join(f.projectDir, CONFIG_DIR_NAME)))
			manager := f.create()
			if _, err := os.Stat(filepath.Join(f.projectDir, CONFIG_DIR_NAME)); !os.IsNotExist(err) {
				t.Fatalf("config dir created by a read: %v", err)
			}
			if manager.GetTheme() != "dark" {
				t.Fatalf("theme = %q", manager.GetTheme())
			}
		})
		t.Run("should create .pi folder when writing project settings", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			mustNoError(t, os.RemoveAll(filepath.Join(f.projectDir, CONFIG_DIR_NAME)))
			manager := f.create()
			if _, err := os.Stat(filepath.Join(f.projectDir, CONFIG_DIR_NAME)); !os.IsNotExist(err) {
				t.Fatalf("config dir exists before write: %v", err)
			}
			mustNoError(t, manager.SetProjectPackages([]PackageSource{{Source: "npm:test-pkg", WasObject: true}}))
			mustNoError(t, manager.Flush())
			if _, err := os.Stat(f.project()); err != nil {
				t.Fatalf("project settings file not created: %v", err)
			}
		})
	})

	t.Run("terminal capability overrides", func(t *testing.T) {
		t.Run("maps explicit values and omits auto values", func(t *testing.T) {
			overrides := func(images, trueColor, hyperlinks string) tui.CapabilityOverrides {
				var settings Settings
				mustNoError(t, json.Unmarshal([]byte(`{"terminal":{"images":`+images+`,"trueColor":`+trueColor+`,"hyperlinks":`+hyperlinks+`}}`), &settings))
				return NewInMemorySettingsManager(settings).GetTerminalCapabilityOverrides()
			}
			// images: false maps to a null override (disabled); trueColor/hyperlinks false map to false.
			got := overrides("false", "false", "false")
			if got.Images == nil || *got.Images != "" || got.TrueColor == nil || *got.TrueColor || got.Hyperlinks == nil || *got.Hyperlinks {
				t.Fatalf("false overrides = %#v", got)
			}
			got = overrides(`"kitty"`, "true", "true")
			if got.Images == nil || *got.Images != tui.ImageProtocolKitty || got.TrueColor == nil || !*got.TrueColor || got.Hyperlinks == nil || !*got.Hyperlinks {
				t.Fatalf("explicit overrides = %#v", got)
			}
			if got = overrides(`"auto"`, `"auto"`, `"auto"`); got.Images != nil || got.TrueColor != nil || got.Hyperlinks != nil {
				t.Fatalf("auto overrides = %#v", got)
			}
		})
	})

	t.Run("retry settings", func(t *testing.T) {
		t.Run("defaults and overrides agent retry delay cap", func(t *testing.T) {
			if got := NewInMemorySettingsManager(Settings{}).GetRetrySettings(); got != (RetryConfig{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxDelayMs: 60000}) {
				t.Fatalf("defaults = %#v", got)
			}
			enabled, maxRetries, base, maxDelay := true, 10, 500, 5000
			custom := NewInMemorySettingsManager(Settings{Retry: &RetrySettingsJSON{Enabled: &enabled, MaxRetries: &maxRetries, BaseDelayMs: &base, MaxAgentDelayMs: &maxDelay}})
			if got := custom.GetRetrySettings(); got != (RetryConfig{Enabled: true, MaxRetries: 10, BaseDelayMs: 500, MaxDelayMs: 5000}) {
				t.Fatalf("overrides = %#v", got)
			}
		})
	})

	t.Run("httpIdleTimeoutMs", func(t *testing.T) {
		t.Run("should default to 5 minutes", func(t *testing.T) {
			got, err := newUpstreamSettingsFixture(t).create().GetHttpIdleTimeoutMs()
			if err != nil || got != 300000 {
				t.Fatalf("got %d, %v", got, err)
			}
		})
		t.Run("should use merged global and project settings", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"httpIdleTimeoutMs": 300000})
			f.write(f.project(), map[string]any{"httpIdleTimeoutMs": 0})
			got, err := f.create().GetHttpIdleTimeoutMs()
			if err != nil || got != 0 {
				t.Fatalf("got %d, %v", got, err)
			}
		})
		t.Run("should reject invalid timeout values", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"httpIdleTimeoutMs": -1})
			if _, err := f.create().GetHttpIdleTimeoutMs(); err == nil || !strings.Contains(err.Error(), "Invalid httpIdleTimeoutMs setting") {
				t.Fatalf("err = %v", err)
			}
		})
	})

	t.Run("cacheWarming", func(t *testing.T) {
		t.Run("defaults to streaming and ignores project settings", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			if got := f.create().GetCacheWarmingMode(); got != "streaming" {
				t.Fatalf("default = %q", got)
			}
			f.write(f.project(), map[string]any{"cacheWarming": "idle"})
			if got := f.create().GetCacheWarmingMode(); got != "streaming" {
				t.Fatalf("project = %q", got)
			}
			f.write(f.global(), map[string]any{"cacheWarming": "idle"})
			if got := f.create().GetCacheWarmingMode(); got != "idle" {
				t.Fatalf("global = %q", got)
			}
			f.write(f.global(), map[string]any{"cacheWarming": "bogus"})
			if got := f.create().GetCacheWarmingMode(); got != "streaming" {
				t.Fatalf("bogus = %q", got)
			}
		})
		t.Run("persists the mode globally", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			manager := f.create()
			mustNoError(t, manager.SetCacheWarmingMode("off"))
			mustNoError(t, manager.Flush())
			if got := f.create().GetCacheWarmingMode(); got != "off" {
				t.Fatalf("reloaded = %q", got)
			}
			if got := f.readJSON(f.global()); !reflect.DeepEqual(got, map[string]any{"cacheWarming": "off"}) {
				t.Fatalf("file = %#v", got)
			}
		})
	})

	t.Run("externalEditor", func(t *testing.T) {
		setEditorEnv := func(t *testing.T, visual, editor string) {
			t.Helper()
			for name, value := range map[string]string{"VISUAL": visual, "EDITOR": editor} {
				if value == "" {
					t.Setenv(name, "")
					mustNoError(t, os.Unsetenv(name))
				} else {
					t.Setenv(name, value)
				}
			}
		}
		t.Run("should resolve editor commands by precedence", func(t *testing.T) {
			setEditorEnv(t, "vim", "nano")
			if got := NewInMemorySettingsManager(Settings{ExternalEditor: "code --wait"}).GetExternalEditorCommand(); got != "code --wait" {
				t.Fatalf("configured = %q", got)
			}
			if got := NewInMemorySettingsManager(Settings{}).GetExternalEditorCommand(); got != "vim" {
				t.Fatalf("VISUAL = %q", got)
			}
			setEditorEnv(t, "", "emacs")
			if got := NewInMemorySettingsManager(Settings{}).GetExternalEditorCommand(); got != "emacs" {
				t.Fatalf("EDITOR = %q", got)
			}
		})
		t.Run("should fall back to platform defaults", func(t *testing.T) {
			setEditorEnv(t, "", "")
			// Node overrides process.platform; Go's GOOS is fixed, so the platform is the resolver's parameter.
			// The production method passes runtime.GOOS, which the last row checks on the host.
			for _, tc := range []struct{ goos, want string }{{"windows", "notepad"}, {"darwin", "nano"}, {"linux", "nano"}} {
				if got := resolveExternalEditorCommand("", "", "", tc.goos); got != tc.want {
					t.Errorf("%s = %q, want %q", tc.goos, got, tc.want)
				}
			}
			want := resolveExternalEditorCommand("", "", "", runtime.GOOS)
			if got := NewInMemorySettingsManager(Settings{}).GetExternalEditorCommand(); got != want {
				t.Errorf("host = %q, want %q", got, want)
			}
		})
	})

	t.Run("TUI mode", func(t *testing.T) {
		t.Run("defaults to regular and persists fullscreen mode", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			manager := f.create()
			if manager.GetTuiMode() != "regular" {
				t.Fatalf("default = %q", manager.GetTuiMode())
			}
			mustNoError(t, manager.SetTuiMode("fullscreen"))
			mustNoError(t, manager.Flush())
			if manager.GetTuiMode() != "fullscreen" || f.readJSON(f.global())["tuiMode"] != "fullscreen" {
				t.Fatalf("mode=%q file=%#v", manager.GetTuiMode(), f.readJSON(f.global()))
			}
		})
		t.Run("falls back to regular for unsupported values", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"tuiMode": "other"})
			if got := f.create().GetTuiMode(); got != "regular" {
				t.Fatalf("mode = %q", got)
			}
		})
		t.Run("does not recognize the old uiMode setting", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"uiMode": "fullscreen"})
			if got := f.create().GetTuiMode(); got != "regular" {
				t.Fatalf("mode = %q", got)
			}
		})
	})

	t.Run("validates and persists fullscreen settings", func(t *testing.T) {
		f := newUpstreamSettingsFixture(t)
		manager := f.create()
		if manager.GetFullscreenExitOutput() != "transcript" || manager.GetFullscreenScrollbar() != "auto" || !manager.GetFullscreenCopyOnSelect() {
			t.Fatalf("defaults = %q %q %v", manager.GetFullscreenExitOutput(), manager.GetFullscreenScrollbar(), manager.GetFullscreenCopyOnSelect())
		}
		mustNoError(t, manager.SetFullscreenExitOutput("resume-hint"))
		mustNoError(t, manager.SetFullscreenScrollbar("hidden"))
		mustNoError(t, manager.SetFullscreenCopyOnSelect(false))
		mustNoError(t, manager.Flush())
		saved := f.readJSON(f.global())
		if saved["fullscreenExitOutput"] != "resume-hint" || saved["fullscreenScrollbar"] != "hidden" || saved["fullscreenCopyOnSelect"] != false {
			t.Fatalf("saved = %#v", saved)
		}
		f.write(f.global(), map[string]any{"fullscreenExitOutput": "nothing", "fullscreenScrollbar": "sometimes"})
		reloaded := f.create()
		if reloaded.GetFullscreenExitOutput() != "transcript" || reloaded.GetFullscreenScrollbar() != "auto" || !reloaded.GetFullscreenCopyOnSelect() {
			t.Fatalf("invalid values = %q %q %v", reloaded.GetFullscreenExitOutput(), reloaded.GetFullscreenScrollbar(), reloaded.GetFullscreenCopyOnSelect())
		}
	})

	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:537 (#9758)
	t.Run("persists fullscreen wheel scroll lines", func(t *testing.T) {
		f := newUpstreamSettingsFixture(t)
		manager := f.create()
		if got := manager.GetFullscreenWheelScrollLines(); got != (WheelScrollLines{Auto: true}) {
			t.Fatalf("default = %#v, want auto", got)
		}

		mustNoError(t, manager.SetFullscreenWheelScrollLines(WheelScrollLines{Lines: 3}))
		mustNoError(t, manager.Flush())
		if saved := f.readJSON(f.global())["fullscreenWheelScrollLines"]; saved != float64(3) {
			t.Fatalf("saved = %#v, want 3", saved)
		}

		for _, tc := range []struct {
			value any
			want  WheelScrollLines
		}{
			{7.9, WheelScrollLines{Lines: 7}},
			{0, WheelScrollLines{Lines: 1}},
			{1000, WheelScrollLines{Lines: 100}},
			{"fast", WheelScrollLines{Auto: true}},
			{nil, WheelScrollLines{Auto: true}},
		} {
			f.write(f.global(), map[string]any{"fullscreenWheelScrollLines": tc.value})
			if got := f.create().GetFullscreenWheelScrollLines(); got != tc.want {
				t.Fatalf("value %#v: got %#v, want %#v", tc.value, got, tc.want)
			}
		}
	})

	t.Run("outputPad", func(t *testing.T) {
		t.Run("should default to 1 and persist binary values", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			manager := f.create()
			if manager.GetOutputPad() != 1 {
				t.Fatalf("default = %d", manager.GetOutputPad())
			}
			mustNoError(t, manager.SetOutputPad(0))
			mustNoError(t, manager.Flush())
			if manager.GetOutputPad() != 0 || f.readJSON(f.global())["outputPad"] != float64(0) {
				t.Fatalf("pad=%d file=%#v", manager.GetOutputPad(), f.readJSON(f.global()))
			}
		})
		t.Run("should treat unsupported outputPad values as default padding", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"outputPad": 2})
			if got := f.create().GetOutputPad(); got != 1 {
				t.Fatalf("pad = %d", got)
			}
		})
	})

	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:114
	t.Run("deviceId", func(t *testing.T) {
		t.Run("creates one global device ID and reuses it in later processes", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			f.write(f.project(), map[string]any{"deviceId": "project-device"})
			first := f.create()

			deviceID := first.GetOrCreateDeviceID()
			mustNoError(t, first.Flush())

			if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(deviceID) {
				t.Fatalf("device ID %q is not a UUID", deviceID)
			}
			if got := first.GetOrCreateDeviceID(); got != deviceID {
				t.Fatalf("second call = %q, want %q", got, deviceID)
			}
			if got := f.create().GetOrCreateDeviceID(); got != deviceID {
				t.Fatalf("later process = %q, want %q", got, deviceID)
			}
			if saved := f.readJSON(f.global()); !reflect.DeepEqual(saved, map[string]any{"theme": "dark", "deviceId": deviceID}) {
				t.Fatalf("saved = %#v", saved)
			}
		})
	})

	t.Run("markdown.mermaid", func(t *testing.T) {
		t.Run("defaults to streaming and persists rendering modes", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			manager := f.create()
			if manager.GetMermaidRenderingMode() != "streaming" {
				t.Fatalf("default = %q", manager.GetMermaidRenderingMode())
			}
			mustNoError(t, manager.SetMermaidRenderingMode("final"))
			mustNoError(t, manager.Flush())
			markdown, _ := f.readJSON(f.global())["markdown"].(map[string]any)
			if manager.GetMermaidRenderingMode() != "final" || markdown["mermaid"] != "final" {
				t.Fatalf("mode=%q markdown=%#v", manager.GetMermaidRenderingMode(), markdown)
			}
		})
		t.Run("falls back to streaming for unsupported values", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"markdown": map[string]any{"mermaid": "sometimes"}})
			if got := f.create().GetMermaidRenderingMode(); got != "streaming" {
				t.Fatalf("mode = %q", got)
			}
		})
	})

	t.Run("shellCommandPrefix", func(t *testing.T) {
		t.Run("should load shellCommandPrefix from settings", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"shellCommandPrefix": "shopt -s expand_aliases"})
			if got := f.create().GetShellCommandPrefix(); got != "shopt -s expand_aliases" {
				t.Fatalf("prefix = %q", got)
			}
		})
		t.Run("should return undefined when shellCommandPrefix is not set", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			if got := f.create().GetShellCommandPrefix(); got != "" {
				t.Fatalf("prefix = %q", got)
			}
		})
		t.Run("should preserve shellCommandPrefix when saving unrelated settings", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"shellCommandPrefix": "shopt -s expand_aliases"})
			manager := f.create()
			mustNoError(t, manager.SetTheme("light"))
			mustNoError(t, manager.Flush())
			saved := f.readJSON(f.global())
			if saved["shellCommandPrefix"] != "shopt -s expand_aliases" || saved["theme"] != "light" {
				t.Fatalf("saved = %#v", saved)
			}
		})
	})

	t.Run("defaultTools", func(t *testing.T) {
		t.Run("loads global defaults and lets project settings replace them", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"defaultTools": []string{"read", "bash"}})
			if got := f.create().GetDefaultTools(); !reflect.DeepEqual(got, []string{"read", "bash"}) {
				t.Fatalf("global = %#v", got)
			}
			f.write(f.project(), map[string]any{"defaultTools": []string{"grep"}})
			if got := f.create().GetDefaultTools(); !reflect.DeepEqual(got, []string{"grep"}) {
				t.Fatalf("project = %#v", got)
			}
		})
		t.Run("preserves an empty tool list", func(t *testing.T) {
			if got := NewInMemorySettingsManager(Settings{DefaultTools: []string{}}).GetDefaultTools(); got == nil || len(got) != 0 {
				t.Fatalf("empty = %#v, want a non-nil empty list", got)
			}
			if got := NewInMemorySettingsManager(Settings{}).GetDefaultTools(); got != nil {
				t.Fatalf("unset = %#v, want nil", got)
			}
		})
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:650
		t.Run("applies +name and -name to the default selection", func(t *testing.T) {
			if got := NewInMemorySettingsManager(Settings{DefaultTools: []string{"+codemode", "-write"}}).GetDefaultTools(); !reflect.DeepEqual(got, []string{"read", "bash", "edit", "codemode"}) {
				t.Fatalf("modifiers = %#v", got)
			}
			if got := NewInMemorySettingsManager(Settings{DefaultTools: []string{"read", "+grep", "+read"}}).GetDefaultTools(); !reflect.DeepEqual(got, []string{"read", "grep"}) {
				t.Fatalf("plain and modifiers = %#v", got)
			}
		})
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:663
		t.Run("layers project modifiers on top of the global selection", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"defaultTools": []string{"read", "bash", "+codemode"}})
			f.write(f.project(), map[string]any{"defaultTools": []string{"-codemode", "+tool_search"}})

			manager := f.create()
			if got := manager.GetDefaultTools(); !reflect.DeepEqual(got, []string{"read", "bash", "tool_search"}) {
				t.Fatalf("layered = %#v", got)
			}

			manager.ApplyOverrides(Settings{DefaultTools: []string{"+codemode"}})
			if got := manager.GetDefaultTools(); !reflect.DeepEqual(got, []string{"read", "bash", "tool_search", "codemode"}) {
				t.Fatalf("overrides = %#v", got)
			}
		})
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:680
		t.Run("applies project modifiers to the built-in defaults without a global setting", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.project(), map[string]any{"defaultTools": []string{"+codemode"}})

			if got := f.create().GetDefaultTools(); !reflect.DeepEqual(got, []string{"read", "bash", "edit", "write", "codemode"}) {
				t.Fatalf("project modifiers = %#v", got)
			}
		})
	})

	t.Run("getSessionDir", func(t *testing.T) {
		home, err := os.UserHomeDir()
		mustNoError(t, err)
		t.Run("should return undefined when not set", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"theme": "dark"})
			if got, err := f.create().GetSessionDir(); err != nil || got != "" {
				t.Fatalf("got %q, %v", got, err)
			}
		})
		t.Run("should return global sessionDir", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"sessionDir": "/tmp/sessions"})
			if got, err := f.create().GetSessionDir(); err != nil || got != "/tmp/sessions" {
				t.Fatalf("got %q, %v", got, err)
			}
		})
		t.Run("should return project sessionDir, overriding global", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"sessionDir": "/global/sessions"})
			f.write(f.project(), map[string]any{"sessionDir": "./sessions"})
			if got, err := f.create().GetSessionDir(); err != nil || got != "./sessions" {
				t.Fatalf("got %q, %v", got, err)
			}
		})
		t.Run("should expand ~ in sessionDir", func(t *testing.T) {
			f := newUpstreamSettingsFixture(t)
			f.write(f.global(), map[string]any{"sessionDir": "~/sessions"})
			if got, err := f.create().GetSessionDir(); err != nil || got != filepath.Join(home, "sessions") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	})

	t.Run("getShellPath", func(t *testing.T) {
		home, err := os.UserHomeDir()
		mustNoError(t, err)
		for _, tc := range []struct {
			name  string
			value any
			want  string
		}{
			{"should return undefined when not set", nil, ""},
			{"should return an absolute shellPath unchanged", "/bin/zsh", "/bin/zsh"},
			{"should expand ~ in shellPath", "~/.local/bin/agent-shell-sandbox", filepath.Join(home, ".local/bin/agent-shell-sandbox")},
			{"should expand a bare ~ in shellPath", "~", home},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newUpstreamSettingsFixture(t)
				if tc.value == nil {
					f.write(f.global(), map[string]any{"theme": "dark"})
				} else {
					f.write(f.global(), map[string]any{"shellPath": tc.value})
				}
				if got, err := f.create().GetShellPath(); err != nil || got != tc.want {
					t.Fatalf("got %q, %v, want %q", got, err, tc.want)
				}
			})
		}
	})
}
