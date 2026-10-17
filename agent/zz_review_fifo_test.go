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
	a := mustNewAgent(AgentOptions{})
	if _, err := a.testHost().runPreparedToolCall(WithMutationTicket(ctx, ticket), preparedToolCall{}); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("aborted prepared call stranded reserved queue ticket; later same-path calls wait forever")
	}
}
