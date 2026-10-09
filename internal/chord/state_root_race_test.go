package chord

import (
	"context"
	"sync"
	"testing"

	overlay "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// A replacement can move the state between an object and an array root, which swaps its tracker. Edit, Change, Replace and Value from many goroutines must stay race-free (run with -race) and leave a value that decodes.
func TestReplicatedStateRootKindChangesAreSerialized(t *testing.T) {
	state, err := NewReplicatedState[any](map[string]any{"n": 0.0, "list": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	unsubscribe, err := state.Subscribe(func(any, context.Context, ReplicatedStateDelivery) {})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	var group sync.WaitGroup
	for worker := range 8 {
		group.Go(func() {
			for step := range 100 {
				switch (worker + step) % 4 {
				case 0:
					_ = state.Edit(ctx, func(root *overlay.Object) error {
						if list := root.Array("list"); list != nil {
							_, err := list.Push(float64(step))
							return err
						}
						return nil
					})
				case 1:
					_ = state.Change(ctx, func(value any) error {
						if object, ok := value.(*chordjson.Object); ok {
							object.Set("n", float64(step))
						}
						return nil
					})
				case 2:
					var next any = map[string]any{"n": float64(step), "list": []any{}}
					if step%25 == 0 {
						next = []any{float64(step)}
					}
					if err := state.Replace(ctx, next); err != nil {
						t.Error(err)
					}
				default:
					_ = state.Value()
				}
			}
		})
	}
	group.Wait()
}
