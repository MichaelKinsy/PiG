// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Ports packages/durable/src/harness/harness.ts observeConversation and observeTaskGraph over the core's watch frames: the
// observer gets each frame as the core's published notice arrives, behind session.GuardObserver, so a panicking observer is
// reported, detached and ended with its error.

// ObserveConversation attaches a synchronous observer of a conversation's structural view.
func (s *coreSession) ObserveConversation(ctx context.Context, id durable.ConversationId, observer durable.Observer[harness.ConversationView], window *harness.ConversationWindow) (durable.Observation[harness.ConversationView], error) {
	m, err := openWatch(ctx, s, viewTarget(id, window), typed[harness.ConversationView], viewReducer())
	if err != nil || m == nil {
		return nil, err
	}
	return observeOf(ctx, m, observer)
}

// ObserveTaskGraph attaches a synchronous observer of the task graph.
func (s *coreSession) ObserveTaskGraph(ctx context.Context, observer durable.Observer[harness.TaskGraph]) (durable.Observation[harness.TaskGraph], error) {
	m, err := openWatch(ctx, s, map[string]any{"kind": "graph"}, typed[harness.TaskGraph], reduceTaskGraph)
	if err != nil || m == nil {
		return nil, err
	}
	return observeOf(ctx, m, observer)
}

// observeOf binds a guarded observer to an opened mount. ctx governs only the attachment.
func observeOf[T any](ctx context.Context, m *mountSource[T], observer durable.Observer[T]) (durable.Observation[T], error) {
	var once sync.Once
	guarded := session.GuardObserver(observer, func() { once.Do(func() { m.hub.release(m.id); m.closeCore() }) }, m.hub.report)
	m.hub.bind(m.id, func(f rawFrame) {
		if value, ok := m.advance(f); ok {
			guarded.Advance(m.hub.deliveryContext(), chord.WatchChange[T]{Value: value, Ops: f.ops})
		}
	}, func() { guarded.End(nil) })
	if err := ctx.Err(); err != nil {
		guarded.Detach()
		return nil, context.Cause(ctx)
	}
	return session.NewObservation(m.first, guarded), nil
}
