package codingagent

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports the four exact Pi 0.87.1 trust-selector cases.
func TestTrustSelectorUpstream(t *testing.T) {
	previous := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	DefaultKeybindingsManager()
	// Upstream's fixtures are POSIX paths. Pi's normalizeCwd resolves them
	// with path.resolve, which on Windows puts a rooted path on the current
	// drive (C:\project, parent C:\), so the fixtures use that native form.
	native := func(path string) string {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		return absolute
	}
	render := func(component *TrustSelectorComponent) string {
		return stripANSITest(strings.Join(component.Render(120), "\n"))
	}
	contains := func(t *testing.T, output string, expected ...string) {
		t.Helper()
		for _, text := range expected {
			if !strings.Contains(output, text) {
				t.Fatalf("render %q is missing %q", output, text)
			}
		}
	}
	// packages/coding-agent/test/trust-selector.test.ts:17
	t.Run("keeps the saved trusted decision marked while browsing", func(t *testing.T) {
		selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: native("/project"), SavedDecision: &ProjectTrustStoreEntry{Path: native("/project"), Decision: true}, ProjectTrusted: true})
		contains(t, render(selector), "Saved decision: trusted ("+native("/project")+")", "Current session: trusted", "→ ✓ Trust")
		selector.HandleInput("\x1b[B")
		text := render(selector)
		contains(t, text, "✓ Trust", "→   Trust parent folder ("+native("/")+")")
		if strings.Contains(text, "✓ Do not trust") {
			t.Fatalf("saved marker moved: %q", text)
		}
	})
	// packages/coding-agent/test/trust-selector.test.ts:38
	t.Run("selects a trust decision", func(t *testing.T) {
		var selected *TrustSelection
		selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: native("/project"), OnSelect: func(value TrustSelection) { selected = &value }})
		selector.HandleInput("\n")
		want := &TrustSelection{Trusted: true, Updates: []ProjectTrustUpdate{{Path: native("/project"), Decision: new(true)}}}
		if !reflect.DeepEqual(selected, want) {
			t.Fatalf("selection=%+v, want %+v", selected, want)
		}
	})
	// packages/coding-agent/test/trust-selector.test.ts:53
	t.Run("labels saved ancestor decisions as inherited", func(t *testing.T) {
		selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: native("/parent/project/nested"), SavedDecision: &ProjectTrustStoreEntry{Path: native("/parent"), Decision: true}, ProjectTrusted: true})
		contains(t, render(selector), "Saved decision: trusted (inherited from "+native("/parent")+")")
	})
	// packages/coding-agent/test/trust-selector.test.ts:67
	t.Run("adds a trust parent option", func(t *testing.T) {
		var selected *TrustSelection
		selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: native("/parent/project"), SavedDecision: &ProjectTrustStoreEntry{Path: native("/parent"), Decision: true}, ProjectTrusted: true, OnSelect: func(value TrustSelection) { selected = &value }})
		contains(t, render(selector), "Saved decision: trusted (inherited from "+native("/parent")+")", "✓ Trust parent folder ("+native("/parent")+")")
		selector.HandleInput("\n")
		want := &TrustSelection{Trusted: true, Updates: []ProjectTrustUpdate{{Path: native("/parent"), Decision: new(true)}, {Path: native("/parent/project"), Decision: nil}}}
		if !reflect.DeepEqual(selected, want) {
			t.Fatalf("selection=%+v, want %+v", selected, want)
		}
	})
}
