package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// tree-selector.ts constructor takes SessionTreeNode[] and its currentLeafId: flattenTree reads the tool-call arguments for tool-result rows from the tree
// (tree-selector.ts:255-264, 802) and the filter keeps a tool-call-only assistant row visible when it is the current leaf (tree-selector.ts:289).
func TestNewTreeSelectorComponentReadsToolCallsAndLeafFromTheTree(t *testing.T) {
	sess := NewSession("sess-tree-ctor", "")
	if _, err := sess.AppendMessage(mkUserMsg("read AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	toolOnlyID, err := sess.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:      "assistant",
		Content:   []ai.AssistantContentBlock{ai.ToolCall{ID: "tu-1", Name: "read", Arguments: ai.JsonObject{"path": "AGENTS.md"}}},
		Timestamp: time.Now().UnixMilli(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "tu-1", ToolName: "read",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file body"}},
	}}); err != nil {
		t.Fatal(err)
	}
	finalID, err := sess.AppendMessage(mkAssistantMsg("found it"))
	if err != nil {
		t.Fatal(err)
	}
	render := func(leaf string) string {
		tree := sess.treeRoot().Children
		selector := NewTreeSelectorComponent(tree, &leaf, 40, nil, nil, nil, nil, "default")
		return stripANSI(strings.Join(selector.Render(100), "\n"))
	}

	atFinal := render(finalID)
	if !strings.Contains(atFinal, "[read: AGENTS.md") {
		t.Fatalf("tool-result row lacks the tool call's arguments read from the tree:\n%s", atFinal)
	}
	if strings.Contains(atFinal, "(no content)") {
		t.Fatalf("the tool-call-only assistant row is not the leaf, so it stays hidden:\n%s", atFinal)
	}
	atToolOnly := render(toolOnlyID)
	if !strings.Contains(atToolOnly, "(no content)") {
		t.Fatalf("the tool-call-only assistant row is the current leaf, so it is shown:\n%s", atToolOnly)
	}
}
