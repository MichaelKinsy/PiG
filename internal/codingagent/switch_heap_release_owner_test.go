package codingagent

import (
	"testing"
)

// pig additive (D172, D112): the heap return after a Session replacement runs on a task the mode owns, never on the owner loop: while the
// return is held, renderCurrentSessionState (the owner loop's call) has already returned, and the return runs once the owner has left. A
// switch that arrives while the return runs is coalesced into it (one return, two requests). An ordinary repaint of the transcript
// (rebuildChatFromSession, which is upstream's rebuildChatFromMessages) requests none.
func TestSwitchHeapReleaseRunsOffTheOwnerLoopAndNotOnAnOrdinaryRepaint(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("kept prompt"))
	started, hold := make(chan struct{}), make(chan struct{})
	calls := countHeapReturns(t)
	previous := returnHeapToOS
	returnHeapToOS = func() {
		previous()
		close(started)
		<-hold
	}

	m.rebuildChatFromSession()
	if got := m.switchHeapReleases.Load(); got != 0 {
		t.Fatalf("an ordinary repaint requested %d heap releases, want 0", got)
	}

	m.renderCurrentSessionState() // returns while the return below is held: it is not run by the caller
	<-started
	m.renderCurrentSessionState() // a switch while the return runs is covered by it
	close(hold)
	m.backgroundTasks.Wait()
	if got, requests := calls.Load(), m.switchHeapReleases.Load(); got != 1 || requests != 2 {
		t.Fatalf("heap returns = %d, requests = %d; want 1 return covering 2 requests", got, requests)
	}
}
