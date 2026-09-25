package codingagent

import "testing"

// Upstream sendCustomMessage (agent-session.ts:1453-1469) branches in a fixed
// order on whether a turn is in flight:
//
//	deliverAs "nextTurn" → defer
//	else if isStreaming  → followUp / steer on the running turn
//	else if triggerTurn  → start a turn from this message
//	else                 → persist and render, start nothing
//
// Pig used `agent != nil && (triggerTurn || deliverAs != "")` for this, which
// conflates "an agent exists" with "a turn is running". An extension that
// completed work while the agent sat idle and called
// sendMessage({triggerTurn:true, deliverAs:"followUp"}) had its message routed
// to a queue that only drains inside a running turn, while the persistence
// branch was skipped because it counted as queued. The message appeared in
// neither the UI nor the session JSONL, and a restart lost it.
func TestRouteCustomMessageMatchesUpstreamBranchOrder(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hasAgent    bool
		streaming   bool
		triggerTurn bool
		deliverAs   string
		want        customMessageRoute
	}{
		// The reported bug: idle agent, triggerTurn set. Must start a turn.
		{
			name:     "idle with triggerTurn starts a turn",
			hasAgent: true, triggerTurn: true, deliverAs: "followUp",
			want: customMessageStartTurn,
		},
		{
			name:     "idle with triggerTurn and no deliverAs starts a turn",
			hasAgent: true, triggerTurn: true,
			want: customMessageStartTurn,
		},
		// Streaming outranks triggerTurn: a running turn takes the message.
		{
			name:     "streaming with triggerTurn queues onto the running turn",
			hasAgent: true, streaming: true, triggerTurn: true, deliverAs: "followUp",
			want: customMessageQueue,
		},
		{
			name:     "streaming with steer queues",
			hasAgent: true, streaming: true, deliverAs: "steer",
			want: customMessageQueue,
		},
		// nextTurn outranks everything, including streaming.
		{
			name:     "nextTurn defers even while streaming",
			hasAgent: true, streaming: true, triggerTurn: true, deliverAs: "nextTurn",
			want: customMessageQueue,
		},
		{
			name:     "nextTurn defers when idle rather than starting a turn",
			hasAgent: true, triggerTurn: true, deliverAs: "nextTurn",
			want: customMessageQueue,
		},
		// No trigger and not streaming: persist and render, start nothing.
		{
			name:     "idle without triggerTurn persists",
			hasAgent: true,
			want:     customMessagePersist,
		},
		{
			name:     "idle with deliverAs but no triggerTurn persists",
			hasAgent: true, deliverAs: "followUp",
			want: customMessagePersist,
		},
		// No agent at all: nothing can run, so persist.
		{
			name:        "no agent persists even with triggerTurn",
			triggerTurn: true, deliverAs: "followUp",
			want: customMessagePersist,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := routeCustomMessage(tc.hasAgent, tc.streaming, tc.triggerTurn, tc.deliverAs)
			if got != tc.want {
				t.Errorf("routeCustomMessage(hasAgent=%t, streaming=%t, triggerTurn=%t, deliverAs=%q) = %v, want %v",
					tc.hasAgent, tc.streaming, tc.triggerTurn, tc.deliverAs, got, tc.want)
			}
		})
	}
}

// A message routed to a turn or a queue must never also be persisted here: the
// agent persists it on delivery, and doing both would duplicate the entry.
// Equally, a message that is neither queued nor turned must be persisted, or it
// is lost. This pins the mutual exclusion the caller relies on.
func TestRouteCustomMessagePersistExactlyWhenNotHandedToAgent(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, trigger := range []bool{false, true} {
			for _, deliverAs := range []string{"", "followUp", "steer", "nextTurn"} {
				route := routeCustomMessage(true, streaming, trigger, deliverAs)
				handedToAgent := route == customMessageQueue || route == customMessageStartTurn
				persists := route == customMessagePersist
				if handedToAgent == persists {
					t.Errorf("streaming=%t trigger=%t deliverAs=%q: route %v is both handed to the agent and persisted, or neither",
						streaming, trigger, deliverAs, route)
				}
			}
		}
	}
}
