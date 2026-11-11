package components

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

func assistantStep(streaming bool, content ...ai.AssistantContentBlock) *transcript.Transcript {
	message := &ai.AssistantMessage{Content: content, API: "x", Provider: "faux", Model: "faux-1", Timestamp: 1}
	if !streaming {
		message.StopReason = ai.StopReasonStop
	}
	return &transcript.Transcript{Items: []transcript.Item{transcript.AssistantItem{Key: "response-1", Message: message, Tools: []transcript.ToolView{}, Streaming: streaming}}}
}

// A frame that rendered an answer while it was still arriving (empty, then only thinking) must not keep that picture once the
// answer is complete: the TUI of a loaded machine, which draws between the model's steps, showed "Thinking..." forever.
func TestAnAnswerRenderedWhileStreamingShowsItsFinalTextAtTheNextRender(t *testing.T) {
	view := NewTranscriptView(newTestContext(t), ViewState{}, nil)
	steps := []struct {
		tr   *transcript.Transcript
		want string
	}{
		{assistantStep(true), ""},
		{assistantStep(true, ai.ThinkingContent{Thinking: "secret"}), "Thinking..."},
		{assistantStep(false, ai.ThinkingContent{Thinking: "secret"}, ai.TextContent{Text: "Echo: a"}), "Echo: a"},
	}
	for index, step := range steps {
		view.Sync(step.tr, nil)
		if out := strings.Join(view.Render(80), "\n"); !strings.Contains(out, step.want) {
			t.Fatalf("step %d: want %q in %q", index, step.want, out)
		}
	}
}
