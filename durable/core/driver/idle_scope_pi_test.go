package driver

// pi: packages/durable/src/harness/scheduler.ts:129 (Scope), :319-320 waitForIdle, :330-332 (ordinary traversal does not cross a background task)

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

type graphTask = struct {
	ConversationID int64   `json:"conversationId"`
	Owner          *int64  `json:"owner"`
	Background     bool    `json:"background"`
	Conversations  []int64 `json:"conversations"`
}

func graphOf(tasks map[string]graphTask) taskGraphState {
	var g taskGraphState
	g.Tasks = tasks
	return g
}

// An idle wait covers the ordinary ownership scope of its start: the start conversation, the conversations owned by its non-background tasks, and
// so on down. A live task in a conversation outside that scope, and a live task below a background task, do not keep the wait pending
// (scheduler.ts waitForIdle: "reaches no live non-background task").
// mutation-checked: session_api.go liveIn visit (`t.ConversationID == c`) flips to also walk tasks of other conversations.
func TestTaskGraphLiveInFollowsOrdinaryOwnershipOnly(t *testing.T) {
	one := int64(1)
	for _, tc := range []struct {
		name  string
		root  *int64
		tasks map[string]graphTask
		want  bool
	}{
		{"a live task of the start conversation", &one, map[string]graphTask{"1": {ConversationID: 1}}, true},
		{"nothing live", &one, map[string]graphTask{}, false},
		{"a task of another conversation owns a conversation, the start owns none", &one, map[string]graphTask{
			"1": {ConversationID: 2, Conversations: []int64{4}},
			"2": {ConversationID: 4},
		}, false},
		{"below a background task is out of scope", &one, map[string]graphTask{
			"1": {ConversationID: 1, Background: true, Conversations: []int64{2}},
			"2": {ConversationID: 2},
		}, false},
		{"ownerless roots stop at a background task", nil, map[string]graphTask{
			"1": {ConversationID: 1, Background: true, Conversations: []int64{2}},
			"2": {ConversationID: 2},
		}, false},
		{"ownerless roots reach a live foreground task", nil, map[string]graphTask{
			"1": {ConversationID: 1, Conversations: []int64{2}},
			"2": {ConversationID: 2},
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := graphOf(tc.tasks)
			var got bool
			if tc.root == nil {
				got = g.liveIn(nil)
			} else {
				c := durable.ConversationId(*tc.root)
				got = g.liveIn(&c)
			}
			if got != tc.want {
				t.Errorf("liveIn = %v, want %v", got, tc.want)
			}
		})
	}
}
