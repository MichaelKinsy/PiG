package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// model-selector.ts:44-51, oauth-selector.ts and tree-selector.ts `get/set focused`: a selector is Focusable and propagates the
// TUI focus to its search input, which emits the hardware-cursor marker only while focused. Each case drives the same sequence
// and reads the rendered marker, so a setter that does not reach the input, or a getter that does not follow it, fails.
func TestSelectorsPropagateFocusToTheSearchInput(t *testing.T) {
	providers := []OAuthProvider{{ID: "p", Name: "Provider"}}
	for _, tc := range []struct {
		name  string
		build func() interface {
			Component
			Focusable
			Focused() bool
		}
	}{
		{"model", func() interface {
			Component
			Focusable
			Focused() bool
		} {
			return NewStaticModelSelectorComponent("Select model", mkItems("a/b"), mkItems("a/b"), "")
		}},
		{"oauth", func() interface {
			Component
			Focusable
			Focused() bool
		} {
			return NewOAuthSelectorComponent("login", providers, nil, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := tc.build()
			if !IsFocusable(selector) {
				t.Fatal("selector is not Focusable")
			}
			marker := func() bool { return strings.Contains(strings.Join(selector.Render(60), "\n"), widthx.CursorMarker) }
			selector.SetFocused(false)
			if selector.Focused() || marker() {
				t.Fatalf("after SetFocused(false): Focused=%v marker=%v", selector.Focused(), marker())
			}
			selector.SetFocused(true)
			if !selector.Focused() || !marker() {
				t.Fatalf("after SetFocused(true): Focused=%v marker=%v", selector.Focused(), marker())
			}
			selector.SetFocused(false)
			if selector.Focused() || marker() {
				t.Fatalf("after a second SetFocused(false): Focused=%v marker=%v", selector.Focused(), marker())
			}
		})
	}
}

func TestTreeSelectorFocusedGetterFollowsSetFocused(t *testing.T) {
	ts := NewTreeSelectorComponent("Pick", nil)
	for _, want := range []bool{true, false, true} {
		ts.SetFocused(want)
		if ts.Focused() != want {
			t.Fatalf("Focused() = %v after SetFocused(%v)", ts.Focused(), want)
		}
	}
}
