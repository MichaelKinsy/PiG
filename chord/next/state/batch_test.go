package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/state/batch.test.ts. The describe blocks "reentrancy" and "delivery" run on a ChordHost
// with served objects, defineState documents and references; they are covered here by batch_own_test.go and
// derive_own_test.go on the same Batch, and wait for the host, contracts and documents packages to run as upstream.

func TestFlushes(t *testing.T) {
	t.Run("run once per listing at the end of the commit, after its deliveries; a throwing one is reported", func(t *testing.T) {
		var reported []state.BatchError
		batch := state.NewBatch(func(err state.BatchError) { reported = append(reported, err) }, nil)
		var order []string
		err := batch.Commit(func() error {
			batch.Flushing(func(context.Context) error { order = append(order, "first"); return nil })
			batch.Flushing(func(context.Context) error { return errors.New("flush threw") })
			batch.Flushing(func(context.Context) error { order = append(order, "third"); return nil })
			order = append(order, "adopted")
			return nil
		}, background)
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if want := []string{"adopted", "first", "third"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
		if len(reported) != 1 || reported[0].Kind != state.BatchDeliveryFailed || reported[0].Error.Error() != "flush threw" {
			t.Fatalf("reported = %+v, want one delivery_failed flush threw", reported)
		}
		if err := batch.Commit(func() error { return nil }, background); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if want := []string{"adopted", "first", "third"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("order after a second commit = %v, want %v", order, want)
		}
	})
}
