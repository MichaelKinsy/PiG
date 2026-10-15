// SPDX-License-Identifier: MIT

//go:build !race

package codingagent

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// transcriptTurn is the source of one finished turn of a long session: the assistant's Markdown answer, its read call,
// and the file text the read returned.
type transcriptTurn struct {
	final  *agent.AssistantMessage
	callID string
	args   json.RawMessage
	result agent.AgentToolResult
}

var transcriptWords = strings.Fields("the renderer keeps every finished row of the transcript while the terminal already holds them in its scrollback " +
	"so a long session pays for each answer and each tool card again in memory although only the rows below the viewport top can still change")

// transcriptProse returns a Markdown answer of about 700 bytes, distinct for each turn: two paragraphs with inline
// code and emphasis around a three-item list, as a model writes between tool calls.
func transcriptProse(turn int) string {
	word := func(k int) string { return transcriptWords[(turn*7+k*3)%len(transcriptWords)] }
	var b strings.Builder
	fmt.Fprintf(&b, "Turn %d: I read `internal/pkg%d/file%d.txt` and **%s %s** is where the %s happens. ", turn, turn%17, turn, word(0), word(1), word(2))
	for k := range 14 {
		b.WriteString(word(k + 3))
		b.WriteByte(' ')
	}
	b.WriteString("before the next step.\n\n")
	for item := range 3 {
		fmt.Fprintf(&b, "- `%s%d()` %s", word(item), turn, word(item+20))
		for k := range 10 {
			b.WriteByte(' ')
			b.WriteString(word(item*10 + k + 30))
		}
		b.WriteString(".\n")
	}
	b.WriteString("\nNext I will check whether ")
	for k := range 22 {
		b.WriteString(word(k + 60))
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "and then read file %d.", turn+1)
	return b.String()
}

// transcriptFile returns the 40-line text a read of turn's file returns.
func transcriptFile(turn int) string {
	var b strings.Builder
	for line := range 40 {
		fmt.Fprintf(&b, "%4d  value_%d_%d = compute(%d, %q)\n", line+1, turn, line, line*turn, transcriptWords[(turn+line)%len(transcriptWords)])
	}
	return b.String()
}

func buildTranscriptTurns(n int) (turns []transcriptTurn, sourceBytes int) {
	for i := range n {
		prose, file := transcriptProse(i), transcriptFile(i)
		path := fmt.Sprintf("internal/pkg%d/file%d.txt", i%17, i)
		callID := fmt.Sprintf("call_%d", i)
		args, _ := json.Marshal(map[string]any{"path": path})
		final := &agent.AssistantMessage{
			Role: agent.RoleAssistant, API: "test-api", Provider: "test-provider", ModelID: "test-model", Usage: &ai.Usage{},
			StopReason: ai.StopReasonToolUse, Timestamp: int64(1_700_000_000_000 + i),
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: prose}, ai.ToolCall{ID: callID, Name: "read", Arguments: map[string]any{"path": path}}},
		}
		turns = append(turns, transcriptTurn{final: final, callID: callID, args: args,
			result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: file}}}})
		sourceBytes += len(prose) + len(file) + len(args)
	}
	return turns, sourceBytes
}

// playTranscriptTurn delivers one turn's events to the interactive mode and draws a frame after each of them, as the
// main loop does: the assistant answer streams in four deltas, then the read call runs and settles collapsed.
func playTranscriptTurn(m *InteractiveMode, turn transcriptTurn) {
	prose := turn.final.Content[0].(ai.TextContent).Text
	m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop}}})
	m.tuiInst.Render()
	for k := 1; k <= 4; k++ {
		text := prose[:len(prose)*k/4]
		partial := &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}}
		m.handleAgentEvent(agent.MessageUpdateEvent{Message: agent.AgentMessage{Assistant: partial}, AssistantMessageEvent: ai.TextDeltaEvent{Delta: text[len(prose)*(k-1)/4:]}})
		m.tuiInst.Render()
	}
	m.handleAgentEvent(agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: turn.final}})
	m.tuiInst.Render()
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: turn.callID, ToolName: "read", Args: turn.args})
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: turn.callID, ToolName: "read", Result: turn.result})
}

