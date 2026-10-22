package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream sendCustomMessage (agent-session.ts:2323-2359) branches in a fixed order:
//
//	deliverAs "nextTurn"                          → held for the next run
//	else if streaming and triggerTurn !== false   → followUp when deliverAs is "followUp", otherwise steer
//	else if triggerTurn                           → start a run from this message
//	else if streaming                             → held until the turn's tool results are complete
//	else                                          → persist and render, start nothing
//
// triggerTurn is tri-state: an unset value queues a streaming message like true, and only an explicit false defers it.
func TestRouteCustomMessageMatchesUpstreamBranchOrder(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name        string
		hasAgent    bool
		streaming   bool
		triggerTurn *bool
		deliverAs   extension.DeliverAs
		want        customMessageRoute
	}{
		{name: "idle with triggerTurn starts a turn", hasAgent: true, triggerTurn: &yes, deliverAs: "followUp", want: customMessageStartTurn},
		{name: "idle with triggerTurn and no deliverAs starts a turn", hasAgent: true, triggerTurn: &yes, want: customMessageStartTurn},
		{name: "streaming with triggerTurn and followUp follows up", hasAgent: true, streaming: true, triggerTurn: &yes, deliverAs: "followUp", want: customMessageFollowUp},
		{name: "streaming with triggerTurn and no deliverAs steers", hasAgent: true, streaming: true, triggerTurn: &yes, want: customMessageSteer},
		{name: "streaming with steer steers", hasAgent: true, streaming: true, deliverAs: "steer", want: customMessageSteer},
		{name: "streaming with no options steers", hasAgent: true, streaming: true, want: customMessageSteer},
		{name: "streaming with followUp and unset triggerTurn follows up", hasAgent: true, streaming: true, deliverAs: "followUp", want: customMessageFollowUp},
		{name: "streaming with an unknown deliverAs steers", hasAgent: true, streaming: true, deliverAs: "later", want: customMessageSteer},
		{name: "streaming with triggerTurn false defers", hasAgent: true, streaming: true, triggerTurn: &no, want: customMessageDefer},
		{name: "streaming with triggerTurn false and followUp defers", hasAgent: true, streaming: true, triggerTurn: &no, deliverAs: "followUp", want: customMessageDefer},
		{name: "nextTurn holds even while streaming", hasAgent: true, streaming: true, triggerTurn: &yes, deliverAs: "nextTurn", want: customMessageNextTurn},
		{name: "nextTurn holds when idle rather than starting a turn", hasAgent: true, triggerTurn: &yes, deliverAs: "nextTurn", want: customMessageNextTurn},
		{name: "idle without triggerTurn persists", hasAgent: true, want: customMessagePersist},
		{name: "idle with triggerTurn false persists", hasAgent: true, triggerTurn: &no, want: customMessagePersist},
		{name: "idle with deliverAs but no triggerTurn persists", hasAgent: true, deliverAs: "followUp", want: customMessagePersist},
		{name: "no agent persists even with triggerTurn", triggerTurn: &yes, deliverAs: "followUp", want: customMessagePersist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := routeCustomMessage(tc.hasAgent, tc.streaming, tc.triggerTurn, tc.deliverAs)
			if got != tc.want {
				t.Errorf("routeCustomMessage(hasAgent=%t, streaming=%t, triggerTurn=%v, deliverAs=%q) = %v, want %v",
					tc.hasAgent, tc.streaming, tc.triggerTurn, tc.deliverAs, got, tc.want)
			}
		})
	}
}
