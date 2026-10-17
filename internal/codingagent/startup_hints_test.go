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

// interactive-mode.ts:950-963: init() prints `Model scope: id[:thinking], ...` in dim, with the cycle key in muted inside it, only when models are scoped and the startup details are shown.
func TestModelScopeLineMatchesPiInit(t *testing.T) {
	model := func(id string) *ai.Model { return &ai.Model{ID: id} }
	handle := &recordingCompactHandle{scopedModels: []extension.ScopedModel{{Model: model("a")}, {Model: model("b"), ThinkingLevel: "high"}}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Model: model("a"), SessionHandle: handle, Verbose: true})
	theme := tui.ActiveTheme()
	want := theme.Fg("dim", "Model scope: a, b:high"+theme.Fg("muted", " (Ctrl+P to cycle)"))
	if got := m.modelScopeLine(); got != want {
		t.Errorf("modelScopeLine = %q, want %q", got, want)
	}
	m.opts.Verbose = false
	m.opts.Settings = Settings{QuietStartup: QuietStartupTrue}
	if got := m.modelScopeLine(); got != "" {
		t.Errorf("quietStartup line = %q, want none", got)
	}
	handle.scopedModels = nil
	m.opts.Verbose = true
	if got := m.modelScopeLine(); got != "" {
		t.Errorf("unscoped line = %q, want none", got)
	}
}
