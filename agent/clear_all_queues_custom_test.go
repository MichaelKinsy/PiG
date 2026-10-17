package agent

import "testing"

// agent.ts:308-322: clearSteeringQueue, clearFollowUpQueue and clearAllQueues empty their queue outright (PendingMessageQueue.clear), so a custom message such as a bash-execution record is discarded with the user messages. The ported tests queue user messages only. ClearQueuedUserMessages is the one that keeps custom messages (D135), so each clear method must be told apart from it.
//
// mutation-checked: ClearSteeringQueue or ClearFollowUpQueue delegating to the user-messages-only clear leaves the custom message queued.
func TestClearQueuesDiscardCustomMessagesWhileClearQueuedUserMessagesKeepsThem(t *testing.T) {
	custom := func() AgentMessage {
		return AgentMessage{Custom: map[string]any{"role": "bashExecution", "command": "ls"}}
	}
	fill := func(a *Agent) {
		a.Steer(userMsg("s"))
		a.Steer(custom())
		a.FollowUp(userMsg("f"))
		a.FollowUp(custom())
	}
	counts := func(a *Agent) (steering, followUp int) {
		s, f := a.PendingMessages()
		return len(s), len(f)
	}
	for _, tc := range []struct {
		name                       string
		clear                      func(*Agent)
		wantSteering, wantFollowUp int
	}{
		{"ClearSteeringQueue", (*Agent).ClearSteeringQueue, 0, 2},
		{"ClearFollowUpQueue", (*Agent).ClearFollowUpQueue, 2, 0},
		{"ClearAllQueues", (*Agent).ClearAllQueues, 0, 0},
		{"ClearQueuedUserMessages", (*Agent).ClearQueuedUserMessages, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAgentForNextTurn(t)
			fill(a)
			tc.clear(a)
			if steering, followUp := counts(a); steering != tc.wantSteering || followUp != tc.wantFollowUp {
				t.Fatalf("after %s: steering=%d followUp=%d, want %d and %d", tc.name, steering, followUp, tc.wantSteering, tc.wantFollowUp)
			}
			if wantQueued := tc.wantSteering+tc.wantFollowUp > 0; a.HasQueuedMessages() != wantQueued {
				t.Fatalf("after %s: HasQueuedMessages = %v, want %v", tc.name, a.HasQueuedMessages(), wantQueued)
			}
		})
	}
}
