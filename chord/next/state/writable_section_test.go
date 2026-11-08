package state_test

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// sectionOwner is the WritableOwner of a host: Do runs work as one section of its loop (a lock here), and OnLoop tells
// whether the caller is in one already.
type sectionOwner struct {
	attempt
	mu      sync.Mutex
	inside  bool
	entered int
}

func (o *sectionOwner) Do(work func()) {
	if o.inside {
		work()
		return
	}
	o.mu.Lock()
	o.inside = true
	o.entered++
	defer func() { o.inside = false; o.mu.Unlock() }()
	work()
}

func (o *sectionOwner) OnLoop() bool { return o.inside }

// Set from outside a section enters one; from inside it runs in the section it is in, entering none.
func TestWritableSetEntersASectionOnlyOutsideOne(t *testing.T) {
	h := newHarness(t, nil)
	owner := &sectionOwner{attempt: attempt{ctx: context.Background()}}
	writable := state.NewWritableSource[float64](1, owner, h.batch)
	if err := writable.Set(2); err != nil || owner.entered != 1 {
		t.Fatalf("outside a section: %v, entered %d", err, owner.entered)
	}
	owner.Do(func() {
		if err := writable.Set(3); err != nil {
			t.Error(err)
		}
	})
	if owner.entered != 2 || writable.Value() != 3 {
		t.Fatalf("inside a section: entered %d, value %v", owner.entered, writable.Value())
	}
}

// BenchmarkWritableSetInSection is a Set made from inside a section of the owner's loop: the fast path, no closure.
func BenchmarkWritableSetInSection(b *testing.B) {
	h := newHarness(b, nil)
	owner := &sectionOwner{attempt: attempt{ctx: context.Background()}}
	writable := state.NewWritableSource[float64](1, owner, h.batch)
	b.ReportAllocs()
	owner.Do(func() {
		for i := range b.N {
			if err := writable.Set(float64(i + 2)); err != nil {
				b.Fatal(err)
			}
		}
	})
}
