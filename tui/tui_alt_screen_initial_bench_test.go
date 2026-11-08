package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"
)

var benchWords = strings.Fields("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega the of and to in is that for it with as was on be at by this had not are but from or have")

func benchSentence(rng *rand.Rand, n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = benchWords[rng.Intn(len(benchWords))]
	}
	return strings.Join(words, " ")
}

// benchMarkdown is a Markdown answer of about chars characters made of headings, bullet lists with bold and inline code, code
// fences and paragraphs, the mix of a model's reply.
func benchMarkdown(rng *rand.Rand, chars int) string {
	var blocks []string
	size := 0
	for size < chars {
		var block string
		switch r := rng.Float64(); {
		case r < 0.15:
			block = "## " + strings.ToUpper(benchSentence(rng, 1)) + " " + benchSentence(rng, 3)
		case r < 0.3:
			items := make([]string, 4)
			for i := range items {
				items[i] = fmt.Sprintf("- **%s**: %s `%s()`", benchSentence(rng, 2), benchSentence(rng, 10), benchWords[rng.Intn(len(benchWords))])
			}
			block = strings.Join(items, "\n")
		case r < 0.4:
			lines := make([]string, 6)
			for i := range lines {
				lines[i] = fmt.Sprintf("const %s = %s(%d);", benchWords[rng.Intn(len(benchWords))], benchWords[rng.Intn(len(benchWords))], rng.Intn(100))
			}
			block = "```ts\n" + strings.Join(lines, "\n") + "\n```"
		default:
			block = benchSentence(rng, 60)
		}
		blocks = append(blocks, block)
		size += len(block)
	}
	return strings.Join(blocks, "\n\n")
}

// buildHarnessTranscript builds the transcript of a resumed session of entries entries the way interactive mode mounts it: per turn an
// assistant message (a thinking run and a Markdown answer) and the finished bash card of its tool call, after a user message every 20 turns.
func buildHarnessTranscript(entries int) *Container {
	rng := rand.New(rand.NewSource(1))
	chat := NewContainer()
	for turn := 0; len(chat.Children()) < entries*3/2; turn++ {
		if turn%20 == 0 {
			if !chat.IsEmpty() {
				chat.Add(NewSpacer(1))
			}
			chat.Add(NewUserMessageComponent("Next task: "+benchSentence(rng, 30), nil, 1, nil, "", ""))
		}
		chat.Add(NewSpacer(1))
		block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
		block.SetContent([]AssistantSegment{{Thinking: true, Text: benchSentence(rng, 436)}, {Text: benchMarkdown(rng, 2620)}})
		block.FinishStreaming()
		chat.Add(block)
		card := NewToolExecutionComponent("bash", fmt.Sprintf("call_h%d", turn), nil, ToolExecutionOptions{}, nil, nil, "")
		card.UpdateArgs(json.RawMessage(fmt.Sprintf(`{"command":"echo history %d"}`, turn)))
		card.MarkExecutionStarted()
		lines := make([]string, 15)
		for i := range lines {
			lines[i] = benchSentence(rng, 12)
		}
		card.SetResult(strings.Join(lines, "\n"), false, 20*time.Millisecond)
		chat.Add(card)
	}
	return chat
}

func newHarnessAltScreen(chat Component) (*TuiAltScreen, *ScrollView) {
	document := NewContainer(NewText("transcript header"), chat)
	transcript := NewScrollView(document, ScrollViewOptions{Follow: "end", Primary: true, Overscroll: "chain", Scrollbar: "auto"})
	root := NewVStack([]StackEntry{
		{Component: transcript, StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
		{Component: NewText("\x1b[2m> editor dock\x1b[0m"), StackEntryOptions: StackEntryOptions{Grow: new(0), Shrink: new(1), MinSize: new(1)}},
	}, StackOptions{})
	tui := newAltScreenForTest(nil, 200, 50, TuiAltScreenOptions{})
	tui.out = io.Discard
	tui.SetLayoutRoot(root)
	return tui, transcript
}

// BenchmarkAltScreenFirstFrameLongTranscript measures the first fullscreen frame over a resumed transcript of 1000 entries: every
// entry is rendered once, at the terminal width.
func BenchmarkAltScreenFirstFrameLongTranscript(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		chat := buildHarnessTranscript(1000)
		tui, transcript := newHarnessAltScreen(chat)
		b.StartTimer()
		tui.Start()
		tui.doRender()
		b.StopTimer()
		transcript.Dispose()
		b.StartTimer()
	}
}
