package compaction

// Ports .upstream/v0.99.1/packages/coding-agent/test/compaction-nested-calls.test.ts (1 case).

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// upstream compaction-nested-calls.test.ts:5-26: files touched by nested calls recorded on tool results are included (utils.ts:31-35).
func TestCompactionFileOperationsIncludeFilesTouchedByNestedCallsRecordedOnToolResults(t *testing.T) {
	bytes := 40000
	result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "codemode-1", ToolName: "codemode", Content: []ai.ToolResultMessageContent{}, IsError: false, Timestamp: 0,
		NestedCalls: &ai.NestedToolCalls{
			Calls: []ai.NestedToolCallRecord{
				{ID: "codemode-1/1", Name: "read", Arguments: ai.JsonObject{"path": "a.ts"}, Status: ai.NestedToolCallOK},
				{ID: "codemode-1/2", Name: "edit", Arguments: ai.JsonObject{"path": "b.ts", "edits": []any{}}, Status: ai.NestedToolCallOK},
				{ID: "codemode-1/3", Name: "write", ArgumentsBytes: &bytes, Status: ai.NestedToolCallOK},
			},
			Complete: false,
		},
	}}
	ops := NewFileOps()
	ExtractFileOpsFromMessage(result, &ops)
	read, modified := ComputeFileLists(ops)
	if want := []string{"a.ts"}; !reflect.DeepEqual(read, want) {
		t.Fatalf("readFiles = %v, want %v", read, want)
	}
	if want := []string{"b.ts"}; !reflect.DeepEqual(modified, want) {
		t.Fatalf("modifiedFiles = %v, want %v", modified, want)
	}
}
