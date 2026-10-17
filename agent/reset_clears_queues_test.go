package agent

import "testing"

// agent.ts:355-368 reset(): besides the transcript it empties the follow-up queue and the steering queue, so a message queued before the reset is never delivered after it.
// mutation-checked: leaving the follow-up queue untouched in Agent.Reset, or HasQueuedMessages ignoring the follow-up queue, fails it.
func TestAgentResetEmptiesBothQueues(t *testing.T) {
	a := newTestAgentForNextTurn(t)
	a.Steer(userMsg("steer"))
	a.FollowUp(userMsg("follow"))
	if !a.HasQueuedMessages() {
		t.Fatal("queued messages are not reported before the reset")
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}
	if a.HasQueuedMessages() || len(a.PeekQueuedMessages()) != 0 {
		t.Fatalf("after Reset: HasQueuedMessages=%v, preview=%v; want both queues empty", a.HasQueuedMessages(), a.PeekQueuedMessages())
	}
	a.FollowUp(userMsg("later"))
	if !a.HasQueuedMessages() {
		t.Fatal("a follow-up alone is not reported as a queued message")
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}
	if a.HasQueuedMessages() {
		t.Fatal("a follow-up queued alone survived Reset")
	}
}
