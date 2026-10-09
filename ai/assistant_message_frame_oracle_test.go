package ai

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Oracle for packages/ai/src/utils/assistant-message-frame.ts AssistantMessageFrameEncoder: Pi's own encoder and the Go encoder
// encode one stream whose live `partial` is a shared accumulator that runs ahead of the queued events (the encoder's per-block
// offsets trim the part of a delta the partial already shows), with an interleaved tool call and a terminal done event. The
// frames each produces must be identical JSON, and the terminal event produces no frame.
func TestAssistantMessageFrameEncoderMatchesPiOnAStreamTheLivePartialRunsAheadOf(t *testing.T) {
	const script = `(async () => {
  const [root] = process.argv.slice(1);
  const mod = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist/utils/assistant-message-frame.js')).href);
  const partial = { role: 'assistant', content: [], api: 'test-api', provider: 'test-provider', model: 'test-model', stopReason: 'pending', timestamp: 1, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
  const enc = new mod.AssistantMessageFrameEncoder();
  const out = [];
  const push = (e) => { const f = enc.encode(e); out.push(f === undefined ? null : f); };
  push({ type: 'start', partial });
  partial.content.push({ type: 'text', text: 'Hel' });
  push({ type: 'text_start', contentIndex: 0, partial });
  partial.content.push({ type: 'toolCall', id: 'call', name: 'bash', arguments: {} });
  push({ type: 'toolcall_start', contentIndex: 1, partial });
  partial.content[0] = { type: 'text', text: 'Hello world' };
  partial.content[1] = { type: 'toolCall', id: 'call', name: 'bash', arguments: { command: 'ls' } };
  push({ type: 'text_delta', contentIndex: 0, delta: 'lo ', partial });
  push({ type: 'text_delta', contentIndex: 0, delta: 'world', partial });
  push({ type: 'toolcall_delta', contentIndex: 1, delta: '{"command":"ls"}', partial });
  push({ type: 'text_end', contentIndex: 0, content: 'Hello world', partial });
  push({ type: 'toolcall_end', contentIndex: 1, toolCall: { type: 'toolCall', id: 'call', name: 'bash', arguments: { command: 'ls' } }, partial });
  push({ type: 'done', reason: 'toolUse', message: partial });
  console.log(JSON.stringify(out));
})()`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var piFrames []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &piFrames); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	partial := frameSeed()
	encoder := NewAssistantMessageFrameEncoder()
	var goFrames []any
	add := func(event AssistantMessageEvent) {
		frame, err := encoder.Encode(event)
		if err != nil {
			t.Fatalf("Encode(%s): %v", event.EventType(), err)
		}
		if frame == nil {
			goFrames = append(goFrames, nil)
			return
		}
		goFrames = append(goFrames, frame)
	}
	add(StartEvent{Partial: partial})
	partial.Content = append(partial.Content, TextContent{Text: "Hel"})
	add(TextStartEvent{ContentIndex: 0, Partial: partial})
	partial.Content = append(partial.Content, ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{}})
	add(ToolCallStartEvent{ContentIndex: 1, Partial: partial})
	partial.Content[0] = TextContent{Text: "Hello world"}
	partial.Content[1] = ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{"command": "ls"}}
	add(TextDeltaEvent{ContentIndex: 0, Delta: "lo ", Partial: partial})
	add(TextDeltaEvent{ContentIndex: 0, Delta: "world", Partial: partial})
	add(ToolCallDeltaEvent{ContentIndex: 1, Delta: `{"command":"ls"}`, Partial: partial})
	add(TextEndEvent{ContentIndex: 0, Content: "Hello world", Partial: partial})
	add(ToolCallEndEvent{ContentIndex: 1, ToolCall: ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{"command": "ls"}}, Partial: partial})
	add(DoneEvent{Reason: StopReasonToolUse, Message: partial})

	if len(goFrames) != len(piFrames) {
		t.Fatalf("Go produced %d results, Pi %d", len(goFrames), len(piFrames))
	}
	for i := range goFrames {
		got, err := json.Marshal(goFrames[i])
		if err != nil {
			t.Fatal(err)
		}
		var g, p any
		if err := json.Unmarshal(got, &g); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(piFrames[i], &p); err != nil {
			t.Fatal(err)
		}
		gc, _ := json.Marshal(g)
		pc, _ := json.Marshal(p)
		if string(gc) != string(pc) {
			t.Errorf("frame %d:\n Go %s\n Pi %s", i, gc, pc)
		}
	}
}
