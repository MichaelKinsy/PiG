package subprocess

import "testing"

// The host sends the caller its state before the result of a Session write: Pi's setModel, setSessionName, setLabel and sendMessage change the Session
// synchronously, so the SDK's replicated read right after the call returns the written value (agent-session.ts setModel, setSessionName,
// sessionManager.appendLabelChange, sendCustomMessage). A call that writes no replicated state sends none.
func TestReplyToCallPublishesStateBeforeASessionWriteResult(t *testing.T) {
	for _, tc := range []struct {
		method    string
		publishes bool
	}{
		{"setModel", true},
		{"appendEntry", true},
		{"setLabel", true},
		{"setSessionName", true},
		{"sendMessage", true},
		{"ui.select", true},
		{"getFlag", false},
		{"setActiveTools", false},
		{"exec", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rig := newPushRig(t)
			rig.h.replyToCall(rig.me, rig.conn, "call-1", &CallPayload{Method: tc.method}, t.Context(), &CallResultPayload{}, nil)
			if tc.publishes {
				rig.nextState(t)
				return
			}
			rig.expectNoFrame(t, tc.method+" writes no replicated state")
		})
	}
}
