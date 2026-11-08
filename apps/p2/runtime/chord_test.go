package runtime

import (
	"errors"
	goruntime "runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// recordedSource is what followedChanges advances: the calls applied in the order they arrived, as the host's loop applies
// them.
type recordedSource struct {
	mu    sync.Mutex
	calls []chord.WatchCall[any]
}

func (r *recordedSource) advance(call chord.WatchCall[any]) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *recordedSource) last(t *testing.T) chord.WatchCall[any] {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("the source was not advanced")
	}
	return r.calls[len(r.calls)-1]
}

// A goroutine that read the native's state, then lost the loop to the goroutine that changed it again, must not leave its
// stale read as the source's last value (the login flake: cancel against the goroutine closing the prompt).
func TestFollowedChangesLastValueIsTheStateAfterTheLastChange(t *testing.T) {
	var mu sync.Mutex
	state := "working"
	reads := make(chan struct{})
	release := make(chan struct{})
	first := true
	source := &recordedSource{}
	changes := followedChanges{
		read: func() (delta.JsonValue, error) {
			mu.Lock()
			value, hold := state, first
			first = false
			mu.Unlock()
			if hold {
				// The first reader has the stale state in hand while the other change is made and advanced.
				close(reads)
				<-release
			}
			return value, nil
		},
		advance: source.advance,
	}
	stale := make(chan struct{})
	go func() {
		defer close(stale)
		changes.changed()
	}()
	<-reads
	mu.Lock()
	state = "cancelled"
	mu.Unlock()
	changes.changed()
	close(release)
	<-stale
	if got := source.last(t); got.Kind != chord.WatchCallValue || got.Value != "cancelled" {
		t.Fatalf("the source's last call is %+v, want the value cancelled", got)
	}
}

// Every change reaches the source before changed returns, when nothing else changes.
func TestFollowedChangesAdvancesOnceForOneChange(t *testing.T) {
	source := &recordedSource{}
	changes := followedChanges{read: func() (delta.JsonValue, error) { return "x", nil }, advance: source.advance}
	changes.changed()
	if len(source.calls) != 1 || source.calls[0].Value != "x" {
		t.Fatalf("calls %+v, want one value x", source.calls)
	}
}

// A failed read fails the source until the next change.
func TestFollowedChangesFailsTheSourceOnAFailedRead(t *testing.T) {
	source := &recordedSource{}
	boom := errors.New("boom")
	changes := followedChanges{read: func() (delta.JsonValue, error) { return nil, boom }, advance: source.advance}
	changes.changed()
	if got := source.last(t); got.Kind != chord.WatchCallFailed || got.Error != boom {
		t.Fatalf("the source's last call is %+v, want a failure", got)
	}
}

// Many goroutines changing the native at once, round after round: whatever order their reads and advances interleave in,
// the last call is the state after the round's changes.
func TestFollowedChangesStressLastValueIsTheFinalState(t *testing.T) {
	var mu sync.Mutex
	counter := 0
	source := &recordedSource{}
	changes := followedChanges{
		read: func() (delta.JsonValue, error) {
			mu.Lock()
			value := counter
			mu.Unlock()
			// Yield with the state in hand: another goroutine's change and advance can come before this one's.
			goruntime.Gosched()
			return value, nil
		},
		advance: source.advance,
	}
	for round := 1; round <= 500; round++ {
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				mu.Lock()
				counter++
				mu.Unlock()
				changes.changed()
			})
		}
		workers.Wait()
		if got := source.last(t); got.Value != round*8 {
			t.Fatalf("round %d: the source's last value is %v, want %d", round, got.Value, round*8)
		}
	}
}
