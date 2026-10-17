package tui

import (
	"strings"
	"testing"
)

// extension-selector.ts:92-100 updateList: the selected row is theme.fg("accent", "→ ") + theme.fg("accent", option), every other row is `  ` + theme.fg("text", option); :61-63 the description is theme.fg("text", description).
func TestExtensionSelectorComponentRowsAndDescriptionUseThePiColors(t *testing.T) {
	s := NewExtensionSelectorComponent("Pick", []string{"first", "second"}, nil, nil, ExtensionSelectorOptions{Description: "explain"})
	joined := strings.Join(s.Render(60), "\n")
	theme := ActiveTheme()
	for name, want := range map[string]string{
		"selected row": theme.Fg("accent", "→ ") + theme.Fg("accent", "first"),
		"other row":    "  " + theme.Fg("text", "second"),
		"description":  theme.Fg("text", "explain"),
		"styled title": styledDialogTitle("Pick"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s: %q missing from %q", name, want, joined)
		}
	}
	if _, ok := any(s).(*ExtensionSelectorComponent); !ok {
		t.Fatalf("NewExtensionSelectorComponent returned %T", s)
	}
}

// extension-selector.ts:103-120 handleInput: "\n" confirms like the confirm binding, and the tools-expand binding only calls the callback when there is
// one. Pi's handleInput does not invalidate anything: the TUI renders again after every input. Pig's line cache redraws only a dirty component, so
// HandleInput marks the selector dirty after each key; the last assertion pins that render mechanism, not a Pi rule.
func TestExtensionSelectorComponentHandleInputNewlineExpandAndInvalidate(t *testing.T) {
	// Pi's coding-agent installs its merged app/tui manager before opening dialogs.
	previous := GetKeybindings()
	SetKeybindings(NewTUIKeybindingsManager(map[string][]string{"app.tools.expand": {"ctrl+o"}}))
	t.Cleanup(func() { SetKeybindings(previous) })
	var picked []string
	s := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(o string) { picked = append(picked, o) }, nil)
	s.HandleInput("j")
	// With the confirm binding moved to another key, "\n" still confirms: handleInput compares it directly (extension-selector.ts:111).
	SetKeybindings(NewTUIKeybindingsManager(map[string][]string{"app.tools.expand": {"ctrl+o"}, "tui.select.confirm": {"ctrl+y"}}))
	s.HandleInput("\n")
	if !s.Done() || len(picked) != 1 || picked[0] != "b" {
		t.Fatalf("newline: done %v picked %v, want b", s.Done(), picked)
	}

	expanded := 0
	t.Run("expand without a callback", func(t *testing.T) {
		bare := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
		bare.HandleInput("\x0f")
		if bare.Done() {
			t.Fatal("the tools-expand binding finished the selector")
		}
	})
	withCallback := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil, ExtensionSelectorOptions{OnToggleToolsExpanded: func() { expanded++ }})
	withCallback.NeedsRedraw()
	if withCallback.IsDirty() {
		t.Fatal("the dirty mark was not cleared")
	}
	withCallback.HandleInput("\x0f")
	if expanded != 1 || withCallback.Done() {
		t.Fatalf("expand callback ran %d times, done %v", expanded, withCallback.Done())
	}
	if !withCallback.IsDirty() {
		t.Fatal("a handled key did not invalidate the selector")
	}
}

// extension-selector.ts:70-76 the countdown's expiry is onCancelCallback; a selector the user already answered is finished and does not cancel again.
func TestExtensionSelectorComponentCountdownExpiryAfterAnswerDoesNotCancel(t *testing.T) {
	cancels := 0
	s := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, func() { cancels++ })
	s.HandleInput("\r")
	s.expire()
	if cancels != 0 || s.Cancelled() {
		t.Fatalf("expiry after the selection cancelled %d times, cancelled %v", cancels, s.Cancelled())
	}
	open := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, func() { cancels++ })
	open.expire()
	if cancels != 1 || !open.Cancelled() {
		t.Fatalf("expiry of an open selector cancelled %d times, cancelled %v", cancels, open.Cancelled())
	}
}
