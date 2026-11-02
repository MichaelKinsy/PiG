//go:build !race

package delta

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// A text that grew by AppendText shares its buffer with the revisions that hold it, and with nothing else: once the key is deleted or replaced and no revision holds the string, the tracker keeps none of its bytes. The heap is measured after GC with the tracker still alive.

const retentionCommits, retentionChunk = 4096, 1 << 10

// retainedAfterRelease is the heap a tracker holds after streaming texts into keys and then releasing them with release, less the heap before.
func retainedAfterRelease(tb testing.TB, keys int, release func(state *Object, key string)) int64 {
	tb.Helper()
	before := heapAfterGC()
	tracker := Track(JsonObjectOf("a", "a"))
	chunk := strings.Repeat("x", retentionChunk)
	names := make([]string, keys)
	for index := range names {
		names[index] = fmt.Sprintf("text%d", index)
		change := tracker.BeginChange()
		_ = change.State().Set(names[index], "")
		prepared, err := change.Prepare()
		if err != nil {
			tb.Fatal(err)
		}
		if err := tracker.Adopt(prepared); err != nil {
			tb.Fatal(err)
		}
	}
	for range retentionCommits / keys {
		for _, name := range names {
			change := tracker.BeginChange()
			if err := AppendText(change.State(), name, chunk); err != nil {
				tb.Fatal(err)
			}
			prepared, err := change.Prepare()
			if err != nil {
				tb.Fatal(err)
			}
			if err := tracker.Adopt(prepared); err != nil {
				tb.Fatal(err)
			}
		}
	}
	change := tracker.BeginChange()
	for _, name := range names {
		release(change.State(), name)
	}
	prepared, err := change.Prepare()
	if err != nil {
		tb.Fatal(err)
	}
	if err := tracker.Adopt(prepared); err != nil {
		tb.Fatal(err)
	}
	retained := int64(heapAfterGC()) - int64(before)
	runtime.KeepAlive(tracker)
	return retained
}

func TestTrackerAppendTextReleasesBuffersOfReleasedKeys(t *testing.T) {
	const budget = 1 << 20 // the streamed text is 4 MiB per key set; the tracker keeps a few KiB of bookkeeping
	for _, test := range []struct {
		name    string
		keys    int
		release func(state *Object, key string)
	}{
		{"delete one key", 1, func(state *Object, key string) { state.Delete(key) }},
		{"replace one key", 1, func(state *Object, key string) { _ = state.Set(key, "") }},
		{"delete more keys than a tracker keeps buffers for", 128, func(state *Object, key string) { state.Delete(key) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if retained := retainedAfterRelease(t, test.keys, test.release); retained > budget {
				t.Fatalf("a tracker that streamed %d KiB and released it holds %d KiB, want at most %d KiB", retentionCommits*retentionChunk>>10, retained>>10, budget>>10)
			}
		})
	}
}
