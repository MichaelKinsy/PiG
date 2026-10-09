package cli

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
)

func TestRPCMessageUpdateStreamingVariantBytesMatchPi(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import {toJsonEvent} from './extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/json-event.js';
const usage={input:2,output:3,cacheRead:5,cacheWrite:7,totalTokens:17,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0},cacheWrite1h:11,reasoning:13};
const call={type:'toolCall',id:'call',name:'lookup',arguments:{n:1}};
const partial={role:'assistant',content:[call],api:'openai-completions',provider:'probe',model:'probe',usage,stopReason:'pending',timestamp:1};
const events=[{type:'text_start',contentIndex:0,partial},{type:'text_delta',contentIndex:0,delta:'<&>\n',partial},{type:'text_end',contentIndex:0,content:'done',partial},{type:'thinking_start',contentIndex:0,partial},{type:'thinking_delta',contentIndex:0,delta:'think',partial},{type:'thinking_end',contentIndex:0,content:'thought',partial},{type:'toolcall_start',contentIndex:0,partial},{type:'toolcall_delta',contentIndex:0,delta:'{"n":1}',partial},{type:'toolcall_end',contentIndex:0,toolCall:call,partial}];
for(const event of events) console.log(JSON.stringify(toJsonEvent({type:'message_update',message:partial,assistantMessageEvent:event})));
`)
	command.Dir = root
	want, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Pi variants: %v\n%s", err, want)
	}
	usage := &ai.Usage{Input: 2, Output: 3, CacheRead: 5, CacheWrite: 7, TotalTokens: 17, CacheWrite1h: new(11), Reasoning: new(13)}
	call := ai.ToolCall{ID: "call", Name: "lookup", Arguments: ai.JsonObject{"n": 1}}
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{call}, API: ai.APIOpenAICompletions, Provider: "probe", Model: "probe", Usage: *usage, StopReason: ai.StopReasonPending, Timestamp: 1}
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: partial.Content, Usage: usage}}
	events := []ai.AssistantMessageEvent{
		ai.TextStartEvent{Partial: partial}, ai.TextDeltaEvent{Delta: "<&>\n", Partial: partial}, ai.TextEndEvent{Content: "done", Partial: partial},
		ai.ThinkingStartEvent{Partial: partial}, ai.ThinkingDeltaEvent{Delta: "think", Partial: partial}, ai.ThinkingEndEvent{Content: "thought", Partial: partial},
		ai.ToolCallStartEvent{Partial: partial}, ai.ToolCallDeltaEvent{Delta: `{"n":1}`, Partial: partial}, ai.ToolCallEndEvent{ToolCall: call, Partial: partial},
	}
	var got bytes.Buffer
	for _, event := range events {
		wire, err := rpcMessageUpdate(agent.MessageUpdateEvent{Message: message, AssistantMessageEvent: event})
		if err != nil {
			t.Fatal(err)
		}
		line, err := rpcclient.SerializeJsonLine(wire)
		if err != nil {
			t.Fatal(err)
		}
		got.Write(line)
	}
	// Node's usage retains source insertion order; RPC's established usage projection does too.
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("wire variants differ:\nGo=%s\nPi=%s", got.Bytes(), want)
	}
}

func TestRPCMessageUpdateTerminalProjectionKeepsExistingBytes(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, Usage: ai.Usage{Reasoning: new(3)}}
	for _, event := range []ai.AssistantMessageEvent{ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}, ai.ErrorEvent{Reason: ai.StopReasonError, Error: message}} {
		before, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(rpcAssistantEventFields(event))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("terminal projection changed existing bytes: %s -> %s, %v", before, after, err)
		}
	}
}

func TestRPCMessageUpdateRetainedTerminalEncodingErrors(t *testing.T) {
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant}}
	for _, event := range []ai.AssistantMessageEvent{
		ai.ToolCallEndEvent{ToolCall: ai.ToolCall{Arguments: ai.JsonObject{"bad": make(chan int)}}},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{Arguments: ai.JsonObject{"bad": make(chan int)}}}}},
	} {
		if _, err := rpcMessageUpdate(agent.MessageUpdateEvent{Message: message, AssistantMessageEvent: event}); err == nil || !strings.Contains(err.Error(), "marshal message_update assistant event") {
			t.Fatalf("retained non-JSON data lost its encoding error: %v", err)
		}
	}
	var absent *ai.TextDeltaEvent
	if _, err := rpcMessageUpdate(agent.MessageUpdateEvent{Message: message, AssistantMessageEvent: absent}); err == nil {
		t.Fatal("typed nil event became a valid wire record")
	}
}
