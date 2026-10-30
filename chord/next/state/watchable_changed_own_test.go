package state_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Pi's `Source.changed()` reads `this.watched`, a getter a subclass may override (the host's member source does: it is
// watched while it has passed watches on). Source.Changed reads the embedder's Watched, not the base one.

type overridingSource struct {
	docSource
	delivered int
}

func (s *overridingSource) Watched() bool { return true }

func (s *overridingSource) Deliver(context.Context) error {
	s.delivered++
	return nil
}

func TestSourceChangedReadsTheEmbeddersWatched(t *testing.T) {
	h := newHarness(t, nil)
	source := &overridingSource{}
	source.Init(h.batch, source)
	if err := h.batch.Within(func() error {
		source.Changed()
		return nil
	}, background); err != nil {
		t.Fatalf("Within: %v", err)
	}
	h.delivered()
	if source.delivered != 1 {
		t.Fatalf("Deliver ran %d times for a change of a source its embedder reports watched, want 1", source.delivered)
	}
	var _ state.SourceHandle = source
}