// transcriptRenderHeap is the heap in use after two collections.
func transcriptRenderHeap() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// fullRedrawRows returns the rows a destructive full repaint of the current transcript writes, the frame a resize or a
// forced redraw writes, joined by "\r\n".
func fullRedrawRows(t *testing.T, m *InteractiveMode, term *terminalRecorder) string {
	t.Helper()
	term.take()
	m.tuiInst.RenderNow(true)
	frame := term.take()
	const clearScreen, endSync = "\x1b[2J\x1b[H\x1b[3J", "\x1b[?2026l"
	start := strings.Index(frame, clearScreen)
	end := strings.LastIndex(frame, endSync)
	if start < 0 || end < start {
		t.Fatalf("forced render wrote no full redraw: %q", frame)
	}
	return frame[start+len(clearScreen) : end]
}

// replayTranscript returns a session of the given height, in the working directory cwd, that was given the finished
// turns. Tool cards link each path against the working directory, so a session compared row for row with another must
// share its directory.
func replayTranscript(t *testing.T, turns []transcriptTurn, height int, cwd string) (*InteractiveMode, *terminalRecorder) {
	t.Helper()
	m, term := newTickRenderProbeIn(t, "regular", cwd)
	m.tuiInst.(*tui.TuiMainScreen).SetFixedSize(transcriptWidth, height)
	for _, turn := range turns {
		m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop}}})
		m.handleAgentEvent(agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: turn.final}})
		m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: turn.callID, ToolName: "read", Args: turn.args})
		m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: turn.callID, ToolName: "read", Result: turn.result})
	}
	m.tuiInst.Render()
	return m, term
}

const (
	transcriptTurns  = 200
	transcriptWidth  = 120
	transcriptHeight = 40
	// transcriptTallHeight is taller than the transcript, so no row of a session that tall leaves the screen.
	transcriptTallHeight = 4096
	// transcriptRowBound is the heap a finished session may keep per transcript row once its turns have played: the
	// components themselves (the answer text, the tool result, the card's renderers: about 420 B per row here) plus
	// their render state. Keeping every rendered row with the containers' spare capacity and Markdown's streaming caches,
	// as the renderer did, costs about 2.1 KB per row; dropping that spare capacity and those caches keeps 1367-1385 B.
	// Rows above the live region are in the terminal's scrollback, and a renderer that releases their rendered strings
	// keeps about 740 B per row, but the one measured (mem M4 step 2, 16692b03f3) cost 13% on the keystroke frame of a
	// 200-turn transcript for under 1% of a long session's resident memory, so it is not the bound.
	transcriptRowBound = 1450
)

