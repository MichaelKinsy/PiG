package agent

import (
	"reflect"
	"testing"
)

// Pi 0.87.1 packages/agent/src/agent-loop.ts:110,164-319 emits newMessages for this run, not the retained conversation.
func TestAgentEndContainsOnlyCurrentRunMessages(t *testing.T) {
	rec := newEventRecorder(nil)
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("reply")}), EventCh: rec.ch})
	mustSend(t, a, "first")
	mustSend(t, a, "second")
	var ended [][]AgentMessage
	for _, ev := range rec.stop() {
		if end, ok := ev.(AgentEndEvent); ok {
			ended = append(ended, end.Messages)
		}
	}
	if len(ended) != 2 {
		t.Fatalf("agent_end count=%d", len(ended))
	}
	for i, msgs := range ended {
		if !reflect.DeepEqual(roles(msgs), []string{"user", "assistant"}) {
			t.Fatalf("run %d messages=%v", i, roles(msgs))
		}
	}
	if ended[0][0].ContentBlocks()[0] == ended[1][0].ContentBlocks()[0] {
		t.Fatal("run message snapshots share earlier user prompt")
	}
}
