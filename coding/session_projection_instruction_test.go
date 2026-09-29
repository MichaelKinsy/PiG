package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-session.ts _installAgentRequestProjection preserves the Session projection even when compaction places a summary before its retained instruction entry.
func TestInstructionBaselineDoesNotDuplicateProjectedSystem(t *testing.T) {
	system := &ai.SystemMessage{Content: ai.SystemText("instructions"), Timestamp: 10}
	current := []agent.AgentMessage{{System: system}, {User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "old"}}}}}
	projected := []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "summary"}}}}, {System: system}, {User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "next"}}}}}
	if got := withInstructionBaseline(current, projected); !reflect.DeepEqual(got, projected) {
		t.Fatalf("projection was prefixed with duplicate instruction state: %#v", got)
	}
}
