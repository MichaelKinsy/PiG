package cli

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/usagetotals"
)

// Pi json-event.ts:21-37,48-60 retains insertion order when removing partial; agent-loop.ts:286 constructs type,message,toolResults in that order.
func TestRPCEventBytesMatchPi(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
 import {toJsonEvent} from './extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/json-event.js';
 const usage={input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}};
 const message={role:'assistant',content:[{type:'text',text:'ok'}],api:'openai-completions',provider:'custom',model:'custom-model',usage,stopReason:'stop',timestamp:1};
 const events=[{type:'message_start',message}, {type:'message_update',message,assistantMessageEvent:{type:'text_delta',contentIndex:0,delta:'ok',partial:message}}, {type:'turn_end',message,toolResults:[]},{type:'agent_end',messages:[message],willRetry:false}];
 for (const event of events) console.log(JSON.stringify(toJsonEvent(event)));
 `)
	cmd.Dir = root
	want, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pi events: %v: %s", err, want)
	}
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, API: ai.APIOpenAICompletions, Provider: "custom", ModelID: "custom-model", Usage: &ai.Usage{}, StopReason: ai.StopReasonStop, Timestamp: 1}}
	partial := &ai.AssistantMessage{Content: message.Assistant.Content}
	events := []agent.AgentEvent{agent.MessageStartEvent{Message: message}, agent.MessageUpdateEvent{Message: message, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "ok", Partial: partial}}, agent.TurnEndEvent{Message: message}, agent.AgentEndEvent{Messages: []agent.AgentMessage{message}}}
	var got bytes.Buffer
	for _, event := range events {
		records, err := rpcAgentEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			writeJSONLine(&got, record)
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("event bytes:\n got %s\nwant %s", &got, want)
	}
}

func TestRPCUsageSnapshotsOptionalCounters(t *testing.T) {
	usage := &ai.Usage{CacheWrite1h: new(7), Reasoning: new(11)}
	wire := rpcUsage(usage)
	var before, after bytes.Buffer
	writeJSONLine(&before, wire)
	*usage.CacheWrite1h = 99
	*usage.Reasoning = 101
	writeJSONLine(&after, wire)
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatalf("queued usage changed after conversion: before %s; after %s", &before, &after)
	}
}

// Pi's OpenAI-compatible providers build usage as input, output, cacheRead, cacheWrite, reasoning, totalTokens, cost (ai/src/api/openai-completions.ts:1549-1557), so message_end and message_update carry reasoning before totalTokens on the wire.
func TestRPCUsageWireOrderPutsReasoningBeforeTotalTokens(t *testing.T) {
	var got bytes.Buffer
	writeJSONLine(&got, rpcUsage(&ai.Usage{Input: 1, Output: 2, TotalTokens: 3, Reasoning: new(0)}))
	want := `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}` + "\n"
	if got.String() != want {
		t.Fatalf("usage wire order:\n got %swant %s", got.String(), want)
	}
}

// get_messages answers with the agent's messages, which Pi serializes with the members in the order the provider and the agent loop assigned them: usage, stopReason, timestamp, then responseId (rpc-mode.ts get_messages; openai-completions.ts output; agent-loop.ts:409).
func TestRPCGetMessagesKeepsPiMemberOrder(t *testing.T) {
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, API: ai.APIOpenAICompletions, Provider: "custom", ModelID: "custom-model", Usage: &ai.Usage{}, StopReason: ai.StopReasonStop, Timestamp: 1, ResponseID: "chatcmpl-1"}}
	data, err := rpcGetMessagesData([]agent.AgentMessage{message})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	writeJSONLine(&got, data)
	want := `{"messages":[{"role":"assistant","content":[{"type":"text","text":"ok"}],"api":"openai-completions","provider":"custom","model":"custom-model","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1,"responseId":"chatcmpl-1"}]}` + "\n"
	if got.String() != want {
		t.Fatalf("get_messages data:\n got %swant %s", got.String(), want)
	}
}

// One usage object goes to the session line and to RPC, so both write its members in one order. A provider's Anthropic usage gets
// cacheWrite1h and reasoning after cost (anthropic-messages.ts:684, :851), while combineUsage builds them before totalTokens (usage-totals.ts:31-50);
// the split-turn compaction result (compaction.ts:1033) and a tool result with nested calls (nested-tool-calls.ts:88) carry the combined one.
func TestRPCUsageWritesTheMemberOrderOfItsValue(t *testing.T) {
	const providerOrder = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0},"cacheWrite1h":5,"reasoning":4}`
	var provider ai.Usage
	if err := json.Unmarshal([]byte(providerOrder), &provider); err != nil {
		t.Fatal(err)
	}
	combined := usagetotals.CombineUsage(provider, provider)
	for _, tc := range []struct {
		name  string
		usage ai.Usage
		want  string
	}{
		{"provider", provider, providerOrder},
		{"combined", combined, `{"input":2,"output":4,"cacheRead":0,"cacheWrite":0,"cacheWrite1h":10,"reasoning":8,"totalTokens":6,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`},
	} {
		session, err := json.Marshal(tc.usage)
		if err != nil {
			t.Fatal(err)
		}
		var rpc, compaction bytes.Buffer
		writeJSONLine(&rpc, rpcUsage(&tc.usage))
		writeJSONLine(&compaction, rpcCompactionResult(&coding.CompactionResult{Usage: &tc.usage}).Usage)
		if string(session) != tc.want || rpc.String() != tc.want+"\n" || compaction.String() != tc.want+"\n" {
			t.Fatalf("%s usage:\nsession    %s\nrpc        %scompaction %swant       %s", tc.name, session, &rpc, &compaction, tc.want)
		}
	}
}
