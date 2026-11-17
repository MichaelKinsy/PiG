package tui

import (
	"slices"
	"sort"
	"testing"
)

// Pi's tests of components that add markers to a line (the OSC 133 zones of the user and the assistant message, 'markers are added once across
// repeated renders') render twice and compare: a component that writes into the array its child, its Container or its own cache returned shows its
// marker twice. Pi's Container.render returns the same array again while its children are unchanged (b7bb7ca905b8), and PiG's follows, so such a writer
// would also change what every later frame shows. This test holds every exported component type to it: rendered twice at one width with no
// state change, and beneath a parent Container as the transcript renders it, the second render equals the first and the first render's array is
// the same after the second.
func TestRenderingTwiceWithNoStateChangeShowsTheSameLines(t *testing.T) {
	fixtures := contractFixtures()
	names := make([]string, 0, len(fixtures))
	for name := range fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, width := range []int{80, 24} {
			t.Run(name, func(t *testing.T) {
				component := fixtures[name].build(t)
				first := component.Render(width)
				want := slices.Clone(first)
				if second := component.Render(width); !slices.Equal(second, want) {
					t.Fatalf("width %d: the second render differs from the first:\nfirst  %q\nsecond %q", width, want, second)
				}
				if !slices.Equal(first, want) {
					t.Fatalf("width %d: the array the first render returned changed when the component rendered again:\nbefore %q\nafter  %q", width, want, first)
				}

				parent := NewContainer(fixtures[name].build(t))
				held := parent.Render(width)
				snapshot := slices.Clone(held)
				if again := parent.Render(width); !slices.Equal(again, snapshot) {
					t.Fatalf("width %d: the second render beneath a parent differs from the first:\nfirst  %q\nsecond %q", width, snapshot, again)
				}
				if !slices.Equal(held, snapshot) {
					t.Fatalf("width %d: the array the parent returned changed when it rendered again:\nbefore %q\nafter  %q", width, snapshot, held)
				}
			})
		}
	}
}
