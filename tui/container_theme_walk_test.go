package tui

import "testing"

type invalidationCounter struct{ invalidations int }

func (*invalidationCounter) Render(int) []string { return []string{"row"} }
func (c *invalidationCounter) Invalidate()       { c.invalidations++ }

// Pi's TUI.invalidate calls invalidate() once on every mounted component (tui.ts:951-954) and its Container has no theme check of
// its own. PiG's Container invalidates its children at the first render under a new theme, for a container no walk reached. After
// the renderer's walk reached it under that theme the render must not tell the children again: a ui.custom component in the editor
// slot counted three invalidations for one theme set where Pi's counts two (parity scenarios extensions-runtime 104 and 105).
func TestContainerThemeCheckDoesNotRepeatTheWalk(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(original) })
	SetTheme("dark")
	child := &invalidationCounter{}
	container := NewContainer(child)
	container.renderBorrowed(80)
	if child.invalidations != 0 {
		t.Fatalf("the first render invalidated the child %d times", child.invalidations)
	}
	SetTheme("light")
	// A theme set invalidates the UI twice (the theme change callback, then the controller), as Pi's does.
	invalidateComponentTree(container)
	invalidateComponentTree(container)
	container.renderBorrowed(80)
	if child.invalidations != 2 {
		t.Fatalf("two walks and a render under the new theme invalidated the child %d times, want 2", child.invalidations)
	}
	// A theme change no walk followed: the render's own check tells the child, once.
	SetTheme("dark")
	container.renderBorrowed(80)
	container.renderBorrowed(80)
	if child.invalidations != 3 {
		t.Fatalf("a theme change with no walk left the child at %d invalidations, want 3", child.invalidations)
	}
}
