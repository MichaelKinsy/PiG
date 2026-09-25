package tui

import (
	"reflect"
	"testing"
)

func TestTUIKeybindingsManager_DefaultMatches(t *testing.T) {
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	kb := NewTUIKeybindingsManager(nil)

	cases := []struct {
		name   string
		data   string
		action TUIKeybinding
		want   bool
	}{
		{"escape matches ESC byte", "\x1b", KBSelectCancel, true},
		{"esc alias matches ESC byte", "\x1b", "tui.select.cancel", true},
		{"ctrl+c matches cancel", "\x03", KBSelectCancel, true},
		{"enter matches submit", "\r", KBInputSubmit, true},
		{"return alias matches submit", "\r", KBInputSubmit, true},
		{"enter matches select confirm", "\r", KBSelectConfirm, true},
		{"up matches select up", "\x1b[A", KBSelectUp, true},
		{"down matches select down", "\x1b[B", KBSelectDown, true},
		{"backspace matches delete char backward", "\x7f", KBEditorDeleteCharBack, true},
		{"ctrl+w matches delete word backward", "\x17", KBEditorDeleteWordBack, true},
		{"alt+backspace matches delete word backward", "\x1b\x7f", KBEditorDeleteWordBack, true},
		{"ctrl+a matches cursor line start", "\x01", KBEditorCursorLineStart, true},
		{"ctrl+e matches cursor line end", "\x05", KBEditorCursorLineEnd, true},
		{"ctrl+u matches delete to line start", "\x15", KBEditorDeleteToLineStart, true},
		{"ctrl+k matches delete to line end", "\x0b", KBEditorDeleteToLineEnd, true},
		{"ctrl+y matches yank", "\x19", KBEditorYank, true},
		{"alt+y matches yank pop", "\x1by", KBEditorYankPop, true},
		{"platform undo key matches undo", hostUndoInput(), KBEditorUndo, true},
		{"tab matches tab", "\t", KBInputTab, true},
		{"ctrl+j LF matches newline", "\n", KBInputNewLine, true},
		{"ESC+LF is not a Pi key encoding", "\x1b\n", KBInputNewLine, false},
		{"ctrl+j CSI-u matches newline", "\x1b[106;5u", KBInputNewLine, true},
		{"ctrl+j modifyOtherKeys matches newline", "\x1b[27;5;106~", KBInputNewLine, true},
		{"alt+left matches word left", "\x1b[1;3D", KBEditorCursorWordLeft, true},
		{"alt+right matches word right", "\x1b[1;3C", KBEditorCursorWordRight, true},
		{"alt+d matches delete word forward", "\x1bd", KBEditorDeleteWordForward, true},
		{"pageUp matches editor page up", "\x1b[5~", KBEditorPageUp, true},
		{"pageDown matches select page down", "\x1b[6~", KBSelectPageDown, true},
		{"delete matches forward delete", "\x1b[3~", KBEditorDeleteCharForward, true},
		{"ctrl+d matches forward delete", "\x04", KBEditorDeleteCharForward, true},

		// Non-matches
		{"random byte not cancel", "x", KBSelectCancel, false},
		{"ctrl+c not submit", "\x03", KBInputSubmit, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := kb.Matches(tc.data, tc.action)
			if got != tc.want {
				t.Errorf("Matches(%q, %q) = %v, want %v", tc.data, tc.action, got, tc.want)
			}
		})
	}
}

func TestTUIKeybindingsManager_UserOverride(t *testing.T) {
	kb := NewTUIKeybindingsManager(map[string][]string{
		KBSelectCancel: {"ctrl+q"},
		KBSelectUp:     {},
	})

	// Default "escape" should no longer match cancel.
	if kb.Matches("\x1b", KBSelectCancel) {
		t.Error("escape should not match cancel after user override to ctrl+q")
	}
	if kb.Matches("\x1b[A", KBSelectUp) {
		t.Error("explicit empty binding should disable the default")
	}
	if kb.Matches("x", "tui.unknown") {
		t.Error("unknown action matched input")
	}
}

func TestTUIKeybindingsManager_Conflicts(t *testing.T) {
	kb := NewTUIKeybindingsManager(map[string][]string{
		KBSelectCancel:  {"ctrl+c"},
		KBSelectConfirm: {"ctrl+c"},
	})

	conflicts := kb.GetConflicts()
	if len(conflicts) == 0 {
		t.Fatal("expected at least one conflict")
	}
	found := false
	for _, c := range conflicts {
		if c.Key == "ctrl+c" {
			found = true
		}
	}
	if !found {
		t.Error("expected conflict on ctrl+c")
	}
}

func TestTUIKeybindingsManager_GetKeys(t *testing.T) {
	kb := NewTUIKeybindingsManager(nil)
	keys := kb.GetKeys(KBEditorCursorLeft)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys for cursorLeft, got %d: %v", len(keys), keys)
	}
}

func TestTUIKeybindingsManager_GetDefinition(t *testing.T) {
	kb := NewTUIKeybindingsManager(nil)
	def, ok := kb.GetDefinition(KBEditorCursorUp)
	if !ok {
		t.Fatal("expected definition for cursorUp")
	}
	if def.Description != "Move cursor up" {
		t.Errorf("unexpected description: %s", def.Description)
	}
}

func TestTUIKeybindingsManager_GetUserAndResolvedBindings(t *testing.T) {
	kb := NewTUIKeybindingsManager(map[string][]string{
		KBSelectCancel:     {"ctrl+q"},
		KBEditorCursorLeft: {"left", "ctrl+b", "left"},
	})

	gotUser := kb.GetUserBindings()
	wantUser := map[string][]string{
		KBSelectCancel:     {"ctrl+q"},
		KBEditorCursorLeft: {"left", "ctrl+b", "left"},
	}
	if !reflect.DeepEqual(gotUser, wantUser) {
		t.Fatalf("GetUserBindings() = %#v want %#v", gotUser, wantUser)
	}

	resolved := kb.GetResolvedBindings()
	if got := resolved[KBSelectCancel]; got != "ctrl+q" {
		t.Fatalf("resolved cancel = %#v want ctrl+q", got)
	}
	if got, ok := resolved[KBEditorCursorLeft].([]string); !ok || !reflect.DeepEqual(got, []string{"left", "ctrl+b"}) {
		t.Fatalf("resolved cursorLeft = %#v want []string{left, ctrl+b}", resolved[KBEditorCursorLeft])
	}
	if _, ok := resolved[KBEditorCursorUp]; !ok {
		t.Fatal("resolved bindings missing default action entry")
	}
}

func TestTUIKeybindingsGlobalAliases(t *testing.T) {
	prev := GetKeybindings()
	defer SetKeybindings(prev)

	kb := NewTUIKeybindingsManager(map[string][]string{KBSelectCancel: {"ctrl+q"}})
	SetKeybindings(kb)
	if GetTUIKeybindings() != kb {
		t.Fatal("GetTUIKeybindings should share global instance with GetKeybindings")
	}
}
