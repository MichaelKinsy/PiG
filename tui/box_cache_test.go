package tui

import (
	"slices"
	"testing"
)

// inPlaceComponent returns the same backing array every frame, as components with a render cache do, and edits it in place between frames.
type inPlaceComponent struct{ lines []string }

func (c *inPlaceComponent) Render(int) []string { return c.lines }
func (*inPlaceComponent) Invalidate()           {}

// box.ts keeps the unpadded child lines in its render cache and applies padding and background only on a cache miss (upstream 0.99.1 box.ts:109). Since Pi
// b7bb7ca905b8 the lines a component returns are read-only (`readonly string[]`) and the box compares them child by child, the same array being
// unchanged (box.ts renderChildLines, sameLines: `a === b`): a child that changes what it shows returns a new array, and the box sees it.
func TestBoxRenderCacheSeesAChildsNewLines(t *testing.T) {
	child := &inPlaceComponent{lines: []string{"one", "two"}}
	box := NewPaddedBox(2, 1, func(s string) string { return "[" + s + "]" })
	box.AddChild(child)
	first := slices.Clone(box.Render(10))
	if want := []string{"[          ]", "[  one     ]", "[  two     ]", "[          ]"}; !slices.Equal(first, want) {
		t.Fatalf("first frame = %q, want %q", first, want)
	}
	child.lines = []string{"one", "changed"}
	if got, want := box.Render(10)[2], "[  changed ]"; got != want {
		t.Fatalf("frame after the child returned new lines = %q, want %q", got, want)
	}
	// An unchanged frame is a cache hit, and a width change is a miss.
	again := box.Render(10)
	if &box.Render(10)[0] != &again[0] {
		t.Fatal("unchanged children did not reuse the cached frame")
	}
	if got := box.Render(12)[1]; got != "[  one       ]" {
		t.Fatalf("frame at a new width = %q", got)
	}
}

// Pi's stated limit: output that is changed in place is not seen. The array a child returned again is the same array, so the box takes it for
// unchanged and serves its cached frame (the child broke the rule that render output is read-only).
func TestBoxRenderCacheTakesTheSameArrayForUnchanged(t *testing.T) {
	child := &inPlaceComponent{lines: []string{"one", "two"}}
	box := NewPaddedBox(2, 0, nil)
	box.AddChild(child)
	box.Render(10)
	child.lines[1] = "changed"
	if got := box.Render(10)[1]; got != "  two     " {
		t.Fatalf("frame after an in-place edit = %q, want the cached frame", got)
	}
}

// What "the same lines" is in Go (sameChildLines, Pi's sameLines): the same first element and the same length, or else equal rows. So the box
// serves its cache for a fresh slice with equal content, for nil against an empty slice and for two zero-length results (no first element to
// compare), and it re-renders when the length differs (the same array resliced), when a slice of the same array starts at another element and
// shows other rows, and when the rows differ.
func TestBoxRenderCacheIdentityEdges(t *testing.T) {
	backing := []string{"a", "b", "c"}
	child := &inPlaceComponent{lines: backing[:2]}
	box := NewPaddedBox(0, 0, nil)
	box.AddChild(child)
	last := box.Render(4)
	step := func(name string, lines, want []string, cached bool) {
		t.Helper()
		child.lines = lines
		got := box.Render(4)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: lines = %q, want %q", name, got, want)
		}
		if same := len(got) == len(last) && (len(got) == 0 || &got[0] == &last[0]); same != cached {
			t.Fatalf("%s: returned the cached lines = %v, want %v", name, same, cached)
		}
		last = got
	}
	step("a fresh slice with equal rows", []string{"a", "b"}, []string{"a   ", "b   "}, true)
	step("the same array resliced to another length", backing[:1], []string{"a   "}, false)
	step("a slice of the same array from another element", backing[1:2], []string{"b   "}, false)
	step("other rows", []string{"x"}, []string{"x   "}, false)
	step("a zero-length result", []string{}, []string{}, false)
	step("nil after an empty result", nil, []string{}, true)
}

// Box's padding fields are exported and may change between frames. Before the unpadded-cache change the left padding was part of the cached child lines; the cache key now carries the padding so a changed PaddingX or PaddingY cannot serve the previous frame.
func TestBoxRenderCacheMissesWhenPaddingChanges(t *testing.T) {
	box := NewPaddedBox(1, 0, nil)
	box.AddChild(&inPlaceComponent{lines: []string{"x"}})
	if got := box.Render(6); !slices.Equal(got, []string{" x    "}) {
		t.Fatalf("first frame = %q", got)
	}
	box.PaddingX = 2
	if got := box.Render(6); !slices.Equal(got, []string{"  x   "}) {
		t.Fatalf("frame after PaddingX change = %q, want the new left padding", got)
	}
	box.PaddingY = 1
	if got := box.Render(6); !slices.Equal(got, []string{"      ", "  x   ", "      "}) {
		t.Fatalf("frame after PaddingY change = %q, want the new vertical padding", got)
	}
}
