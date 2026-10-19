// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// Admitted is the number of calls admitted before Close that have not returned (tests wait for admission instead of sleeping).
func (s *Session) Admitted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admitted
}

// CloseBegan reports that Close began: admission is sealed.
func (s *Session) CloseBegan() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ClosedOr is closedOr: the error a wait reports once Close ended it.
func (s *Session) ClosedOr(ctx context.Context, err error) error { return s.closedOr(ctx, err) }

// IdleLive is the decision WaitForIdle makes from one task-graph read: whether work is live in the scope of root (nil: the whole Harness).
func (s *Session) IdleLive(ctx context.Context, root *durable.ConversationId) (bool, error) {
	raw, err := s.client.API(ctx, "taskGraph", nil)
	if err != nil {
		return false, err
	}
	var graph taskGraphState
	if err := json.Unmarshal(raw, &graph); err != nil {
		return false, err
	}
	return graph.liveIn(root), nil
}

// Client is the Session's request client, through which tests drive the transaction channel directly.
func (s *Session) Client() *Client { return s.client }

// HostCounters are the measurements of the Session's host: the reads the core ran through pig.read and the rows they returned.
func (s *Session) HostCounters() sqlhost.Counters { return s.host.Counters() }

// Session names the core-backed session for the external tests, which reach its counters and Closed through it.
type Session = coreSession

// OpenSession is Open with the concrete session the external tests inspect.
func OpenSession(ctx context.Context, cfg SessionConfig) (*Session, error) { return open(ctx, cfg) }
