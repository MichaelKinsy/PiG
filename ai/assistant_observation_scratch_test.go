package ai

import "testing"

// upstream: packages/ai/src/api/openai-completions.ts:461-463 and openai-responses-shared.ts:709-726 delete parser scratch on the retained block.
func TestAssistantMessageRetainedScratchCleanup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		scratch toolCallScratch
		wire    string
	}{
		{toolCallScratch{hasPartialArgs: true, partialArgs: `{"path":"file"}`, hasStreamIndex: true, streamIndex: 0}, `,"partialArgs":"{\"path\":\"file\"}","streamIndex":0`},
		{toolCallScratch{hasPartialJson: true, partialJson: `{"path":"file"}`}, `,"partialJson":"{\"path\":\"file\"}"`},
		{toolCallScratch{customInput: true, property: "input", jsonBuffer: grammarToolInputJSONBuffer{Input: "file", Started: true}}, `,"customInput":{"property":"input","jsonBuffer":{"input":"file","started":true,"closed":false}}`},
	} {
		scratch := test.scratch
		message := &AssistantMessage{Content: []AssistantContentBlock{ToolCall{scratch: scratch, ID: "call", Name: "read", Arguments: JsonObject{"path": "file"}}}, StopReason: StopReasonPending}
		cell := newAssistantMessageCell(message)
		full := cell.view()
		shallow := full.ShallowCopy()
		before := full.Observe()
		beforeJSON := observationJSON(t, before)
		const blockJSON = `{"type":"toolCall","id":"call","name":"read","arguments":{"path":"file"}`
		if got, want := observationJSON(t, before.Content[0]), blockJSON+test.wire+"}"; got != want {
			t.Fatalf("scratch wire\ngot  %s\nwant %s", got, want)
		}
		block := message.Content[0].(ToolCall)
		block.scratch = toolCallScratch{}
		message.Content[0] = block
		message.StopReason = StopReasonToolUse
		cell.publish(message, assistantMessageReplacements{})
		for _, view := range []*AssistantMessage{full, shallow} {
			got := view.Observe().Content[0].(ToolCall)
			if got.scratch != (toolCallScratch{}) {
				t.Fatalf("retained view kept scratch: %#v", got.scratch)
			}
			if got := observationJSON(t, got); got != blockJSON+"}" {
				t.Fatalf("final wire kept scratch: %s", got)
			}
			if got.Arguments["path"] != "file" {
				t.Fatalf("scratch cleanup changed parsed arguments: %#v", got.Arguments)
			}
		}
		if got := before.Content[0].(ToolCall).scratch; got != scratch {
			t.Fatalf("cleanup mutated an earlier owned observation: %#v", got)
		}
		if got := observationJSON(t, before); got != beforeJSON {
			t.Fatalf("earlier scratch wire changed: %s", got)
		}
		if got := shallow.Observe().StopReason; got != StopReasonPending {
			t.Fatalf("cleanup changed copied stop reason: %s", got)
		}
	}
}
