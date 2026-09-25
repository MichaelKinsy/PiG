package agentharness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// Review-2 P2: upstream steer/followUp/nextRun accept an AgentMessage plus
// optional images (lane.ts appends them to a user message and rejects them on
// other roles with InvalidMessage "images_with_non_user"). This is a
// declaration contract; no lane implementation exists in this package.
func TestAgentLaneQueueAcceptsMessageWithImages(t *testing.T) {
	type queueWithImages = func(AgentLane, harness.Context, agent.AgentMessage, []ai.ImageContent) (string, error)
	for name, method := range map[string]queueWithImages{
		"SteerWithImages":    AgentLane.SteerWithImages,
		"FollowUpWithImages": AgentLane.FollowUpWithImages,
		"NextRunWithImages":  AgentLane.NextRunWithImages,
	} {
		if method == nil {
			t.Fatalf("%s missing", name)
		}
	}
}
