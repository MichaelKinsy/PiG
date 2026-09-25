package codingagent

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func prepareAssistantEventTest(t *testing.T, m *InteractiveMode) {
	t.Helper()
	m.statusLine = NewStatusLine(nil, "", nil)
	m.pendingArgs = make(map[int]*pendingToolArg)
	// Mirror the production owner-loop dispatcher; assertions also render on
	// this goroutine, never on the throttle timer's goroutine.
	renders := make(chan func(), 1)
	m.tuiInst.SetRenderDispatcher(func(render func()) { renders <- render })
	t.Cleanup(func() {
		m.tuiInst.CancelPendingRender()
		select {
		case render := <-renders:
			render()
		default:
		}
	})
}

func assistantLines(block *tui.AssistantMessageBlock) []string {
	lines := block.Render(80)
	for i := range lines {
		lines[i] = strings.TrimRight(stripANSITest(lines[i]), " ")
	}
	return lines
}

// Pi's message_start/update/end passes the authoritative message.content to
// updateContent, including separate same-type blocks and invisible boundaries.
func TestInteractiveMode_StreamingAssistantContentOrder(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprintf("hidden=%v", hidden), func(t *testing.T) {
			m := resumeThinkingMode(t, hidden, userMsg("question"), assistantMsg(""))
			prepareAssistantEventTest(t, m)
			msg := assistantMsg("", ai.TextContent{Text: "  FIRST  "})
			m.handleAgentEvent(agent.MessageStartEvent{Message: msg})
			if got := assistantLines(m.evCurrentBlock); !slices.Equal(got, []string{"", " FIRST"}) {
				t.Errorf("message_start: %q", got)
			}
			msg.Assistant.Content = []ai.AssistantContentBlock{
				ai.TextContent{Text: "  FIRST  "},
				ai.ThinkingContent{Thinking: "  ALPHA  "},
				ai.ThinkingContent{Thinking: " \n "},
				ai.ThinkingContent{Thinking: "BRAVO\n"},
				ai.TextContent{Text: " "},
				ai.ThinkingContent{Thinking: "CHARLIE"},
				ai.TextContent{Text: " LAST "},
				ai.TextContent{Text: " NEXT "},
			}
			m.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 7, Delta: " NEXT "}})
			want := []string{"", " FIRST", " ALPHA", "", " BRAVO", "", " CHARLIE", "", " LAST", " NEXT"}
			if hidden {
				want = []string{"", " FIRST", " Thinking...", "", " Thinking...", "", " LAST", " NEXT"}
			}
			if got := assistantLines(m.evCurrentBlock); !slices.Equal(got, want) {
				t.Errorf("message_update: %q, want %q", got, want)
			}
			// TextEnd/final replacements can differ from accumulated deltas.
			msg.Assistant.Content = []ai.AssistantContentBlock{ai.TextContent{Text: " corrected final "}}
			m.handleAgentEvent(agent.MessageEndEvent{Message: msg})
			if got := assistantLines(m.assistantBlocks[0]); !slices.Equal(got, []string{"", " corrected final"}) {
				t.Errorf("message_end: %q", got)
			}
			if m.lastAssistantText != "corrected final" {
				t.Errorf("copy text = %q", m.lastAssistantText)
			}
		})
	}
}

// AssistantMessageComponent displays terminal state even without visible
// content; tool calls suppress only abort/error, never length. Both initial
// resume and rebuild must preserve that state, not just text/thinking.
func TestInteractiveMode_AssistantTerminalStateLiveAndRedraw(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop ai.StopReason
		err  string
		want string
	}{
		{"length", ai.StopReasonLength, "", "Response was truncated before completion."},
		{"abort", ai.StopReasonAborted, "", "Operation aborted"},
		{"request-abort", ai.StopReasonAborted, "Request was aborted", "Operation aborted"},
		{"error", ai.StopReasonError, "provider failure", "Error: provider failure"},
		{"unknown-error", ai.StopReasonError, "", "Error: Unknown error"},
		{"multiline-error", ai.StopReasonError, "first\n  second", "Error: first\n  second"},
	} {
		for _, tool := range []bool{false, true} {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/tool=%v/partial=%v", tc.name, tool, partial), func(t *testing.T) {
					msg := assistantMsg("")
					msg.Assistant.StopReason, msg.Assistant.ErrorMessage = tc.stop, tc.err
					var want []string
					if partial {
						msg.Assistant.Content = append(msg.Assistant.Content, ai.TextContent{Text: " partial "})
						want = []string{"", " partial"}
					}
					if tool {
						msg.Assistant.Content = append(msg.Assistant.Content, ai.ToolCall{ID: "call", Name: "read", Arguments: map[string]any{"path": "example.txt"}})
					}
					if !tool || tc.stop == ai.StopReasonLength {
						want = append(want, "")
						for line := range strings.SplitSeq(tc.want, "\n") {
							want = append(want, " "+line)
						}
					}
					m := resumeThinkingMode(t, false, userMsg("question"), msg)
					prepareAssistantEventTest(t, m)
					m.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("")})
					m.handleAgentEvent(agent.MessageEndEvent{Message: msg})
					if got := assistantLines(m.assistantBlocks[0]); !slices.Equal(got, want) {
						t.Errorf("live: %q, want %q", got, want)
					}
					m.chatContainer.Clear()
					for _, render := range []func(){m.renderSessionEntries, m.rebuildChatFromSession} {
						render()
						if len(m.assistantBlocks) != 1 {
							t.Fatalf("redraw assistant blocks = %d, want one", len(m.assistantBlocks))
						}
						if got := assistantLines(m.assistantBlocks[0]); !slices.Equal(got, want) {
							t.Errorf("redraw: %q, want %q", got, want)
						}
						if tool && tc.stop != ai.StopReasonLength {
							comp := m.toolByID["call"]
							toolError := tc.err
							if tc.stop == ai.StopReasonAborted {
								toolError = "Operation aborted"
							} else if toolError == "" {
								toolError = "Error"
							}
							if comp == nil || comp.State != tui.ToolStateError || !strings.Contains(stripANSITest(strings.Join(comp.Render(80), "\n")), strings.Split(toolError, "\n")[0]) {
								t.Errorf("redrawn tool lacks error result: %s", renderedChat(m))
							}
						}
					}
				})
			}
		}
	}
}
