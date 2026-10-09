package tui

// pi: packages/coding-agent/src/modes/interactive/components/keybinding-hints.ts

import (
	"runtime"
	"strings"
	"testing"
)

func TestFormatKeyText(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		capitalize bool
		want       string
	}{
		{
			name:       "single combo capitalized",
			key:        "shift+ctrl+p",
			capitalize: true,
			want:       "Shift+Ctrl+P",
		},
		{
			name:       "slash separated alternates",
			key:        "escape/ctrl+c",
			capitalize: true,
			want:       "Escape/Ctrl+C",
		},
		{
			name:       "raw formatting preserves lowercase when not capitalized",
			key:        "enter",
			capitalize: false,
			want:       "enter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatKeyText(tt.key, tt.capitalize); got != tt.want {
				t.Fatalf("FormatKeyText(%q, %t) = %q, want %q", tt.key, tt.capitalize, got, tt.want)
			}
		})
	}
}

func TestFormatKeyTextDarwinAltUsesOption(t *testing.T) {
	want := "alt+enter"
	if runtime.GOOS == "darwin" {
		want = "option+enter"
	}
	if got := FormatKeyText("alt+enter", false); got != want {
		t.Fatalf("FormatKeyText alt mapping = %q, want %q", got, want)
	}

	wantDisplay := "Alt+Enter"
	if runtime.GOOS == "darwin" {
		wantDisplay = "Option+Enter"
	}
	if got := KeyDisplayText("alt+enter"); got != wantDisplay {
		t.Fatalf("KeyDisplayText alt mapping = %q, want %q", got, wantDisplay)
	}
}

func TestRawKeyHintFormatsRawKeyText(t *testing.T) {
	got := RawKeyHint("alt+enter", "follow up")
	wantKey := "alt+enter"
	if runtime.GOOS == "darwin" {
		wantKey = "option+enter"
	}
	if !strings.Contains(got, wantKey) || !strings.Contains(got, "follow up") {
		t.Fatalf("RawKeyHint output = %q, want formatted key %q and description", got, wantKey)
	}
}

// keybinding-hints.ts:34-42: keyText joins every key bound to the action with "/" in lower case; keyDisplayText is the capitalized form; an unbound action has no text.
func TestActionKeyTextJoinsBoundKeysWithoutCapitalizing(t *testing.T) {
	previous := GetTUIKeybindings()
	defer SetTUIKeybindings(previous)
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{"tui.select.confirm": {"enter", "ctrl+j"}}))
	if got := ActionKeyText("tui.select.confirm"); got != "enter/ctrl+j" {
		t.Fatalf("ActionKeyText = %q", got)
	}
	if got := ActionKeyDisplayText("tui.select.confirm"); got != "Enter/Ctrl+J" {
		t.Fatalf("ActionKeyDisplayText = %q", got)
	}
	if got := ActionKeyText("no.such.action"); got != "" {
		t.Fatalf("an unbound action has no key text: %q", got)
	}
}
