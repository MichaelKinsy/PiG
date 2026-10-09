package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// compaction.ts:331 trims the summary with String.prototype.trim: U+FEFF and Unicode spaces go, U+0085 stays, and a summary left empty is no summary.
func TestSummaryTextTrimsLikeJavaScript(t *testing.T) {
	summary := func(text string) ai.AssistantMessage {
		return ai.AssistantMessage{StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}}
	}
	if text, ok := summaryText(summary("\ufeff\u00a0")); ok {
		t.Errorf("a summary of only JavaScript whitespace is accepted: %q", text)
	}
	if text, ok := summaryText(summary("\ufeff done \u3000")); !ok || text != "done" {
		t.Errorf("summaryText = %q, %v, want \"done\"", text, ok)
	}
	if text, ok := summaryText(summary("\u0085")); !ok || text != "\u0085" {
		t.Errorf("a U+0085-only summary = %q, %v, want it kept", text, ok)
	}
}
