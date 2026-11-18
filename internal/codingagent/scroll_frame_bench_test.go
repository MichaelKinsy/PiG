package codingagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// What a fullscreen scroll frame and a content frame cost on the real transcript of interactive mode, by transcript and terminal size.
// Pi's make-scroll-frames-O(viewport) commit (b7bb7ca905b8) is measured the same way by tui/testdata/scroll_frame_bench.mjs. Both read
// the session from the spec file the generator in tui/scroll_frame_spec_test.go writes:
//
//	PIG_SCROLL_FRAME_SPEC=/path/spec.json go test ./tui -run TestWriteScrollFrameSpec
//	PIG_SCROLL_FRAME_SPEC=/path/spec.json go test ./internal/codingagent -run xxx -bench 'ScrollFrame|ContentFrame' -benchmem
//
// The history is rendered by renderInitialMessages from a persisted session (the resume path of interactive mode), mounted as
// createChatViewport mounts it: a primary follow-end ScrollView over Document(header, chat) above a dock.

type scrollFrameTurn struct {
	User     string `json:"user,omitempty"`
	Thinking string `json:"thinking"`
	Answer   string `json:"answer"`
	Command  string `json:"command"`
	Output   string `json:"output"`
}

func scrollFrameSpec(tb testing.TB, turns int) []scrollFrameTurn {
	tb.Helper()
	path := os.Getenv("PIG_SCROLL_FRAME_SPEC")
	if path == "" {
		tb.Skip("PIG_SCROLL_FRAME_SPEC is not set (tui TestWriteScrollFrameSpec writes it)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	var all []scrollFrameTurn
	if err := json.Unmarshal(data, &all); err != nil {
		tb.Fatal(err)
	}
	if turns > len(all) {
		tb.Fatalf("the spec has %d turns, need %d", len(all), turns)
	}
	return all[:turns]
}

// scrollFrameSizes are the transcripts of the table, by turns.
var scrollFrameSizes = []struct {
	name  string
	turns int
}{{"1k", 11}, {"26k", 285}, {"104k", 1140}}

var scrollFrameTerminals = []struct{ columns, rows int }{{120, 40}, {200, 60}}

type scrollFrameRig struct {
	ui          *tui.TuiAltScreen
	transcript  *tui.ScrollView
	mode        *InteractiveMode
	tail        *tui.AssistantMessageComponent
	streamTexts []string // the streamed message at one fixed length, one text per frame (see stream)
	streamed    int
	document    tui.Component
	columns     int
	rows        int
}

func newScrollFrameRig(tb testing.TB, turns, columns, rows int) *scrollFrameRig {
	tb.Helper()
	return newScrollFrameRigOutput(tb, turns, columns, rows, io.Discard, true)
}

// newScrollFrameRigOutput mounts the transcript; ownFrames takes the frame timer away, so that only RenderNow draws.
func newScrollFrameRigOutput(tb testing.TB, turns, columns, rows int, out io.Writer, ownFrames bool) *scrollFrameRig {
	tb.Helper()
	spec := scrollFrameSpec(tb, turns)
	dir := tb.TempDir()
	sess, err := NewSessionManagerWithDir(dir, dir).Create("scroll-frames", "")
	if err != nil {
		tb.Fatal(err)
	}
	ui := tui.NewTuiAltScreenWithOutput(out, columns, rows, tui.TuiAltScreenOptions{})
	if ownFrames {
		ui.SetRenderDeadline(func(time.Time) {}) // the benchmark owns the frames: no timer frame runs beside it
	}
	m := &InteractiveMode{
		opts:          InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: sess}},
		chatContainer: tui.NewContainer(),
		tuiInst:       ui,
		altScreen:     ui,
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		outputPad:     1,
	}
	for i, turn := range spec {
		call := fmt.Sprintf("call_h%d", i)
		messages := []agent.AgentMessage{}
		if turn.User != "" {
			messages = append(messages, userMsg(turn.User))
		}
		messages = append(messages,
			agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{
				ai.ThinkingContent{Thinking: turn.Thinking}, ai.TextContent{Text: turn.Answer},
				ai.ToolCall{ID: call, Name: "bash", Arguments: ai.JsonObject{"command": turn.Command}},
			}, StopReason: ai.StopReasonToolUse, Provider: "bench", ModelID: "bench-model", Timestamp: int64(i)}},
			agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "bash",
				Content: []ai.ToolResultMessageContent{ai.TextContent{Text: turn.Output}}, Timestamp: int64(i)}},
		)
		for _, message := range messages {
			if _, err := sess.AppendMessage(message); err != nil {
				tb.Fatal(err)
			}
		}
	}
	m.opts.CWD = "/work"
	m.opts.ResumePath = sess.Path()
	m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(m.opts.CWD, tb.TempDir(), true)
	m.renderInitialMessages()
	tail := m.newAssistantMessageBlock(messageMeta{})
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tail)
	document := tui.NewContainer(tui.NewPaddedText("transcript header", 0, 0, nil), m.chatContainer)
	transcript := tui.NewScrollView(document, tui.ScrollViewOptions{Follow: "end", Primary: true, Overscroll: "chain", Scrollbar: tui.ScrollbarAuto})
	tb.Cleanup(transcript.Dispose)
	root := tui.NewVStack([]tui.StackEntry{
		{Component: transcript, Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)},
		{Component: tui.NewPaddedText("\x1b[2m> editor dock\x1b[0m", 0, 0, nil), Grow: new(0), Shrink: new(1), MinSize: new(1)},
	}, tui.StackOptions{})
	ui.SetLayoutRoot(root)
	ui.Start()
	ui.RenderNow()
	return &scrollFrameRig{ui: ui, transcript: transcript, mode: m, tail: tail, document: document, columns: columns, rows: rows}
}

