package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// discardSession is a frontend session that draws nothing, so a benchmark
// measures the surface renderer alone.
type discardSession struct{}

func (discardSession) InputReady()                {}
func (discardSession) Columns() (main, dock int)  { return 0, 0 }
func (discardSession) Apply(frontend.Frame) error { return nil }
func (discardSession) HandleInput(string) bool    { return false }
func (discardSession) Suspend()                   {}
func (discardSession) Resume()                    {}
func (discardSession) Close() error               { return nil }

// buildSessionChat builds a transcript of msgs entries as interactive mode
// mounts them: user messages, assistant messages with a thinking run, and
// finished tool cards, each followed by a spacer.
func buildSessionChat(msgs int) *Container {
	chat := NewContainer()
	for i := range msgs {
		switch i % 3 {
		case 0:
			chat.Add(NewUserMessageComponent(fmt.Sprintf("Please look at file %d and fix the **build**.", i), nil, 1, nil))
		case 1:
			block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
			block.SetContent([]AssistantSegment{
				{Thinking: true, Text: fmt.Sprintf("Reading file %d first.", i)},
				{Text: fmt.Sprintf("File %d has a `nil` check missing.\n\n- add it\n- rerun the tests", i)},
			})
			chat.Add(block)
		case 2:
			card := NewToolExecutionComponent("read", "", nil, ToolExecutionOptions{}, nil, nil, "")
			card.UpdateArgs(json.RawMessage(fmt.Sprintf(`{"path":"src/file%d.go"}`, i)))
			card.MarkExecutionStarted()
			card.SetResult(fmt.Sprintf("package main\n\nfunc f%d() {}\n", i), false, 20*time.Millisecond)
			chat.Add(card)
		}
		chat.Add(NewSpacer(1))
	}
	return chat
}

// streamingRenderer builds the renderer under test over document and dock
// and returns its render function.
func streamingRenderer(renderer string, document, dock Component, hooks SurfaceHooks) func() {
	switch renderer {
	case "main-screen":
		ui := NewWithOutput(io.Discard, 100, 40)
		ui.Add(document)
		if dock != nil {
			ui.Add(dock)
		}
		return ui.Render
	default:
		surface := NewTuiSurfaceWithSize(discardSession{}, 100, 40, nil)
		surface.SetHooks(hooks)
		surface.SetLayout(document, dock)
		surface.Start()
		return surface.Render
	}
}

var benchmarkRenderers = []string{"main-screen", "surface"}

// BenchmarkStreamingFrameLongTranscript measures one streaming delta at the
// end of a long transcript, the frame PiG renders for every token, in the
// stock main-screen renderer and in the frontend surface renderer. The
// markdown transcript streams into a Markdown component; the session
// transcript streams into the assistant message interactive mode streams
// into, after user messages, assistant messages and tool cards.
func BenchmarkStreamingFrameLongTranscript(b *testing.B) {
	for _, transcript := range []string{"markdown", "session"} {
		for _, msgs := range []int{100, 2000, 10000} {
			for _, renderer := range benchmarkRenderers {
				b.Run(fmt.Sprintf("%s/%s/msgs=%d", transcript, renderer, msgs), func(b *testing.B) {
					var chat *Container
					var hooks SurfaceHooks
					var stream func(text string)
					switch transcript {
					case "markdown":
						chat, _ = buildLargeChat(msgs, 4)
						tail := NewMarkdown("")
						chat.Add(tail)
						stream = tail.SetText
					case "session":
						chat = buildSessionChat(msgs)
						tail := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
						chat.Add(tail)
						hooks.Streaming = func() *AssistantMessageComponent { return tail }
						stream = func(text string) {
							tail.SetContent([]AssistantSegment{{Text: text}})
						}
					}
					render := streamingRenderer(renderer, chat, nil, hooks)
					render()
					var text strings.Builder
					b.ReportAllocs()
					for b.Loop() {
						if text.Len() > 4000 {
							text.Reset()
						}
						text.WriteString("token ")
						stream(text.String())
						render()
					}
				})
			}
		}
	}
}

// BenchmarkDockFrameLongTranscript measures the frames that change only the
// dock under a long transcript: a keystroke in the input editor, and a
// working indicator tick with a footer update, as a streaming turn sends.
func BenchmarkDockFrameLongTranscript(b *testing.B) {
	const msgs = 2000
	for _, change := range []string{"keystroke", "status"} {
		for _, renderer := range benchmarkRenderers {
			b.Run(fmt.Sprintf("%s/%s/msgs=%d", change, renderer, msgs), func(b *testing.B) {
				chat := buildSessionChat(msgs)
				editor := NewEditor()
				indicator := &StatusIndicator{Kind: "working", Loader: newPlainLoader("Working")}
				status := NewContainer(indicator)
				footerLines := NewText("~/pig (main)")
				footer := frontend.Footer{Cwd: "~/pig", Model: "faux-1", ThinkingLevel: "off", AutoCompact: true, ContextUsage: frontend.ContextUsage{ContextWindow: 128000}}
				hooks := SurfaceHooks{
					Editor:         func() *Editor { return editor },
					EditorSendable: func() bool { return true },
					Working: func() (frontend.Working, bool) {
						return frontend.Working{Kind: frontend.WorkingAgent, Message: "Working", Interval: 80 * time.Millisecond}, true
					},
					Footer: func(c Component) (frontend.Footer, bool) {
						if c != Component(footerLines) {
							return frontend.Footer{}, false
						}
						return footer, true
					},
				}
				dock := NewContainer(status, NewContainer(editor), footerLines)
				render := streamingRenderer(renderer, chat, dock, hooks)
				render()
				b.ReportAllocs()
				n := 0
				for b.Loop() {
					n++
					switch change {
					case "keystroke":
						if n%200 == 0 {
							editor.SetText("")
						}
						editor.HandleInput("x")
					case "status":
						indicator.Tick()
						footer.UsageTotals.Output = n
						footerLines.SetText(fmt.Sprintf("~/pig (main) ↑0 ↓%d", n))
					}
					render()
				}
			})
		}
	}
}

// BenchmarkMessageFrameLongTranscript measures the frame after a message is
// mounted at the end of a long transcript, or unmounted from it, the frame
// that changes the transcript's structure.
func BenchmarkMessageFrameLongTranscript(b *testing.B) {
	for _, msgs := range []int{2000, 10000} {
		for _, renderer := range benchmarkRenderers {
			b.Run(fmt.Sprintf("%s/msgs=%d", renderer, msgs), func(b *testing.B) {
				chat := buildSessionChat(msgs)
				render := streamingRenderer(renderer, chat, nil, SurfaceHooks{})
				render()
				message := NewUserMessageComponent("One more **thing**.", nil, 1, nil)
				b.ReportAllocs()
				mounted := false
				for b.Loop() {
					if mounted {
						chat.Remove(message)
					} else {
						chat.Add(message)
					}
					mounted = !mounted
					render()
				}
			})
		}
	}
}
