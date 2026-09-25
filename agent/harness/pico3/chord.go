package pico3

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ChordViewBridgeOptions controls the bounded queue and failure notification.
type ChordViewBridgeOptions struct {
	Capacity  *int
	OnFailure func(error)
}

// ChordViewBridge owns the off-line publisher. Close discards queued envelopes;
// Wait joins any publication already in progress, including failure reporting.
type ChordViewBridge struct {
	View      MutableReplicatedState
	watch     *Watch
	ctx       context.Context
	capacity  int
	onFailure func(error)
	mu        sync.Mutex
	queue     []*Envelope
	closed    bool
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
}

// AttachChordView captures and subscribes atomically, then publishes each
// envelope off the Session line through the caller's Chord state factory.
func AttachChordView(ctx context.Context, conversation *ConversationHandle, createState func(PublishedConversationView) MutableReplicatedState, opts ChordViewBridgeOptions) (*ChordViewBridge, error) {
	watch, err := conversation.Watch(ctx)
	if err != nil {
		return nil, err
	}
	initial := cloneObject(watch.View)
	initial["commit"] = JsonObject{"events": []any{}}
	view := createState(initial)
	bridge, err := bridgeWatch(ctx, watch, view, opts)
	if err != nil {
		watch.Stop()
	}
	return bridge, err
}

func bridgeWatch(ctx context.Context, watch *Watch, view MutableReplicatedState, opts ChordViewBridgeOptions) (*ChordViewBridge, error) {
	capacity := WatchCapacity
	if opts.Capacity != nil {
		capacity = *opts.Capacity
	}
	if capacity < 1 || int64(capacity) > 9007199254740991 {
		return nil, errors.New("Chord view queue capacity must be positive")
	}
	bridge := &ChordViewBridge{View: view, watch: watch, ctx: ctx, capacity: capacity, onFailure: opts.OnFailure, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go bridge.drain()
	watch.Start(bridge.enqueue)
	return bridge, nil
}

// Closed reports whether publication has stopped.
func (bridge *ChordViewBridge) Closed() bool {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.closed
}

// Close stops the watch and discards pending publications. It may be called
// reentrantly from the state's publication callback or OnFailure.
func (bridge *ChordViewBridge) Close() { bridge.close() }

func (bridge *ChordViewBridge) close() bool {
	bridge.mu.Lock()
	if bridge.closed {
		bridge.mu.Unlock()
		return false
	}
	bridge.closed = true
	bridge.queue = nil
	close(bridge.stop)
	bridge.mu.Unlock()
	bridge.watch.Stop()
	return true
}

// Wait joins the bridge's owned publisher after Close or publication failure.
func (bridge *ChordViewBridge) Wait(ctx context.Context) error {
	select {
	case <-bridge.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (bridge *ChordViewBridge) fail(err error) {
	if !bridge.close() {
		return
	}
	defer func() { _ = recover() }() // upstream: agent/src/harness/pico3/chord.ts:onFailure
	if bridge.onFailure != nil {
		bridge.onFailure(err)
	}
}

func (bridge *ChordViewBridge) enqueue(envelope *Envelope) {
	bridge.mu.Lock()
	if bridge.closed {
		bridge.mu.Unlock()
		return
	}
	if len(bridge.queue) >= bridge.capacity {
		bridge.mu.Unlock()
		bridge.fail(fmt.Errorf("Pico-to-Chord view queue exceeded %d envelopes", bridge.capacity))
		return
	}
	bridge.queue = append(bridge.queue, envelope)
	bridge.mu.Unlock()
	select {
	case bridge.wake <- struct{}{}:
	default:
	}
}

func (bridge *ChordViewBridge) pop() *Envelope {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.closed || len(bridge.queue) == 0 {
		return nil
	}
	envelope := bridge.queue[0]
	bridge.queue[0] = nil
	bridge.queue = bridge.queue[1:]
	return envelope
}

func (bridge *ChordViewBridge) drain() {
	defer close(bridge.done)
	for {
		select {
		case <-bridge.stop:
			return
		case <-bridge.wake:
			for envelope := bridge.pop(); envelope != nil; envelope = bridge.pop() {
				if err := bridge.publish(envelope); err != nil {
					bridge.fail(err)
					return
				}
			}
		}
	}
}

func (bridge *ChordViewBridge) publish(envelope *Envelope) (err error) {
	defer recoverInto(&err)
	return bridge.View.Change(bridge.ctx, func(draft PublishedConversationView) error {
		if err := applyTracked(draft, envelope.Ops); err != nil {
			return err
		}
		events := make([]any, len(envelope.Events))
		for index, event := range envelope.Events {
			events[index] = cloneObject(event)
		}
		asObject(draft["commit"])["events"] = events
		return nil
	})
}
