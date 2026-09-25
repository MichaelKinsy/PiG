package agent

import (
	"context"
	"testing"
)

func TestReviewCancelledPreparedCallReleasesReservation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	released := false
	ticket := &MutationTicket{Wait: func() {}, Release: func() { released = true }}
	a := NewAgent(AgentOptions{})
	a.runPreparedToolCall(WithMutationTicket(ctx, ticket), preparedToolCall{})
	if !released {
		t.Fatal("aborted prepared call stranded reserved queue ticket; later same-path calls wait forever")
	}
}
