package tui

import (
	"slices"
	"testing"
)

// The interactive mode shows these selectors as children of the editor-slot Container, which reuses a child's lines until the child is invalidated. Pi re-renders the whole tree each frame, so a key that moves the selection must reach the screen through the parent as well.
func TestSelectorsRedrawThroughAParentContainerAfterInput(t *testing.T) {
	const down = "\x1b[B"
	cases := []struct {
		name string
		make func() (Component, func(string))
	}{
		{"UserMessageSelectorComponent", func() (Component, func(string)) {
			c := newUserMessageSelectorForTest([]string{"first", "second"})
			return c, c.HandleInput
		}},
		{"SelectSubmenuComponent", func() (Component, func(string)) {
			c := NewSelectSubmenu("Title", "Description", []SelectItem{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}, "a")
			return c, c.HandleInput
		}},
		{"ShowImagesSelectorComponent", func() (Component, func(string)) {
			c := NewShowImagesSelectorComponent(true, func(bool) {}, func() {})
			return c, c.HandleInput
		}},
		{"ThemeSelectorComponent", func() (Component, func(string)) {
			c := NewThemeSelectorComponent("dark", func(string) {}, func() {}, func(string) {})
			return c, c.HandleInput
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			component, handleInput := tc.make()
			parent := NewContainer(component)
			before := parent.Render(60)
			handleInput(down)
			got := parent.Render(60)
			if want := component.Render(60); !slices.Equal(got, want) {
				t.Fatalf("parent kept the lines from before the key\n got %q\nwant %q", got, want)
			}
			if slices.Equal(got, before) {
				t.Fatalf("the key did not change the selection: %q", got)
			}
		})
	}
}
