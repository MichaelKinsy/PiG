package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/usagetotals"
)

// Pi's combineUsage builds the summed usage as one literal with cacheWrite1h and reasoning before totalTokens (usage-totals.ts:31-51), and
// agent-session.ts:1111 stores it on a tool result message. The Anthropic usages it sums hold the two splits after cost
// (anthropic-messages.ts:684, :851). The RPC message writes the combined usage as the session line does. Node, for two such usages:
// combineUsage(a, a) stringifies as {"input":2,"output":4,"cacheRead":0,"cacheWrite":6,"cacheWrite1h":6,"reasoning":2,"totalTokens":12,"cost":{...}}.
func TestRPCCombinedUsageKeepsTheLiteralOrder(t *testing.T) {
	var anthropic ai.Usage
	if err := json.Unmarshal([]byte(`{"input":1,"output":2,"cacheRead":0,"cacheWrite":3,"totalTokens":6,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0},"cacheWrite1h":3,"reasoning":1}`), &anthropic); err != nil {
		t.Fatal(err)
	}
	combined := usagetotals.CombineUsage(anthropic, anthropic)
	message := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "subagent", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}},
		Usage: &combined, Timestamp: 1,
	}}
	session, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	const want = `"usage":{"input":2,"output":4,"cacheRead":0,"cacheWrite":6,"cacheWrite1h":6,"reasoning":2,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	if !strings.Contains(string(session), want) {
		t.Fatalf("session line usage\n got: %s\nwant: %s", session, want)
	}
	wire, err := rpcAgentMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rpc), want) {
		t.Fatalf("RPC tool result usage\n got: %s\nwant: %s", rpc, want)
	}
	single, err := json.Marshal(rpcUsage(&anthropic))
	if err != nil {
		t.Fatal(err)
	}
	if got, wantSingle := string(single), `{"input":1,"output":2,"cacheRead":0,"cacheWrite":3,"totalTokens":6,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0},"cacheWrite1h":3,"reasoning":1}`; got != wantSingle {
		t.Fatalf("RPC provider usage\n got: %s\nwant: %s", got, wantSingle)
	}
}
