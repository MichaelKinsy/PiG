package services

// Ports packages/coding-agent/src/experimental/services/transcript.ts

import (
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// Transcript is the root conversation's durable view, replicated through Chord's operation stream: the active entries and its live, inbox, agent and usage documents.
type Transcript interface {
	State() chord.ReplicatedStateOf[ConversationView]
}

var TranscriptDefinition = chord.DefineService[Transcript]("pi.transcript")

func init() {
	chord.RegisterServiceView(TranscriptDefinition, func(resolve func() (Transcript, error)) Transcript { return transcriptView{resolve: resolve} })
	chord.RegisterRemoteClient(TranscriptDefinition, func(service *chord.RemoteService) Transcript { return remoteTranscript{service: service} })
}

type transcriptView struct{ resolve func() (Transcript, error) }

func (view transcriptView) State() chord.ReplicatedStateOf[ConversationView] {
	return chord.StateView(func() (chord.ReplicatedStateOf[ConversationView], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

type remoteTranscript struct{ service *chord.RemoteService }

func (remote remoteTranscript) State() chord.ReplicatedStateOf[ConversationView] {
	replica, err := remote.service.State("state")
	if err != nil {
		panic(err)
	}
	return chord.TypedReplica[ConversationView](replica)
}
