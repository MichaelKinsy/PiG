package tui

import (
	"slices"
	"testing"
)

// components/mouse-region.ts: MouseRegion adds mouse handling "without changing its rendering": render(width) is the child's render(width), and invalidate()
// reaches the child.
func TestMouseRegionRendersAndInvalidatesTheChildUnchanged(t *testing.T) {
	child := &countingChild{label: "inner"}
	wide := &scriptedChild{lines: []string{"a", "b"}}
	var component Component = NewMouseRegion(child, func(TuiMouseEvent) *TuiMouseEventResult { return nil })
	region, ok := component.(*MouseRegion)
	if !ok {
		t.Fatalf("NewMouseRegion returned %T, want a *MouseRegion", component)
	}
	if got := region.Render(30); !slices.Equal(got, []string{"inner"}) {
		t.Fatalf("Render = %q, want the child's lines", got)
	}
	region.Invalidate()
	if child.invalidates != 1 {
		t.Fatalf("Invalidate reached the child %d times, want 1", child.invalidates)
	}
	sized := NewMouseRegion(wide, nil)
	if got := sized.Render(17); !slices.Equal(got, []string{"a", "b"}) || !slices.Equal(wide.widths, []int{17}) {
		t.Fatalf("Render(17) = %q with the child asked for widths %v, want its lines at width 17", got, wide.widths)
	}
}
