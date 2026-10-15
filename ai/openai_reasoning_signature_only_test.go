package ai

import "testing"

// pi-mono #9534: OpenRouter streams the signature of an Anthropic reasoning.text block in its own
// delta that has no `text` field. Pi's isOpenAIReasoningDetail (openai-completions.ts:144-148)
// requires typeof detail.text === "string", so the delta is dropped and the replayed
// reasoning_details lose the signature.
// pig divergence (D122): a reasoning.text delta without text but with a string signature is kept and merged.
func TestOpenAICompletionsKeepsSignatureOnlyReasoningText(t *testing.T) {
	const textChunk = `{"type":"reasoning.text","text":"I should call the read tool.","id":"reasoning-text-1","format":"anthropic-claude-v1","index":0}`
	const signatureChunk = `{"type":"reasoning.text","signature":"sha256:signed-text","id":"reasoning-text-1","format":"anthropic-claude-v1","index":0}`
	const merged = `[{"type":"reasoning.text","text":"I should call the read tool.","id":"reasoning-text-1","format":"anthropic-claude-v1","index":0,"signature":"sha256:signed-text"}]`

	m := rfRun(t, "openrouter", rfChunk(`{"reasoning_details":[`+textChunk+`]}`, ""), rfChunk(`{"reasoning_details":[`+signatureChunk+`]}`, ""), rfChunk(`{"content":"done"}`, "stop"))
	thinking := rfThinking(t, m)
	if thinking.ThinkingSignature != merged {
		t.Fatalf("thinkingSignature = %s, want %s", thinking.ThinkingSignature, merged)
	}
	rfJSONEqual(t, "replayed reasoning_details", rfReplay(t, "openrouter", *m).ReasoningDetails, merged)

	alone := rfRun(t, "openrouter", rfChunk(`{"reasoning_details":[`+signatureChunk+`]}`, ""), rfChunk(`{"content":"done"}`, "stop"))
	if got := rfThinking(t, alone).ThinkingSignature; got != "["+signatureChunk+"]" {
		t.Fatalf("signature-only block = %s", got)
	}

	// A reasoning.text delta with neither text nor a string signature stays invalid.
	for _, invalid := range []string{`{"type":"reasoning.text","index":0}`, `{"type":"reasoning.text","signature":null}`, `{"type":"reasoning.text","signature":5}`} {
		none := rfRun(t, "openrouter", rfChunk(`{"reasoning_details":[`+invalid+`]}`, ""), rfChunk(`{"content":"done"}`, "stop"))
		for _, block := range none.Content {
			if _, ok := block.(ThinkingContent); ok {
				t.Fatalf("%s opened a thinking block: %#v", invalid, none.Content)
			}
		}
	}
}
