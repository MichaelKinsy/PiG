package durable

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

func concurrentDoc(n int) JsonObject {
	object := delta.NewJsonObject(1)
	object.Set("a", "v"+strconv.Itoa(n))
	return object
}

// A typed document state attached while another goroutine publishes has the erased state's snapshot and every later
// publication: the public subscription's 100-delivery window skipped cursors (the attached typed state then failed
// "cursor has a gap") and its hydration could run after Subscribe returned (an empty snapshot). Reported by review
// of 18672f860a.
func TestTypedDocumentStateAttachesGapFreeUnderConcurrentPublication(t *testing.T) {
	source := &manualSource{snapshot: concurrentDoc(0)}
	erased, err := chord.AttachReplicatedStateSource[JsonObject](source, chord.ReplicatedStateSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for n := 1; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			source.cursor = n
			source.emit(n, concurrentDoc(n))
		}
	})
	var nilSnapshots, reports atomic.Int64
	for range 20000 {
		typedSource := &typedStateSource[typedStateDoc]{state: erased, onError: func(err error) {
			if reports.Add(1) <= 3 {
				t.Logf("source onError: %v", err)
			}
		}}
		typed, err := chord.AttachReplicatedStateSource[*typedStateDoc](typedSource, chord.ReplicatedStateSourceOptions{OnError: func(err error) {
			if reports.Add(1) <= 3 {
				t.Logf("typed OnError: %v", err)
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if typed.Value() == nil {
			nilSnapshots.Add(1)
		}
		typedSource.mu.Lock()
		typedSource.disposed = true // keep the erased state alive for the next round
		typedSource.mu.Unlock()
		typedSource.unsubscribe()
	}
	close(stop)
	wg.Wait()
	if nilSnapshots.Load() != 0 || reports.Load() != 0 {
		t.Fatalf("typed attachments with an empty snapshot: %d; contract failures reported: %d", nilSnapshots.Load(), reports.Load())
	}
}
