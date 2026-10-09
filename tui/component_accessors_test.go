package tui

import (
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/{oauth-selector.ts:62-69, extension-input.ts:26-35}
// `get focused` returns the flag `set focused` stored, and the setter propagates it to the component's input or editor.
func TestFocusableComponentsStoreAndPropagateFocus(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (Component, func() bool, func(bool), func() bool)
	}{
		{"OAuthSelectorComponent", func() (Component, func() bool, func(bool), func() bool) {
			c := NewOAuthSelectorComponent("login", []OAuthProvider{{ID: "a", Name: "A"}}, nil, nil)
			return c, c.Focused, c.SetFocused, func() bool { return c.search.Focused }
		}},
		{"ExtensionInputComponent", func() (Component, func() bool, func(bool), func() bool) {
			c := NewExtensionInputComponent("Title", "", nil, nil)
			return c, c.Focused, c.SetFocused, func() bool { return c.input.Focused }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component, focused, setFocused, inner := tc.build()
			if !IsFocusable(component) {
				t.Fatal("component is not Focusable")
			}
			setFocused(true)
			if !focused() || !inner() {
				t.Fatalf("after SetFocused(true): focused=%v inner=%v", focused(), inner())
			}
			setFocused(false)
			if focused() || inner() {
				t.Fatalf("after SetFocused(false): focused=%v inner=%v", focused(), inner())
			}
		})
	}
}

// upstream: bash-execution.ts:83-99 appendOutput strips ANSI and normalizes CRLF and CR to LF before joining the lines; :213-222 getOutput joins them with "\n" and getCommand returns the command.
func TestBashExecutionAppendOutputSanitizesAndGettersReadItBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{"plain", []string{"hello"}, "hello"},
		{"ansi stripped", []string{"\x1b[31mred\x1b[0m"}, "red"},
		{"crlf and cr become lf", []string{"a\r\nb\rc"}, "a\nb\nc"},
		{"chunks continue the last line", []string{"par", "tial\nnext"}, "partial\nnext"},
		{"trailing newline kept", []string{"line\n"}, "line\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := NewBashExecutionComponent("echo hi", nil, false, 1)
			for _, chunk := range tc.chunks {
				block.AppendOutput(chunk)
			}
			if got := block.GetOutput(); got != tc.want {
				t.Fatalf("GetOutput = %q, want %q", got, tc.want)
			}
			if got := block.GetCommand(); got != "echo hi" {
				t.Fatalf("GetCommand = %q", got)
			}
			if strings.Contains(strings.Join(block.Render(80), "\n"), "\r") {
				t.Fatal("a carriage return reached the rendered block")
			}
		})
	}
}
