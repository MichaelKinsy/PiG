package main

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream ProcessTerminal.forwardInputSequence normalizes every StdinBuffer
// sequence, so `pi config` sees Apple Terminal's native Shift+Enter fallback
// too. driveConfigSelector must pass each split sequence through the same
// normalization before the selector handles it.
func TestConfigSelectorNormalizesNativeShiftEnter(t *testing.T) {
	newSelector := func() *tui.ConfigSelectorComponent {
		items := []tui.ResourceItem{
			{Path: "/a/one.md", ResourceType: tui.ResourceSkills, Scope: "user", Origin: "top-level"},
			{Path: "/a/two.md", ResourceType: tui.ResourceSkills, Scope: "user", Origin: "top-level"},
		}
		selector := tui.NewConfigSelector(tui.BuildResourceGroups(items), 0)
		selector.SetTerminalRows(40)
		return selector
	}
	run := func(normalize func(string) string) (string, []string) {
		var seen []string
		restore := normalizeConfigInputSequence
		normalizeConfigInputSequence = func(sequence string) string {
			seen = append(seen, sequence)
			return normalize(sequence)
		}
		defer func() { normalizeConfigInputSequence = restore }()
		selector := newSelector()
		ui := tui.New()
		ui.Add(selector)
		if err := driveConfigSelector(ui, selector, strings.NewReader("\r\x03")); err != nil {
			t.Fatal(err)
		}
		return strings.Join(selector.Render(80), "\n"), seen
	}

	plain, seen := run(func(sequence string) string { return sequence })
	if len(seen) < 1 || seen[0] != "\r" {
		t.Fatalf("normalized sequences = %q, want the Return first", seen)
	}
	shifted, _ := run(func(sequence string) string {
		return tui.NormalizeNativeShiftEnterInput(sequence, true, true)
	})
	enhanced := newSelector()
	enhanced.HandleInput("\x1b[13;2u")
	if want := strings.Join(enhanced.Render(80), "\n"); shifted != want {
		t.Fatalf("selector after a native Shift+Return:\n%s\nwant the Shift+Enter result:\n%s", shifted, want)
	}
	if shifted == plain {
		t.Fatal("Shift+Enter and Enter render the same; the test cannot tell whether normalization reached the selector")
	}
}
