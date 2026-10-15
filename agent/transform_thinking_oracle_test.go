package agent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type replayThinkingCase struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	Redacted  bool   `json:"redacted"`
	Same      bool   `json:"same"`
}

type replayBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// transformMessages (transform-messages.ts) drops a thinking block whose text is empty after JavaScript trim() and turns any other foreign thinking into text. trim() strips U+FEFF and U+180E-free Unicode spaces but keeps U+0085, the reverse of Go's strings.TrimSpace.
// Pi source: packages/ai/src/api/transform-messages.ts:111
func TestNormalizeAssistantReplayThinkingTrimMatchesPi(t *testing.T) {
	texts := []string{"", " ", "x", " x ", "\n\t", "\ufeff", "\u0085", "\u00a0", "\u2028", "\u2029", "\u3000", "\u2003", "\u180e", "\u200b", "\v\f", "\ufeff\u0085", "\u0085x", "\ufeffx"}
	var cases []replayThinkingCase
	for _, text := range texts {
		for _, signature := range []string{"", "sig"} {
			for _, redacted := range []bool{false, true} {
				for _, same := range []bool{false, true} {
					cases = append(cases, replayThinkingCase{text, signature, redacted, same})
				}
			}
		}
	}
	var want [][]replayBlock
	pioracle.Run(t, `
const { transformMessages } = await load("pi-ai/api/transform-messages.js");
const model = { id: "m", provider: "p", api: "openai-completions", input: ["text"] };
emit(input.map(({ thinking, signature, redacted, same }) => {
  const block = { type: "thinking", thinking };
  if (signature) block.thinkingSignature = signature;
  if (redacted) block.redacted = true;
  const assistant = { role: "assistant", content: [block], api: same ? "openai-completions" : "anthropic-messages", provider: same ? "p" : "q", model: same ? "m" : "n", usage: {}, stopReason: "stop", timestamp: 1 };
  const out = transformMessages([{ role: "user", content: "hi", timestamp: 0 }, assistant], model);
  return out.filter((m) => m.role === "assistant").flatMap((m) => m.content.map((b) => ({ type: b.type, text: b.type === "text" ? b.text : b.thinking ?? "" })));
}));`, cases, &want)
	if len(want) != len(cases) {
		t.Fatalf("Pi returned %d results for %d cases", len(want), len(cases))
	}
	bad := 0
	for i, c := range cases {
		block := ai.ThinkingContent{Thinking: c.Thinking, ThinkingSignature: c.Signature, Redacted: c.Redacted}
		blocks, _ := normalizeAssistantReplayBlocks([]ai.AssistantContentBlock{block}, c.Same)
		var got []replayBlock
		for _, b := range blocks {
			switch b := b.(type) {
			case ai.TextContent:
				got = append(got, replayBlock{"text", b.Text})
			case ai.ThinkingContent:
				got = append(got, replayBlock{"thinking", b.Thinking})
			}
		}
		if !slices.Equal(got, want[i]) {
			if bad++; bad <= 8 {
				t.Errorf("%+v: got %+q, Pi %+q", c, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d cases differ from Pi", bad, len(cases))
	}
}
