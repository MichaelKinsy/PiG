package agent

import (
	"reflect"
	"testing"
)

// Pi's Agent.clearSteeringQueue, clearFollowUpQueue and clearAllQueues return nothing and empty only the queues they name (agent.ts:308-322).
func TestClearQueuesReturnNothingAndEmptyOnlyTheirQueues(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		method                 string
		wantSteering, wantFlow int
	}{
		{"clearSteeringQueue", "ClearSteeringQueue", 0, 2},
		{"clearFollowUpQueue", "ClearFollowUpQueue", 2, 0},
		{"clearAllQueues", "ClearAllQueues", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAgentForNextTurn(t)
			a.Steer(userMsg("s1"))
			a.Steer(userMsg("s2"))
			a.FollowUp(userMsg("f1"))
			a.FollowUp(userMsg("f2"))
			method := reflect.ValueOf(a).MethodByName(tc.method)
			if method.Type().NumOut() != 0 {
				t.Fatalf("%s returns %d values, want none", tc.method, method.Type().NumOut())
			}
			method.Call(nil)
			steering, followUp := a.PendingMessages()
			if len(steering) != tc.wantSteering || len(followUp) != tc.wantFlow {
				t.Fatalf("after %s: steering=%d followUp=%d, want %d and %d", tc.method, len(steering), len(followUp), tc.wantSteering, tc.wantFlow)
			}
		})
	}
}
