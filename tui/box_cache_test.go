package tui

import (
	"slices"
	"testing"
)

// inPlaceComponent returns the same backing array every frame, as components with a render cache do, and edits it in place between frames.
type inPlaceComponent struct{ lines []string }

func (c *inPlaceComponent) Render(int) []string { return c.lines }
func (*inPlaceComponent) Invalidate()           {}

// box.ts keeps the unpadded child lines in its render cache and applies padding and background only on a cache miss (upstream 0.99.1 box.ts:109). The cache must not alias a child's backing array, or an in-place edit would compare equal to itself and serve a stale frame.
func TestBoxRenderCacheSeesInPlaceChildEdits(t *testing.T) {
	child := &inPlaceComponent{lines: []string{"one", "two"}}
	box := NewPaddedBox(2, 1, func(s string) string { return "[" + s + "]" })
	box.AddChild(child)
	first := slices.Clone(box.Render(10))
	if want := []string{"[          ]", "[  one     ]", "[  two     ]", "[          ]"}; !slices.Equal(first, want) {
		t.Fatalf("first frame = %q, want %q", first, want)
	}
	child.lines[1] = "changed"
	if got, want := box.Render(10)[2], "[  changed ]"; got != want {
		t.Fatalf("frame after an in-place edit = %q, want %q", got, want)
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
