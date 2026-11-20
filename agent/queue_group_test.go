package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQueueAdjacentGroups(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeOneAtATime)
	for _, group := range []string{"channel", "channel", "", "channel"} {
		q.Enqueue(AgentMessage{Custom: map[string]any{"role": "custom", "content": "event"}, QueueGroup: group})
	}
	if got := len(q.Peek()); got != 2 {
		t.Fatalf("peek=%d", got)
	}
	for _, want := range []int{2, 1, 1, 0} {
		if got := len(q.Drain()); got != want {
			t.Fatalf("drain=%d want=%d", got, want)
		}
	}
	raw, err := json.Marshal(AgentMessage{Custom: map[string]any{"role": "custom"}, QueueGroup: "private-group"})
	if err != nil || strings.Contains(string(raw), "private-group") {
		t.Fatalf("queue metadata serialized: %s %v", raw, err)
	}
}