// The main-screen renderer and the transcript's components keep every rendered row of a finished session: each
// Markdown block's lines and its wrap and inline caches, each container's concatenation and its spare array, each tool
// card's painted rows, and the renderer's previous frame with its line-reset input and output arrays. The heap a
// session keeps per row must stay within transcriptRowBound once the spare capacity and the streaming caches are gone,
// and every full redraw must write the rows of a session in which no row ever left the screen.
func TestFinishedTranscriptRowsRetainOnlyAFixedRecordEach(t *testing.T) {
	// The rows a session keeps depend on the terminal: one with OSC 8 links (kitty, Ghostty, iTerm2, WezTerm, ...)
	// links every read card's path as file://<cwd>/<path>, and 256-color escapes are shorter than truecolor ones, so the
	// heap per row would follow the host's terminal and the length of its temp directory. Measure one terminal: truecolor,
	// no links (Pi's PI_TRUE_COLOR and PI_HYPERLINKS overrides).
	t.Setenv("PI_TRUE_COLOR", "1")
	t.Setenv("PI_HYPERLINKS", "0")
	tui.ResetCapabilitiesCache()
	t.Cleanup(tui.ResetCapabilitiesCache)
	turns, sourceBytes := buildTranscriptTurns(transcriptTurns)
	m, term := newTickRenderProbe(t, "regular")
	ui := m.tuiInst.(*tui.TuiMainScreen)
	ui.SetFixedSize(transcriptWidth, transcriptHeight)
	m.tuiInst.Render()
	term.take()

	before := transcriptRenderHeap()
	for _, turn := range turns {
		playTranscriptTurn(m, turn)
		// The terminal keeps what a frame wrote, not the renderer: drop it so the recorder's buffer stays one turn long.
		term.take()
	}
	for range 3 {
		m.tuiInst.Render()
	}
	term.take()
	after := transcriptRenderHeap()
	runtime.KeepAlive(turns)
	runtime.KeepAlive(m)
	retained := int64(after) - int64(before)

	redraw := fullRedrawRows(t, m, term)
	rows := strings.Count(redraw, "\r\n") + 1
	perRow := float64(retained) / float64(rows)
	t.Logf("%d turns, %d rows at %dx%d: retained %d B (%.0f B/row, %.2fx the %d B source); bound %d B/row",
		transcriptTurns, rows, transcriptWidth, transcriptHeight, retained, perRow, float64(retained)/float64(sourceBytes), sourceBytes, transcriptRowBound)

	// Parity. The reference session is taller than the transcript, so none of its rows ever leaves the screen. The
	// session's full redraw, the one after a width round trip that renders every row again, and the one after expanding
	// every tool card must write the reference's rows; the frame that expands the cards, which changes rows far above
	// the viewport, must be the one a fresh session of the session's height writes.
	reference, referenceTerm := replayTranscript(t, turns, transcriptTallHeight, m.opts.CWD)
	want := fullRedrawRows(t, reference, referenceTerm)
	if redraw != want {
		t.Fatalf("full redraw of the session differs from the reference's:\n%s", firstRedrawDifference(redraw, want))
	}
	ui.SetFixedSize(transcriptWidth-20, transcriptHeight)
	m.tuiInst.Render()
	ui.SetFixedSize(transcriptWidth, transcriptHeight)
	m.tuiInst.Render()
	if resized := fullRedrawRows(t, m, term); resized != want {
		t.Fatalf("full redraw after a width round trip differs from the reference's:\n%s", firstRedrawDifference(resized, want))
	}
	// Invalidating a finished answer far above the viewport without changing it changes no row, so the frame that
	// follows must not clear the scrollback and repaint the transcript.
	m.assistantBlocks[0].Invalidate()
	term.take()
	m.tuiInst.Render()
	if frame := term.take(); strings.Contains(frame, "\x1b[3J") {
		t.Fatalf("frame after invalidating an unchanged answer above the viewport repainted the transcript (%d rows)", strings.Count(frame, "\r\n")+1)
	}

	fresh, freshTerm := replayTranscript(t, turns, transcriptHeight, m.opts.CWD)
	for _, session := range []*InteractiveMode{m, fresh, reference} {
		session.setAllToolsExpanded(true)
	}
	term.take()
	m.tuiInst.Render()
	freshTerm.take()
	fresh.tuiInst.Render()
	if got, want := term.take(), freshTerm.take(); got != want {
		t.Fatalf("frame after expanding every tool card differs from a fresh session's:\n%s", firstRedrawDifference(got, want))
	}
	reference.tuiInst.Render()
	if got, want := fullRedrawRows(t, m, term), fullRedrawRows(t, reference, referenceTerm); got != want {
		t.Fatalf("full redraw after expanding every tool card differs from the reference's:\n%s", firstRedrawDifference(got, want))
	}

	if perRow > transcriptRowBound {
		t.Fatalf("finished transcript keeps %.0f B per row (%d B for %d rows, %.2fx its %d B source), want <= %d B/row",
			perRow, retained, rows, float64(retained)/float64(sourceBytes), sourceBytes, transcriptRowBound)
	}
}

// firstRedrawDifference describes the first row where two redraws differ.
func firstRedrawDifference(got, want string) string {
	gotRows, wantRows := strings.Split(got, "\r\n"), strings.Split(want, "\r\n")
	for i := range max(len(gotRows), len(wantRows)) {
		var g, w string
		if i < len(gotRows) {
			g = gotRows[i]
		}
		if i < len(wantRows) {
			w = wantRows[i]
		}
		if g != w {
			return fmt.Sprintf("row %d of %d/%d:\n got  %q\n want %q", i, len(gotRows), len(wantRows), g, w)
		}
	}
	return "same rows"
}
