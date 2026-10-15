package pipelinediff

import "testing"

// Pi's transformMessages (packages/ai/src/api/transform-messages.ts) maps every assistant message's tool call ids through normalizeToolCallId in its first
// pass, including a message the second pass then drops (stopReason error or aborted), and rewrites the id of each tool result from that map. A tool result
// whose assistant message was dropped therefore reaches the OpenAI Responses converter with its normalized id. PiG's Responses converters run the same
// TransformMessages with the same normalizer (ai/openai_responses.go prepareResponsesTranscript), so the request is the same bytes: this pins finding 7a891011,
// which the pipeline's former responsesOrphanID filter had excused as a difference (the filter is removed; 400 generated cases per target and mode pass without it). The ids carry a '|' and characters outside the Responses id alphabet and come
// from a foreign provider, so normalization changes them.
func TestAIPipelineOrphanResultOfADroppedAssistantMatchesPi(t *testing.T) {
	h := newHarness(t)
	h.direct = true
	ts := int64(1_700_000_000_000)
	tick := func() int64 { ts++; return ts }
	for _, tgt := range targets {
		if tgt.Name != "openai-responses" && tgt.Name != "azure-responses" {
			continue
		}
		for _, stop := range []string{"error", "aborted"} {
			t.Run(tgt.Name+"/"+stop, func(t *testing.T) {
				assistant := message{
					"role": "assistant", "api": "anthropic-messages", "provider": "anthropic", "model": "claude-opus-4-5",
					"content": []any{message{"type": "toolCall", "id": "toolu_01/A+b|fc_9?x", "name": "read", "arguments": message{"path": "a"}}},
					"usage": message{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2,
						"cost": message{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
					"stopReason": stop, "errorMessage": "failed", "timestamp": tick(),
				}
				c := testCase{Target: tgt, Messages: []message{
					{"role": "user", "content": "go", "timestamp": tick()},
					assistant,
					{"role": "toolResult", "toolCallId": "toolu_01/A+b|fc_9?x", "toolName": "read", "content": []any{message{"type": "text", "text": "result"}}, "isError": false, "timestamp": tick()},
					{"role": "user", "content": "again", "timestamp": tick()},
				}}
				o := h.judge(c)
				if o.Kind == "pi-error" {
					t.Skipf("Pi rejects the history itself: %s", o.Detail)
				}
				if o.Kind != "equal" {
					t.Fatalf("%s: PiG's request differs from Pi's:\n%s", signature(o), truncate(o.Detail, 1500))
				}
			})
		}
	}
}
