package services_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type disposeCountingView struct {
	*chord.MutableReplicatedState[services.ConversationView]
	disposed atomic.Int32
}

func (view *disposeCountingView) Dispose() { view.disposed.Add(1) }

type viewStateConversation struct {
	view *disposeCountingView
	err  error
}

func (conversation viewStateConversation) ViewState(context.Context) (services.TranscriptViewState, error) {
	if conversation.err != nil {
		return nil, conversation.err
	}
	return conversation.view, nil
}

// packages/coding-agent/src/experimental/services/transcript-provider.ts:6-13: the facet attaches the view state before it exists
// (a failed attach fails creation), owns it, and disposes it exactly once with the host.
func TestTranscriptServiceFacetOwnsAndDisposesTheViewState(t *testing.T) {
	cause := errors.New("view unavailable")
	if _, err := services.CreateTranscriptServiceFacet(t.Context(), viewStateConversation{err: cause}); !errors.Is(err, cause) {
		t.Fatalf("attach failure = %v, want %v", err, cause)
	}

	state, err := chord.NewReplicatedState(services.ConversationView{})
	if err != nil {
		t.Fatal(err)
	}
	view := &disposeCountingView{MutableReplicatedState: state}
	facet, err := services.CreateTranscriptServiceFacet(t.Context(), viewStateConversation{view: view})
	if err != nil {
		t.Fatal(err)
	}
	if facet.Id != "@pi/transcript" {
		t.Fatalf("facet id = %q", facet.Id)
	}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{facet}})
	if err != nil {
		t.Fatal(err)
	}
	if view.disposed.Load() != 0 {
		t.Fatal("the view state was disposed while the host runs")
	}
	if err := host.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if view.disposed.Load() != 1 {
		t.Fatalf("view state disposed %d times, want 1", view.disposed.Load())
	}
}
