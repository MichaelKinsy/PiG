package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
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
