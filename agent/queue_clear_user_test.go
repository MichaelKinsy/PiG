package agent

import "testing"

func TestClearUserMessagesKeepsCustomMessagesInOrder(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	custom := func(id string) AgentMessage { return AgentMessage{Custom: map[string]any{"customType": id}} }
	q.Enqueue(custom("a"))
	q.Enqueue(AgentMessage{User: &UserMessage{Role: RoleUser}})
	q.Enqueue(custom("b"))
	if cleared := q.ClearUserMessages(); len(cleared) != 1 || cleared[0].User == nil {
		t.Fatalf("cleared = %+v", cleared)
	}
	kept := q.Messages()
	if len(kept) != 2 || kept[0].Custom["customType"] != "a" || kept[1].Custom["customType"] != "b" {
		t.Fatalf("kept = %+v", kept)
	}
}
