package coding

import (
	"testing"
	"testing/synctest"
)

// HoldAgentEvents stops the Agent at the Session's listener until release; release is idempotent and a later hold
// does not undo an earlier release.
func TestHoldAgentEventsBlocksTheListenerUntilReleased(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &Session{}
		s.agentEventHold.wait() // no hold: returns at once
		release := s.HoldAgentEvents()
		passed := make(chan struct{})
		go func() {
			s.agentEventHold.wait()
			close(passed)
		}()
		synctest.Wait()
		select {
		case <-passed:
			t.Fatal("an event passed the hold before release")
		default:
		}
		release()
		synctest.Wait()
		select {
		case <-passed:
		default:
			t.Fatal("release did not let the held event through")
		}
		release()
		second := s.HoldAgentEvents()
		release() // the first hold's release does not end the second
		held := make(chan struct{})
		go func() {
			s.agentEventHold.wait()
			close(held)
		}()
		synctest.Wait()
		select {
		case <-held:
			t.Fatal("an earlier release ended a later hold")
		default:
		}
		second()
		synctest.Wait()
		<-held
	})
}