// lines is the rendered height of the transcript document at the rig's width.
func (r *scrollFrameRig) lines() int { return len(r.document.Render(r.columns)) }

// streamFrames is the number of distinct texts stream cycles through (more than any render cache of the message keeps).
const streamFrames = 256

// stream replaces the streaming message with its next text and draws a frame: a content frame. Every text has the same length (about
// 2 KB, the middle of the lengths a streamed answer passes through) and differs from the previous one in its last word, so every frame
// does the same work and allocates the same however many frames a benchmark runs (a message that grew by one word per frame and began
// again at 4,000 bytes made allocs/op depend on b.N: 316 and 404 on the same commit).
func (r *scrollFrameRig) stream() {
	if r.streamTexts == nil {
		body := strings.Repeat("token ", 333)
		r.streamTexts = make([]string, streamFrames)
		for i := range r.streamTexts {
			r.streamTexts[i] = fmt.Sprintf("%stail%03d", body, i)
		}
	}
	r.tail.SetContent([]tui.AssistantSegment{{Text: r.streamTexts[r.streamed%streamFrames]}})
	r.streamed++
	r.ui.RenderNow()
}

func (r *scrollFrameRig) wheelUp() {
	r.ui.HandleViewportInput(fmt.Sprintf("\x1b[<64;%d;%dM", 10, r.rows/2))
	r.ui.RenderNow()
	if r.transcript.ScrollTop() == 0 {
		r.transcript.ScrollToEnd()
	}
}

func (r *scrollFrameRig) pageUp() {
	r.ui.HandleViewportInput("\x1b[5~")
	r.ui.RenderNow()
	if r.transcript.ScrollTop() == 0 {
		r.transcript.ScrollToEnd()
	}
}

// dragStep moves the pressed scrollbar thumb between two rows of the track.
func (r *scrollFrameRig) dragStep(i int) {
	y := 5
	if i%2 == 1 {
		y = r.rows - 6
	}
	r.ui.HandleViewportInput(fmt.Sprintf("\x1b[<32;%d;%dM", r.columns, y))
	r.ui.RenderNow()
}

func (r *scrollFrameRig) pressScrollbar() {
	r.ui.HandleViewportInput(fmt.Sprintf("\x1b[<0;%d;%dM", r.columns, r.rows/2))
	r.ui.RenderNow()
}

func (r *scrollFrameRig) releaseScrollbar() {
	r.ui.HandleViewportInput(fmt.Sprintf("\x1b[<0;%d;%dm", r.columns, r.rows/2))
}

