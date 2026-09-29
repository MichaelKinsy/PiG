package services

// Ports packages/coding-agent/src/experimental/services/transcript.ts

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// TranscriptState publishes a coherent lane snapshot and the source event. Hydration and rebases have a null event; event JSON preserves the closed upstream LaneWatchEvent wire shape.
type TranscriptState struct {
	Snapshot *agentharness.LaneSnapshot `json:"snapshot"`
	Event    json.RawMessage            `json:"event"`
}

// Transcript is the main-lane state replicated through Chord's operation stream.
type Transcript interface {
	State() pico3.ReplicatedStateOf[*TranscriptState]
}

var TranscriptDefinition = pico3.DefineService[Transcript]("pi.transcript")

func init() {
	chord.RegisterServiceView(TranscriptDefinition, func(resolve func() (Transcript, error)) Transcript { return transcriptView{resolve: resolve} })
	chord.RegisterRemoteClient(TranscriptDefinition, func(service *chord.RemoteService) Transcript { return remoteTranscript{service: service} })
}

type transcriptView struct{ resolve func() (Transcript, error) }

func (view transcriptView) State() pico3.ReplicatedStateOf[*TranscriptState] {
	return chord.StateView(func() (pico3.ReplicatedStateOf[*TranscriptState], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

type remoteTranscript struct{ service *chord.RemoteService }

func (remote remoteTranscript) State() pico3.ReplicatedStateOf[*TranscriptState] {
	replica, err := remote.service.State("state")
	if err != nil {
		panic(err)
	}
	return chord.TypedReplica[*TranscriptState](replica)
}
