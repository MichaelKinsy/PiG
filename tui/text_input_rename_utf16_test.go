package tui

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi input.ts:60-63 preserves the UTF-16 cursor even inside a surrogate pair.
// These public-path cases must not round the cursor or replace either half.
func TestInputSetTextRetainsUTF16Cursor(t *testing.T) {
	for _, tc := range []struct {
		name, initial, replacement, key, want string
	}{
		{"fresh", "", "Old", "X", "XOld"},
		{"retained", "a", "Old", "X", "OXld"},
		{"clamped", "long", "a", "X", "aX"},
		{"empty", "a", "", "X", "X"},
		{"astral prefix", "😀", "abcd", "X", "abXcd"},
		{"split pair", "a", "😀", "X", "\xed\xa0\xbdX\xed\xb8\x80"},
		{"backspace half", "a", "😀", "\x7f", "\xed\xb8\x80"},
		{"delete half", "a", "😀", "\x1b[3~", "\xed\xa0\xbd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := NewInput(InputOptions{})
			input.HandleInput(tc.initial)
			input.SetText(tc.replacement)
			input.HandleInput(tc.key)
			if got := input.Text(); got != tc.want {
				t.Fatalf("text bytes=% x, want % x", got, tc.want)
			}
			record, err := json.Marshal([]any{tc.name, jsstring.ToUTF16(input.Text())})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("INPUT_UTF16 %s\n", record)
		})
	}
}
