package codingagent

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// The radius login menu swaps one row of its selector's output for the animated row. Pi's radius-login-selector.ts copies the lines first
// (`[...super.render(width)]`); a menu that wrote into the array its selector returned would find the row it looks for gone on the next render once
// a Container returns the same array while its children are unchanged (Pi b7bb7ca905b8). Rendering twice at a fixed clock, directly and beneath a
// parent Container, must show the same rows, and the selector's own output must not change.
func TestRadiusLoginMenuRendersTheSameRowsTwiceAndLeavesTheSelectorOutputAlone(t *testing.T) {
	label := "Sign in with Radius" + tui.FormatAuthSelectorProviderStatus(tui.OAuthProvider{ID: "radius", Name: "Radius", AuthType: "oauth"})
	selector := tui.NewExtensionSelectorComponent("Select authentication method:", []string{"Sign in with an account", "Sign in with an API key", label}, nil, nil)
	menu := newRadiusLoginMenu(selector, label, "Sign in with Radius", func() {})
	defer menu.Dispose()
	start := time.Now()
	menu.start, menu.now = start, func() time.Time { return start.Add(300 * time.Millisecond) }
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\x1b[B")

	selectorLines := slices.Clone(selector.Render(120))
	first := slices.Clone(menu.Render(120))
	if !menu.animating.Load() || slices.Equal(first, selectorLines) {
		t.Fatalf("the selected Radius row was not animated: %q", first)
	}
	if again := menu.Render(120); !slices.Equal(again, first) {
		t.Fatalf("the second render of the menu differs from the first:\nfirst  %q\nsecond %q", first, again)
	}
	if after := selector.Render(120); !slices.Equal(after, selectorLines) {
		t.Fatalf("the menu wrote into the selector's output:\nbefore %q\nafter  %q", selectorLines, after)
	}

	parent := tui.NewContainer(menu)
	held := parent.Render(120)
	snapshot := slices.Clone(held)
	if again := parent.Render(120); !slices.Equal(again, snapshot) {
		t.Fatalf("the second render beneath a parent differs from the first:\nfirst  %q\nsecond %q", snapshot, again)
	}
	if !slices.Equal(held, snapshot) {
		t.Fatalf("the array the parent returned changed when it rendered again:\nbefore %q\nafter  %q", snapshot, held)
	}
}
