package codingagent

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D19): an extension's markdown transform runs on a worker, where Pi's runs inline on its one event loop. The worker's completion may only ask for a paint. The assistant block was handed Invalidate, which rebuilds its content container from the block's segments, spacers and scratch child list, while the owner loop streamed the next delta into the same block: two goroutines in updateContent, and a nil child in the container ("Container.renderChildLocked({0x0, 0x0})"). Run under -race.
func TestAssistantBlockAsyncMarkdownCompletionLeavesTheBlockToTheOwnerLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &InteractiveMode{backgroundCtx: ctx, tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	m.newRunner = inproc.NewRunner([]extension.Extension{{MarkdownTransformer: func(text string, _ extension.MarkdownTransformContext) string {
		return "rewritten " + text
	}}}, "")
	block := m.newAssistantMessageBlock(messageMeta{})
	chat := tui.NewContainer()
	chat.Add(block)
	message := func(step int) tui.AssistantMessage {
		content := []tui.AssistantContentBlock{
			{Type: "thinking", Thinking: "pondering " + strconv.Itoa(step)},
			{Type: "text", Text: "answer " + strconv.Itoa(step)},
		}
		if step%3 == 0 {
			content = append(content, tui.AssistantContentBlock{Type: "toolCall"}, tui.AssistantContentBlock{Type: "thinking", Thinking: "again " + strconv.Itoa(step)})
		}
		return tui.AssistantMessage{Content: content}
	}
	// The test goroutine is the owner loop: it streams each delta into the block and draws it.
	for step := range 300 {
		block.UpdateContent(message(step), true)
		chat.Render(80)
	}
	block.UpdateContent(tui.AssistantMessage{Content: []tui.AssistantContentBlock{{Type: "text", Text: "final answer"}}}, false)
	chat.Render(80)
	m.backgroundTasks.Wait()
	// Taking the completion off the block must not lose the repaint: the last generation's result reaches the next frame.
	if got := strings.Join(chat.Render(80), "\n"); !strings.Contains(got, "rewritten final answer") {
		t.Fatalf("the completed rewrite was not painted: %q", got)
	}
}
