// Package ops replays stored Chord operation batches for the storage backends.
package ops

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// ApplyBatches replays decoded operation batches over base without mutating it and returns the final value only.
func ApplyBatches(base durable.JsonValue, batches [][]durable.Op) (durable.JsonValue, error) {
	return delta.ApplyImmutableBatches(base, batches)
}
