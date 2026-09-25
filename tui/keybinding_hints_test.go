package tui

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
