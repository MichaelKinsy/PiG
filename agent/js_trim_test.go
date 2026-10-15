package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// String.prototype.trim (transform-messages.ts:111, proxy.ts:181) treats U+FEFF as whitespace and U+0085 as an ordinary character; strings.TrimSpace does the opposite.
func TestReplayAndProxyLinesTrimLikeJavaScript(t *testing.T) {
	for _, tc := range []struct {
		thinking string
		dropped  bool
	}{{"\ufeff", true}, {" \ufeff\t", true}, {"\u0085", false}, {"x", false}, {"", true}} {
		out, changed := normalizeAssistantReplayBlocks([]ai.AssistantContentBlock{ai.ThinkingContent{Thinking: tc.thinking}}, true)
		if (len(out) == 0) != tc.dropped || changed != tc.dropped {
			t.Errorf("thinking %q: %d blocks, changed %v; dropped should be %v", tc.thinking, len(out), changed, tc.dropped)
		}
	}
	stream := ai.NewAssistantMessageEventStream()
	if handled, err := processProxyLine("data: \ufeff", &proxyEventConverter{}, stream); handled || err != nil {
		t.Errorf("a data line holding only U+FEFF: handled %v, error %v; want it skipped like an empty one", handled, err)
	}
	if _, err := processProxyLine("data: \u0085", &proxyEventConverter{}, stream); err == nil {
		t.Error("a data line holding U+0085 is not empty to JavaScript and must reach the JSON parser")
	}
}
