package chordctx_test

import (
	"context"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// AbortOrder tells which of two contexts was aborted first, whatever order the goroutines of their listeners run in: the
// reason of a context derived from two sources is the earlier abort's (AbortSignal.any).
func TestAbortOrderIsTheOrderOfTheCancelCalls(t *testing.T) {
	first, cancelFirst := chordctx.WithCancel(chordctx.Background())
	second, cancelSecond := chordctx.WithCancel(chordctx.Background())
	if chordctx.AbortOrder(first) != 0 {
		t.Fatal("a context not aborted has an order")
	}
	cancelSecond(context.Canceled)
	cancelFirst(context.Canceled)
	if a, b := chordctx.AbortOrder(first), chordctx.AbortOrder(second); a == 0 || b == 0 || b >= a {
		t.Fatalf("orders first=%d second=%d, want second earlier", a, b)
	}
	// A child aborted through its parent has the parent's place; a later cancel of its own does not move it.
	parent, cancelParent := chordctx.WithCancel(chordctx.Background())
	child, cancelChild := chordctx.WithCancel(parent)
	cancelParent(context.Canceled)
	order := chordctx.AbortOrder(parent)
	cancelChild(context.Canceled)
	if got := chordctx.AbortOrder(child); got != order {
		t.Fatalf("child order = %d, want its parent's %d", got, order)
	}
	// A context of another package has none.
	if chordctx.AbortOrder(context.Background()) != 0 {
		t.Fatal("a foreign context has an order")
	}
}
