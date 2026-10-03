package services

// Ports packages/coding-agent/src/experimental/services/transcript-provider.ts

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// TranscriptViewState is the conversation's attached durable view state. The durable Harness publishes exact operations per commit, so the worker keeps no reducer of its own. Dispose detaches it.
type TranscriptViewState interface {
	chord.ReplicatedStateOf[ConversationView]
	Dispose()
}

// TranscriptConversation is the observation boundary the Transcript provider consumes from the root conversation.
type TranscriptConversation interface {
	ViewState(context.Context) (TranscriptViewState, error)
}

type transcriptService struct{ state TranscriptViewState }

func (service transcriptService) State() chord.ReplicatedStateOf[ConversationView] {
	return service.state
}

// CreateTranscriptServiceFacet serves the conversation's durable view state. The facet owns the attached state and disposes it with the host.
func CreateTranscriptServiceFacet(ctx context.Context, conversation TranscriptConversation) (chord.Facet, error) {
	state, err := conversation.ViewState(ctx)
	if err != nil {
		return chord.Facet{}, err
	}
	return chord.DefineFacet(chord.Facet{Id: "@pi/transcript", Setup: func(env *chord.FacetEnvironment) error {
		if err := env.Own(func(context.Context) error { state.Dispose(); return nil }); err != nil {
			return err
		}
		return chord.ProvideService(env, TranscriptDefinition, Transcript(transcriptService{state: state}))
	}}), nil
}
