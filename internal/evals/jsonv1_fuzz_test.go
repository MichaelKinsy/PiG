package evals

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzSortedKeysInInsertionOrderMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		record := map[string]any{"z": 1, "a": 2}
		jsonseed.SameResult(t, data, "sortedKeysInInsertionOrder", sortedKeysInInsertionOrder(string(data), record), sortedKeysInInsertionOrderV1(string(data), record))
	})
}

func BenchmarkSortedKeysInInsertionOrder(b *testing.B) {
	record := map[string]any{"z": 1, "a": 2}
	jsonseed.Bench(b, jsonseed.Typical(), func(d []byte) { sortedKeysInInsertionOrderV1(string(d), record) }, func(d []byte) { sortedKeysInInsertionOrder(string(d), record) })
}
