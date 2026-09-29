package services

// Ports packages/coding-agent/src/experimental/services/transcript-provider.ts

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/agent/harness/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// TranscriptWatchLane is the durable observation boundary consumed by the Transcript provider.
type TranscriptWatchLane interface {
	Watch(context.Context) (agentharness.WatchHandle[agentharness.LaneSnapshot], error)
}

type transcriptRebase struct {
	done chan struct{}
	err  error
}

// TranscriptRuntime owns a lane watch and any in-flight rebase. Dispose joins the rebase before unsubscribing; rebase errors also reach subsequent event delivery.
type TranscriptRuntime struct {
	Service     Transcript
	lane        TranscriptWatchLane
	state       *chord.MutableReplicatedState[*TranscriptState]
	mu          sync.Mutex
	watch       agentharness.WatchHandle[agentharness.LaneSnapshot]
	rebase      *transcriptRebase
	rebaseError error
}

type transcriptService struct {
	state *chord.MutableReplicatedState[*TranscriptState]
}

func (service transcriptService) State() pico3.ReplicatedStateOf[*TranscriptState] {
	return service.state
}

func CreateTranscriptService(lane TranscriptWatchLane) (*TranscriptRuntime, error) {
	state, err := chord.NewReplicatedState(&TranscriptState{})
	if err != nil {
		return nil, err
	}
	return &TranscriptRuntime{Service: transcriptService{state: state}, lane: lane, state: state}, nil
}

func (r *TranscriptRuntime) Activate() error {
	r.mu.Lock()
	opened := r.watch != nil
	r.mu.Unlock()
	if opened {
		return errors.New("Transcript service is already active")
	}
	watch, err := r.lane.Watch(context.Background())
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.watch = watch
	r.mu.Unlock()
	snapshot := watch.Snapshot()
	if err := r.state.Replace(context.Background(), &TranscriptState{Snapshot: &snapshot}); err != nil {
		return err
	}
	return watch.Start(r.onEvent)
}

func (r *TranscriptRuntime) onEvent(ctx context.Context, event agentharness.HarnessEvent) error {
	r.mu.Lock()
	failure := r.rebaseError
	r.mu.Unlock()
	if failure != nil {
		return failure
	}
	forwarded, err := transcriptWatchEvent(event)
	if err != nil || forwarded == nil {
		return err
	}
	needsRebase := false
	err = r.state.Change(ctx, func(draft *TranscriptState) error {
		if draft.Snapshot == nil {
			return errors.New("Transcript service is not active")
		}
		needsRebase = runtime.ReduceLaneSnapshot(draft.Snapshot, event) == "rebase"
		draft.Event = forwarded
		return nil
	})
	if err != nil {
		return err
	}
	if needsRebase {
		r.scheduleRebase(ctx)
	}
	return nil
}

func (r *TranscriptRuntime) scheduleRebase(ctx context.Context) {
	r.mu.Lock()
	if r.rebase != nil || r.watch == nil {
		r.mu.Unlock()
		return
	}
	watch := r.watch
	pending := &transcriptRebase{done: make(chan struct{})}
	r.rebase = pending
	r.mu.Unlock()
	// The navigation publication precedes the owned asynchronous resnapshot; Dispose joins it without cancelling the lane's operation.
	go func() {
		snapshot, err := watch.Resnapshot(ctx)
		if err == nil {
			err = r.state.Replace(ctx, &TranscriptState{Snapshot: &snapshot})
		}
		r.mu.Lock()
		pending.err = err
		if err != nil {
			r.rebaseError = err
		}
		if r.rebase == pending {
			r.rebase = nil
		}
		close(pending.done)
		r.mu.Unlock()
	}()
}

func (r *TranscriptRuntime) Dispose() error {
	r.mu.Lock()
	pending := r.rebase
	r.mu.Unlock()
	var failure error
	if pending != nil {
		<-pending.done
		failure = pending.err
	}
	r.mu.Lock()
	watch := r.watch
	r.watch = nil
	r.mu.Unlock()
	if watch != nil {
		watch.Unsubscribe()
	}
	return failure
}

func CreateTranscriptServiceFacet(lane TranscriptWatchLane) chord.Facet {
	return chord.DefineFacet(chord.Facet{Id: "@pi/transcript", Setup: func(env *chord.FacetEnvironment) error {
		runtime, err := CreateTranscriptService(lane)
		if err != nil {
			return err
		}
		if err := chord.ProvideService(env, TranscriptDefinition, runtime.Service); err != nil {
			return err
		}
		if err := env.OnActivate(func(context.Context) error { return runtime.Activate() }); err != nil {
			return err
		}
		return env.Own(func(context.Context) error { return runtime.Dispose() })
	}})
}

func transcriptWatchEvent(event agentharness.HarnessEvent) (json.RawMessage, error) {
	switch event.Type() {
	case agentharness.EventHandlerError, agentharness.EventTurnStart, agentharness.EventTurnEnd, agentharness.EventValueUpdate, agentharness.EventLaneCreated:
		return nil, nil
	case agentharness.EventConfigUpdate:
		property := event.Payload.(agentharness.ConfigUpdatePayload).Property
		if property != agentharness.ConfigModel && property != agentharness.ConfigThinkingLevel && property != agentharness.ConfigActiveTools {
			return nil, nil
		}
	case agentharness.EventMessageUpdate:
		if event.Payload.(agentharness.MessageUpdatePayload).Message.Role() != agent.RoleAssistant {
			return nil, errors.New("Harness message_update did not carry an assistant message")
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return nil, err
		}
		delete(fields, "event")
		return json.Marshal(fields)
	}
	return json.Marshal(event)
}
