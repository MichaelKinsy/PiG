package tui

import (
	"slices"
	"testing"
)

// Ports packages/tui/test/container.test.ts of Pi b7bb7ca905b8 (#9549: copying every transcript line on every frame made long
// fullscreen transcripts expensive). Pi's assertions are `assert.strictEqual(outer.render(20), first)`, the same array object while
// the children are unchanged, and `notStrictEqual` plus an untouched old array after a change. A Go slice has no identity of its own, so
// "the same array" is the same first element address (sameLines).

func TestContainerReturnsTheSameLinesWhileChildrenAreUnchanged(t *testing.T) {
	text := NewPaddedText("hello", 0, 0, nil)
	inner := NewContainer()
	inner.AddChild(text)
	inner.AddChild(NewSpacer(1))
	outer := NewContainer()
	outer.AddChild(inner)

	first := outer.Render(20)
	if second := outer.Render(20); !sameLines(second, first) {
		t.Fatalf("a second render of unchanged children returned another array")
	}

	text.SetText("changed")
	changed := outer.Render(20)
	if sameLines(changed, first) {
		t.Fatalf("a render after a change returned the old array")
	}
	if got, want := trimmedRows(changed), []string{"changed", ""}; !slices.Equal(got, want) {
		t.Fatalf("changed = %q, want %q", got, want)
	}
	if got, want := trimmedRows(first), []string{"hello", ""}; !slices.Equal(got, want) {
		t.Fatalf("the array returned before the change now holds %q, want %q", got, want)
	}
}

func TestContainerRendersAgainWhenChildrenAreAddedOrTheWidthChanges(t *testing.T) {
	container := NewContainer()
	container.AddChild(NewPaddedText("a", 0, 0, nil))
	first := container.Render(20)
	container.AddChild(NewPaddedText("b", 0, 0, nil))
	if got, want := trimmedRows(container.Render(20)), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("after addChild = %q, want %q", got, want)
	}
	if sameLines(container.Render(10), first) {
		t.Fatalf("a render at another width returned the old array")
	}
}

func TestBoxReusesItsOutputWhileChildrenAreUnchanged(t *testing.T) {
	text := NewPaddedText("boxed", 0, 0, nil)
	box := NewPaddedBox(1, 0, nil)
	box.AddChild(text)
	first := box.Render(20)
	if second := box.Render(20); !sameLines(second, first) {
		t.Fatalf("a second render of unchanged children returned another array")
	}
	text.SetText("next")
	if sameLines(box.Render(20), first) {
		t.Fatalf("a render after a change returned the old array")
	}
}

type heightMouseChild struct {
	name  string
	lines int
	hits  *[]string
}

func (c heightMouseChild) Render(int) []string {
	out := make([]string, c.lines)
	for i := range out {
		out[i] = c.name
	}
	return out
}
func (c heightMouseChild) Invalidate() {}
func (c heightMouseChild) HandleMouse(TuiMouseEvent) *TuiMouseEventResult {
	*c.hits = append(*c.hits, c.name)
	return &TuiMouseEventResult{Handled: true}
}

func TestContainerDispatchesMouseByTheRenderedChildHeights(t *testing.T) {
	var hits []string
	container := NewContainer()
	container.AddChild(heightMouseChild{"first", 2, &hits})
	container.AddChild(heightMouseChild{"second", 1, &hits})
	container.Render(20)
	wheelAt := func(y int) TuiMouseEvent {
		return TuiMouseEvent{Type: MouseWheel, Button: MouseButtonNone, Y: y, ScreenY: y, Width: 20, Height: 3, WheelDelta: 1}
	}
	container.HandleMouse(wheelAt(2))
	container.HandleMouse(wheelAt(1))
	if want := []string{"second", "first"}; !slices.Equal(hits, want) {
		t.Fatalf("hits = %q, want %q", hits, want)
	}
}
