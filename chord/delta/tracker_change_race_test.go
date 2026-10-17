package delta

import (
	"errors"
	"sync"
	"testing"
)

// A Change's draft handles lock their overlay, so goroutines may share a draft. The Change itself must be as safe: reading its draft while another goroutine prepares or aborts it either succeeds or panics with ErrRevoked, and never races on the Change's own fields (run under -race).
func TestAChangeIsSafeToSettleWhileAnotherGoroutineReadsIt(t *testing.T) {
	settle := map[string]func(*Change){
		"Prepare": func(change *Change) { _, _ = change.Prepare() },
		"Abort":   func(change *Change) { change.Abort() },
	}
	for name, settleChange := range settle {
		t.Run(name, func(t *testing.T) {
			for range 200 {
				tracker := Track(JsonObjectOf("a", 1.0))
				change := tracker.BeginChange()
				var wg sync.WaitGroup
				wg.Go(func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							if err, ok := recovered.(error); ok && errors.Is(err, ErrRevoked) {
								return
							}
							panic(recovered)
						}
					}()
					_ = change.State().Set("b", 2.0)
				})
				wg.Go(func() { settleChange(change) })
				wg.Go(func() { change.Abort() })
				wg.Wait()
				if _, err := change.Prepare(); err == nil {
					t.Fatalf("%s: a settled change prepared again", name)
				}
			}
		})
	}
}
