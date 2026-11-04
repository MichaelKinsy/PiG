package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestStartupKeybindHintsShowsCoreBindings(t *testing.T) {
	got := StartupKeybindHints(DefaultKeybindingsManager())
	for _, want := range []string{"interrupt", "clear/exit", "commands", "bash", "more"} {
		if !strings.Contains(got, want) {
			t.Fatalf("startup hints missing %q:\n%s", want, got)
		}
	}
	// The default interrupt/clear/exit/expand keys are surfaced.
	// Upstream keyHint uses non-capitalized keyText (keybinding-hints.ts).
	for _, key := range []string{"escape", "ctrl+c", "ctrl+d", "ctrl+o"} {
		if !strings.Contains(got, key) {
			t.Fatalf("startup hints missing key %q:\n%s", key, got)
		}
	}
}

func TestStartupKeybindHintsNilManagerUsesDefaults(t *testing.T) {
	if StartupKeybindHints(nil) != StartupKeybindHints(DefaultKeybindingsManager()) {
		t.Fatal("nil manager should fall back to defaults")
	}
}

// interactive-mode.ts:1056 joins the compact hints with theme.fg("muted", " · "), which closes only the foreground.
func TestStartupKeybindHintsSeparatorIsTheMutedForeground(t *testing.T) {
	got := StartupKeybindHints(DefaultKeybindingsManager())
	separator := tui.ActiveTheme().Fg("muted", " · ")
	if strings.Count(got, separator) != 4 {
		t.Fatalf("separator %q appears %d times, want 4 in %q", separator, strings.Count(got, separator), got)
	}
}

// interactive-mode.ts getModelScopeNotice: the main-screen init() prints `Model scope: id[:thinking], ...` in dim, with the cycle key in muted inside it, only when models are scoped and the startup details are shown.
func TestModelScopeLineMatchesPiInit(t *testing.T) {
	model := func(id string) *ai.Model { return &ai.Model{ID: id} }
	handle := &recordingCompactHandle{scopedModels: []extension.ScopedModel{{Model: model("a")}, {Model: model("b"), ThinkingLevel: "high"}}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Model: model("a"), SessionHandle: handle, Verbose: true})
	theme := tui.ActiveTheme()
	want := theme.Fg("dim", "Model scope: a, b:high"+theme.Fg("muted", " (Ctrl+P to cycle)"))
	if got := m.getModelScopeNotice(); got != want {
		t.Errorf("getModelScopeNotice = %q, want %q", got, want)
	}
	m.opts.Verbose = false
	m.opts.Settings = Settings{QuietStartup: QuietStartupTrue}
	if got := m.getModelScopeNotice(); got != "" {
		t.Errorf("quietStartup line = %q, want none", got)
	}
	handle.scopedModels = nil
	m.opts.Verbose = true
	if got := m.getModelScopeNotice(); got != "" {
		t.Errorf("unscoped line = %q, want none", got)
	}
}

// interactive-mode-model-scope.test.ts "renders the model scope inside the fullscreen header instead of printing it": the notice is a themed header line followed by a spacer, rendered at width 120.
func TestFullscreenModelScopeNoticeRendersInHeader(t *testing.T) {
	model := func(id string) *ai.Model { return &ai.Model{ID: id} }
	handle := &recordingCompactHandle{scopedModels: []extension.ScopedModel{{Model: model("model-a"), ThinkingLevel: "high"}, {Model: model("model-b")}}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Model: model("model-a"), SessionHandle: handle, Verbose: true})
	header := tui.NewContainer()
	m.showFullscreenModelScopeNotice(header)
	if got := len(header.Children()); got != 2 {
		t.Fatalf("header children = %d, want the notice and a spacer", got)
	}
	rendered := stripANSI(strings.Join(header.Render(120), "\n"))
	if !strings.Contains(rendered, "Model scope: model-a:high, model-b (Ctrl+P to cycle)") {
		t.Errorf("header = %q, want the model scope notice", rendered)
	}
}

// interactive-mode-model-scope.test.ts "adds nothing when startup details are hidden".
func TestFullscreenModelScopeNoticeHiddenWithStartupDetails(t *testing.T) {
	model := func(id string) *ai.Model { return &ai.Model{ID: id} }
	handle := &recordingCompactHandle{scopedModels: []extension.ScopedModel{{Model: model("model-a"), ThinkingLevel: "high"}, {Model: model("model-b")}}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Model: model("model-a"), SessionHandle: handle, Settings: Settings{QuietStartup: QuietStartupTrue}})
	header := tui.NewContainer()
	m.showFullscreenModelScopeNotice(header)
	if got := len(header.Children()); got != 0 {
		t.Errorf("header children = %d, want none", got)
	}
}
