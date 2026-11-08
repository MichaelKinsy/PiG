package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

const benchMarkdown = "## Plan\n\nThe **fix** touches `render()` and the cache:\n\n- first item with a [link](https://example.com)\n- second item\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\nA closing paragraph that is long enough to wrap across several rows at narrow widths, with emoji 👍 and 日本語."

// benchView is a conversation of n user/assistant pairs.
func benchView(n int) harness.TranscriptView {
	var view harness.TranscriptView
	id := 0
	for i := range n {
		id++
		view.Items = append(view.Items, harness.TranscriptEntry{Entry: userEntry(id)})
		id++
		reply := ai.AssistantMessage{
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("Answer %d\n\n%s", i, benchMarkdown)}},
			API:     "openai-completions", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: int64(id),
		}
		view.Items = append(view.Items, harness.TranscriptEntry{Entry: durable.EntryRecord{
			Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.assistant", Model: []ai.Message{reply},
		}})
	}
	return view
}

func BenchmarkBuildTranscript(b *testing.B) {
	for _, n := range []int{10, 100} {
		view := benchView(n)
		b.Run(fmt.Sprintf("pairs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				transcript.BuildTranscript(view, nil, nil)
			}
		})
	}
}

func BenchmarkTranscriptViewRender(b *testing.B) {
	ctx := newTestContext(b)
	built := transcript.BuildTranscript(benchView(50), nil, nil)
	view := NewTranscriptView(ctx, ViewState{}, nil)
	view.Sync(&built, nil)
	b.ReportAllocs()
	for b.Loop() {
		view.Invalidate()
		view.Render(100)
	}
}

func BenchmarkTranscriptViewRenderCached(b *testing.B) {
	ctx := newTestContext(b)
	built := transcript.BuildTranscript(benchView(50), nil, nil)
	view := NewTranscriptView(ctx, ViewState{}, nil)
	view.Sync(&built, nil)
	view.Render(100)
	b.ReportAllocs()
	for b.Loop() {
		view.Render(100)
	}
}

func BenchmarkAssistantMessageRender(b *testing.B) {
	ctx := newTestContext(b)
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: benchMarkdown}}, StopReason: ai.StopReasonStop}
	c := NewAssistantMessageComponent(ctx, message, false, "")
	b.ReportAllocs()
	for b.Loop() {
		c.Invalidate()
		c.Render(100)
	}
}

func BenchmarkToolCardRender(b *testing.B) {
	ctx := newTestContext(b)
	out := strings.Repeat("output line with some content\n", 30)
	c := NewToolExecutionComponent(transcript.ToolView{ID: "c", Name: "mcp_thing", Status: "done", Output: out}, nil, ctx, true)
	b.ReportAllocs()
	for b.Loop() {
		c.Invalidate()
		c.Render(100)
	}
}

func BenchmarkBashExecutionRender(b *testing.B) {
	ctx := newTestContext(b)
	c := NewBashExecutionComponent("ls", ctx, BashExecutionOptions{})
	c.SetOutput(strings.Repeat("output line with some content\n", 100), false)
	zero := 0.0
	c.SetComplete(&zero, false, BashCompletion{})
	b.ReportAllocs()
	for b.Loop() {
		c.Invalidate()
		c.Render(100)
	}
}
