package host

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// object.ts:1090-1110: a listener invocation runs inside its publication, synchronously up to its first await, so the
// synchronous part of one event's listener ends before the next event's listener starts, however long it computes.
func TestListenerInvocationsRunTheirSynchronousPartsInPublicationOrder(t *testing.T) {
	host := quiet(t, nil)
	counter := Local("t.listener-order", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"n", tNumber()},
		{"set", Method(tNumber(), nil)},
	}})
	var mu sync.Mutex
	var order []float64
	mustServe(t, host, counter, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		if err := self.On("follow", n, func(value any, _ Ctx) error {
			if value == float64(1) {
				// Synchronous work: no await, so the next event's listener must not start meanwhile.
				for start := time.Now(); time.Since(start) < 20*time.Millisecond; {
				}
			}
			mu.Lock()
			order = append(order, value.(float64))
			mu.Unlock()
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n, "set", methodFunc(func(value any, ctx Ctx) (any, error) {
			return nil, n.Replace(ctx, value, CellReplaceOptions{})
		})), nil
	}, ServeOptions{})
	ref := mustRef(t, host, counter, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	mustCall(t, ref, "set", float64(1), nil)
	mustCall(t, ref, "set", float64(2), nil)
	waitFor(t, "three invocations", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(order, []float64{0, 1, 2}) {
		t.Fatalf("invocations ran %v, want [0 1 2]", order)
	}
}
