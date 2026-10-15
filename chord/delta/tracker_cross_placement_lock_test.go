package delta

import (
	"sync"
	"testing"
	"time"
)

// Two open changes that place each other's handles from two goroutines must not wait on each other. A placement used to hold its own draft's lock while it locked the placed handle's draft, so change A placing B's handle and change B placing A's handle locked the two drafts in opposite orders. Pi runs both on one thread; Go callers may run changes concurrently, so a placement copies a foreign handle before it locks its own draft.
func TestChangesPlacingEachOthersHandlesDoNotDeadlock(t *testing.T) {
	place := map[string]func(target *Object, source *Object){
		"Object.Set":    func(target, source *Object) { _ = target.Set("k", source) },
		"Array.Set":     func(target, source *Object) { _ = target.Array("rows").Set(0, source) },
		"Array.Push":    func(target, source *Object) { _, _ = target.Array("rows").Push(source) },
		"Array.Unshift": func(target, source *Object) { _, _ = target.Array("rows").Unshift(source) },
		"Array.Splice":  func(target, source *Object) { _, _ = target.Array("rows").Splice(0, 1, source) },
		"Array.Fill":    func(target, source *Object) { _ = target.Array("rows").Fill(source, 0, 1) },
	}
	for name, placeHandle := range place {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 2000 {
					tracker := Track(JsonObjectOf("a", JsonObjectOf("x", 1.0), "rows", []any{0.0}))
					left, right := tracker.BeginChange().State(), tracker.BeginChange().State()
					leftSource, rightSource := left.Object("a"), right.Object("a")
					var wg sync.WaitGroup
					wg.Go(func() { placeHandle(left, rightSource) })
					wg.Go(func() { placeHandle(right, leftSource) })
					wg.Wait()
				}
			}()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("%s: two changes placing each other's handles deadlocked", name)
			}
		})
	}
}
