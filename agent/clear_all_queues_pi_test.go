package agent

import (
	"reflect"
	"testing"
)

// Pi agent.ts:308-322 clearSteeringQueue, clearFollowUpQueue and clearAllQueues, and agent.test.ts:944-957 (peekQueuedMessages returns the steering
// selection until clearSteeringQueue empties it, then the follow-up). clearAllQueues empties both queues, so nothing is left to peek, hasQueuedMessages
// is false, and the queues accept messages again afterwards.
func TestClearAllQueuesEmptiesBothQueuesAndTheyAcceptMessagesAgain(t *testing.T) {
	steering, followUp := userMsg("steering"), userMsg("follow-up")

	// agent.test.ts:950-957: clearing the steering queue exposes the follow-up; clearing that one too leaves nothing.
	a := newTestAgentForNextTurn(t)
	a.Steer(steering)
	a.FollowUp(followUp)
	if got := a.PeekQueuedMessages(); !reflect.DeepEqual(got, []AgentMessage{steering}) {
		t.Fatalf("peek = %v, want the steering message", got)
	}
	a.ClearSteeringQueue()
	if got := a.PeekQueuedMessages(); !reflect.DeepEqual(got, []AgentMessage{followUp}) {
		t.Fatalf("peek after ClearSteeringQueue = %v, want the follow-up", got)
	}
	a.ClearFollowUpQueue()
	if got := a.PeekQueuedMessages(); len(got) != 0 || a.HasQueuedMessages() {
		t.Fatalf("after ClearFollowUpQueue peek = %v, queued = %v, want nothing", got, a.HasQueuedMessages())
	}

	// clearAllQueues: both queues hold messages; one call empties both (agent.ts:319-322).
	a = newTestAgentForNextTurn(t)
	a.Steer(steering)
	a.Steer(userMsg("steering 2"))
	a.FollowUp(followUp)
	a.ClearAllQueues()
	if got := a.PeekQueuedMessages(); len(got) != 0 || a.HasQueuedMessages() {
		t.Fatalf("after ClearAllQueues peek = %v, queued = %v, want nothing", got, a.HasQueuedMessages())
	}
	pendingSteering, pendingFollowUp := a.PendingMessages()
	if len(pendingSteering) != 0 || len(pendingFollowUp) != 0 {
		t.Fatalf("after ClearAllQueues steering=%v followUp=%v, want both empty", pendingSteering, pendingFollowUp)
	}

	// An emptied queue is not closed: the next messages queue and peek in Pi's order (steering first).
	a.FollowUp(followUp)
	a.Steer(steering)
	if got := a.PeekQueuedMessages(); !reflect.DeepEqual(got, []AgentMessage{steering}) {
		t.Fatalf("peek after refilling = %v, want the steering message", got)
	}
	a.ClearAllQueues()
	a.ClearAllQueues()
	if a.HasQueuedMessages() {
		t.Fatal("a repeated ClearAllQueues on empty queues left messages queued")
	}
}
