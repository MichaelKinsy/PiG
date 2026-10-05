package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// TestCollectQueuedTexts_IncludesCompactionQueue is the regression guard for the
// Alt+Up dequeue bug: messages typed during an in-flight compaction live in
// m.compactionQueue, not the agent's steering/follow-up queues. Both the pending
// display and the dequeue handler must surface them, or the "Alt+Up to edit all
// queued messages" hint restores nothing.
func TestCollectQueuedTexts_IncludesCompactionQueue(t *testing.T) {
	tests := []struct {
		name          string
		steering      []string
		followUps     []string
		compaction    []compactionQueuedMessage
		wantSteering  []string
		wantFollowUps []string
	}{
		{
			name:          "empty",
			wantSteering:  []string{},
			wantFollowUps: []string{},
		},
		{
			name:          "agent queues only",
			steering:      []string{"s1"},
			followUps:     []string{"f1"},
			wantSteering:  []string{"s1"},
			wantFollowUps: []string{"f1"},
		},
		{
			name:          "compaction-only steering is surfaced",
			compaction:    []compactionQueuedMessage{{text: "c-steer", mode: compactionQueueSteer}},
			wantSteering:  []string{"c-steer"},
			wantFollowUps: []string{},
		},
		{
			name:          "compaction-only follow-up is surfaced",
			compaction:    []compactionQueuedMessage{{text: "c-follow", mode: compactionQueueFollowUp}},
			wantSteering:  []string{},
			wantFollowUps: []string{"c-follow"},
		},
		{
			name:      "mixed keeps session-then-compaction order per bucket",
			steering:  []string{"s1"},
			followUps: []string{"f1"},
			compaction: []compactionQueuedMessage{
				{text: "c-steer", mode: compactionQueueSteer},
				{text: "c-follow", mode: compactionQueueFollowUp},
			},
			wantSteering:  []string{"s1", "c-steer"},
			wantFollowUps: []string{"f1", "c-follow"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var steering, followUps []agent.AgentMessage
			for _, s := range tc.steering {
				steering = append(steering, mkUserMsg(s))
			}
			for _, f := range tc.followUps {
				followUps = append(followUps, mkUserMsg(f))
			}
			gotSteering, gotFollowUps := collectQueuedTexts(steering, followUps, tc.compaction)
			if !reflect.DeepEqual(gotSteering, tc.wantSteering) {
				t.Errorf("steering = %q, want %q", gotSteering, tc.wantSteering)
			}
			if !reflect.DeepEqual(gotFollowUps, tc.wantFollowUps) {
				t.Errorf("followUps = %q, want %q", gotFollowUps, tc.wantFollowUps)
			}
		})
	}
}

// An extension's custom message queued during a run has no "Steering:" or "Follow-up:" row, as in Pi: Pi's session
// lists only the user input it queued (agent-session.ts:2191-2210), and sendCustomMessage queues on the agent alone
// (:2246-2266). Before this a custom message rendered as an empty row (#141).
func TestCollectQueuedTexts_SkipsCustomMessages(t *testing.T) {
	custom := agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "notice", "content": "background done"}}
	steering, followUps := collectQueuedTexts(
		[]agent.AgentMessage{custom, mkUserMsg("s1")},
		[]agent.AgentMessage{mkUserMsg("f1"), custom},
		nil,
	)
	if !reflect.DeepEqual(steering, []string{"s1"}) || !reflect.DeepEqual(followUps, []string{"f1"}) {
		t.Fatalf("steering = %q, followUps = %q", steering, followUps)
	}
}
