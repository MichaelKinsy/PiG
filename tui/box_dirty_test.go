package tui

import "testing"

// box.ts keys the render cache by bgFn("test") when there is a background and by undefined when there is none, so a background that draws "" is a different key
// from no background.
func TestBoxBackgroundThatDrawsNothingIsNotNoBackground(t *testing.T) {
	box := NewPaddedBox(1, 0, nil)
	box.AddChild(NewText("inside"))
	if lines := box.Render(20); len(lines) != 1 || lines[0] == "" {
		t.Fatalf("box without a background renders %q", lines)
	}
	box.SetBgFn(func(string) string { return "" })
	if lines := box.Render(20); len(lines) != 1 || lines[0] != "" {
		t.Fatalf("box whose background draws nothing renders %q, want one empty row", lines)
	}
}

// A type that embeds a Box is a child a Go container serves from its per-child cache until the child reports a change; Pi's Container.render renders every child
// every frame, so SetBgFn and SetPaddingX, which change what the box draws, must mark it dirty.
func TestBoxSetBgFnAndSetPaddingXReportTheChange(t *testing.T) {
	box := NewBox()
	box.AddChild(NewText("inside"))
	box.Render(20)
	box.NeedsRedraw()
	for name, mutate := range map[string]func(){
		"SetBgFn":     func() { box.SetBgFn(func(s string) string { return "[" + s + "]" }) },
		"SetPaddingX": func() { box.SetPaddingX(3) },
	} {
		mutate()
		if !box.IsDirty() {
			t.Errorf("%s did not mark the box dirty", name)
		}
		box.Render(20)
		box.NeedsRedraw()
	}
}