// BenchmarkScrollFrame is one frame per wheel event, per page key, and per scrollbar drag step, walking up from the end of the
// transcript (at the top it wraps to the end).
func BenchmarkScrollFrame(b *testing.B) {
	for _, term := range scrollFrameTerminals {
		for _, size := range scrollFrameSizes {
			prefix := fmt.Sprintf("%dx%d/%s", term.columns, term.rows, size.name)
			b.Run(prefix+"/wheel", func(b *testing.B) {
				r := newScrollFrameRig(b, size.turns, term.columns, term.rows)
				b.ReportMetric(float64(r.lines()), "doclines")
				b.ReportAllocs()
				for b.Loop() {
					r.wheelUp()
				}
			})
			b.Run(prefix+"/page", func(b *testing.B) {
				r := newScrollFrameRig(b, size.turns, term.columns, term.rows)
				b.ReportAllocs()
				for b.Loop() {
					r.pageUp()
				}
			})
			b.Run(prefix+"/drag", func(b *testing.B) {
				r := newScrollFrameRig(b, size.turns, term.columns, term.rows)
				r.pressScrollbar()
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					r.dragStep(i)
					i++
				}
				r.releaseScrollbar()
			})
		}
	}
}

// settleScrollbar waits until the scrollbar that a scroll shows has hidden itself (ScrollbarAuto hides it on a timer after the last
// scroll) and draws that state. A frame drawn before and a frame drawn after the timer differ by the cost of painting the thumb, so a
// benchmark that scrolls once and then measures frames would read the sooner frames dearer than the later ones, the more so the fewer
// frames it runs (the scrolled-up content frame read 395 allocs at one second of frames and 452 at 2,000 frames).
func (r *scrollFrameRig) settleScrollbar(tb testing.TB) {
	tb.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for r.transcript.IsScrollbarVisible() {
		if time.Now().After(deadline) {
			tb.Fatal("the scrollbar did not hide")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.ui.RenderNow()
}

// BenchmarkContentFrame is one streamed text drawn at the end of the transcript (the viewport follows it) and while the viewport
// is scrolled up in the middle of the history, with the scrollbar hidden as it is while a reader waits. Two frames before the loop
// build the texts and move the message from empty to its length, so that every measured frame is the same replacement of one
// same-length text by another.
func BenchmarkContentFrame(b *testing.B) {
	for _, term := range scrollFrameTerminals {
		for _, size := range scrollFrameSizes {
			for _, where := range []string{"bottom", "scrolled-up"} {
				b.Run(fmt.Sprintf("%dx%d/%s/%s", term.columns, term.rows, size.name, where), func(b *testing.B) {
					r := newScrollFrameRig(b, size.turns, term.columns, term.rows)
					if where == "scrolled-up" {
						r.transcript.ScrollTo(r.lines() / 2)
						r.ui.RenderNow()
						r.settleScrollbar(b)
					}
					r.stream()
					r.stream()
					b.ReportAllocs()
					for b.Loop() {
						r.stream()
					}
				})
			}
		}
	}
}

// BenchmarkContainerPublicRender is the cost of the public Container.Render over the whole transcript: what a scroll frame would pay if
// the layout called the public method, and what a caller outside the package pays.
func BenchmarkContainerPublicRender(b *testing.B) {
	for _, size := range scrollFrameSizes {
		b.Run(size.name, func(b *testing.B) {
			r := newScrollFrameRig(b, size.turns, 120, 40)
			chat := r.mode.chatContainer
			b.ReportMetric(float64(len(chat.Render(120))), "doclines")
			b.ReportAllocs()
			for b.Loop() {
				chat.Render(120)
			}
		})
	}
}

// TestScrollFrameTranscriptLines logs the transcript sizes and checks that every kind of scroll input moves the view.
func TestScrollFrameTranscriptLines(t *testing.T) {
	for _, term := range scrollFrameTerminals {
		for _, size := range scrollFrameSizes {
			r := newScrollFrameRig(t, size.turns, term.columns, term.rows)
			t.Logf("%dx%d %s: %d turns, %d document lines", term.columns, term.rows, size.name, size.turns, r.lines())
			top := r.transcript.ScrollTop()
			r.wheelUp()
			if r.transcript.ScrollTop() >= top {
				t.Fatalf("a wheel event did not scroll up: %d then %d", top, r.transcript.ScrollTop())
			}
			top = r.transcript.ScrollTop()
			r.pageUp()
			if r.transcript.ScrollTop() >= top {
				t.Fatalf("a page key did not scroll up: %d then %d", top, r.transcript.ScrollTop())
			}
			top = r.transcript.ScrollTop()
			r.pressScrollbar()
			r.dragStep(0)
			if r.transcript.ScrollTop() == top {
				t.Fatalf("a scrollbar drag did not scroll: %d", top)
			}
			r.releaseScrollbar()
		}
	}
}

// burstWriter counts the writes of the terminal: one frame is one write.
type burstWriter struct{ writes, bytes int }

func (w *burstWriter) Write(p []byte) (int, error) {
	w.writes++
	w.bytes += len(p)
	return len(p), nil
}

// TestWheelBurstFrames sends 600 wheel events 1 ms apart (50 up, 50 down, alternating) on the real frame timer and logs how many frames
// are drawn and the CPU time the process spent, per transcript size (Pi's counterpart: scroll_frame_bench.mjs burst mode).
func TestWheelBurstFrames(t *testing.T) {
	if os.Getenv("PIG_SCROLL_FRAME_BURST") == "" {
		t.Skip("PIG_SCROLL_FRAME_BURST is not set")
	}
	for _, term := range scrollFrameTerminals {
		for _, size := range scrollFrameSizes {
			w := &burstWriter{}
			r := newScrollFrameRigOutput(t, size.turns, term.columns, term.rows, w, false)
			ui := r.ui
			r.transcript.ScrollToEnd()
			ui.RenderNow()
			time.Sleep(100 * time.Millisecond)
			*w = burstWriter{}
			top := r.transcript.ScrollTop()
			cpuStart := processCPU(t)
			start := time.Now()
			for i := range 600 {
				// 50 events up, 50 down: the burst never stops at either end of the transcript.
				ui.HandleViewportInput(fmt.Sprintf("\x1b[<%d;%d;%dM", 64+(i/50)%2, 10, term.rows/2))
				time.Sleep(time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			wall := time.Since(start)
			cpu := processCPU(t) - cpuStart
			t.Logf("%dx%d %s: 600 wheel events: %d frames drawn (%d bytes), wall %v, cpu %v, scroll top %d -> %d", term.columns, term.rows, size.name, w.writes, w.bytes, wall.Round(time.Millisecond), cpu.Round(time.Millisecond), top, r.transcript.ScrollTop())
		}
	}
}

// TestDumpScrollFrameTurns writes what each top-level component of the first PIG_SCROLL_FRAME_DUMP_TURNS turns renders at 120 columns
// (ANSI stripped) to the file named by PIG_SCROLL_FRAME_DUMP, in the shape of scroll_frame_bench.mjs dump mode.
func TestDumpScrollFrameTurns(t *testing.T) {
	path := os.Getenv("PIG_SCROLL_FRAME_DUMP")
	if path == "" {
		t.Skip("PIG_SCROLL_FRAME_DUMP is not set")
	}
	n := 3
	if v := os.Getenv("PIG_SCROLL_FRAME_DUMP_TURNS"); v != "" {
		if _, err := fmt.Sscan(v, &n); err != nil {
			t.Fatal(err)
		}
	}
	osc := regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	r := newScrollFrameRig(t, n, 120, 40)
	type child struct {
		Type  string   `json:"type"`
		Lines []string `json:"lines"`
	}
	var children []child
	for _, c := range r.mode.chatContainer.Children() {
		lines := slices.Clone(c.Render(120))
		for i := range lines {
			lines[i] = osc.ReplaceAllString(stripANSITest(lines[i]), "")
		}
		children = append(children, child{fmt.Sprintf("%T", c), lines})
	}
	data, err := json.Marshal(map[string]any{"columns": 120, "children": children})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
