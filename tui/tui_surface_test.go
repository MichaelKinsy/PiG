package tui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type recordingSession struct {
	frames []frontend.Frame
	tree   map[frontend.Region][]surfaceEntry
	// mainCols and dockCols are what Columns reports; inputs that start with
	// "cols" are taken and set mainCols to the rest of the sequence.
	mainCols, dockCols int
	// colsMu guards mainCols and dockCols: a scheduled render reads them on a timer goroutine while a test sets them.
	colsMu sync.Mutex
	// calls records Suspend, Resume and each Apply as "apply", in order.
	calls []string
}

func newRecordingSession() *recordingSession {
	return &recordingSession{tree: map[frontend.Region][]surfaceEntry{}}
}

// Apply replays ops as a frontend must, so tests check the retained tree the
// frontend ends with, not only the op list.
func (s *recordingSession) Apply(frame frontend.Frame) error {
	s.frames = append(s.frames, frame)
	s.calls = append(s.calls, "apply")
	for _, op := range frame.Ops {
		nodes := s.tree[op.Region]
		switch op.Kind {
		case frontend.Insert:
			if op.Index < 0 || op.Index > len(nodes) {
				return fmt.Errorf("insert %s at %d of %d", op.ID, op.Index, len(nodes))
			}
			nodes = slices.Insert(nodes, op.Index, surfaceEntry{id: op.ID, node: op.Node})
		case frontend.Update:
			if op.Index >= len(nodes) || nodes[op.Index].id != op.ID {
				return fmt.Errorf("update %s at %d: not there", op.ID, op.Index)
			}
			nodes[op.Index].node = op.Node
		case frontend.Remove:
			if op.Index >= len(nodes) || nodes[op.Index].id != op.ID {
				return fmt.Errorf("remove %s at %d: not there", op.ID, op.Index)
			}
			nodes = slices.Delete(nodes, op.Index, op.Index+1)
		}
		s.tree[op.Region] = nodes
	}
	return nil
}

func (s *recordingSession) HandleInput(data string) bool {
	cols, ok := strings.CutPrefix(data, "cols")
	if ok {
		s.colsMu.Lock()
		defer s.colsMu.Unlock()
		_, _ = fmt.Sscan(cols, &s.mainCols)
	}
	return ok
}
func (*recordingSession) Close() error { return nil }
func (s *recordingSession) Suspend()   { s.calls = append(s.calls, "suspend") }
func (s *recordingSession) Resume()    { s.calls = append(s.calls, "resume") }
func (*recordingSession) InputReady()  {}
func (s *recordingSession) Columns() (main, dock int) {
	s.colsMu.Lock()
	defer s.colsMu.Unlock()
	return s.mainCols, s.dockCols
}

func (s *recordingSession) mainNodes() []frontend.Node {
	var nodes []frontend.Node
	for _, entry := range s.tree[frontend.RegionMain] {
		nodes = append(nodes, entry.node)
	}
	return nodes
}

func startedSurface(t *testing.T, document, dock Component) (*TuiSurface, *recordingSession) {
	t.Helper()
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	surface.SetLayout(document, dock)
	surface.Start()
	return surface, session
}

// assertReplayIsFresh checks the diff/replay invariant: the tree the session
// built by applying every frame holds the nodes a fresh surface sends in its
// first frame for the same document.
// renderOnlyWhenAsked stops the surface from rendering on a timer goroutine: a test that draws with surface.Render and then reads the
// session's frames and tree would otherwise race with, and count extra frames from, a render the surface scheduled itself.
func renderOnlyWhenAsked(surface *TuiSurface) {
	surface.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
}

func assertReplayIsFresh(t *testing.T, surface *TuiSurface, session *recordingSession) {
	t.Helper()
	fresh := newRecordingSession()
	fresh.mainCols, fresh.dockCols = session.mainCols, session.dockCols
	other := NewTuiSurfaceWithSize(fresh, surface.width, surface.height, func(err error) { t.Fatalf("apply: %v", err) })
	// The fresh surface draws once, on this goroutine: a scheduled render on a timer goroutine would write the tree this reads.
	renderOnlyWhenAsked(other)
	other.SetHooks(surface.hooks)
	other.SetLayout(surface.document, surface.dock)
	other.Start()
	other.Render()
	// The fresh surface releases the components it received changes from.
	defer other.Stop()
	for _, region := range []frontend.Region{frontend.RegionMain, frontend.RegionDock, frontend.RegionOverlay} {
		got, want := session.tree[region], fresh.tree[region]
		if len(got) != len(want) {
			t.Fatalf("%s: replayed %d nodes, fresh %d", region, len(got), len(want))
		}
		for i := range got {
			if !reflect.DeepEqual(got[i].node, want[i].node) {
				t.Fatalf("%s node %d: replayed %#v, fresh %#v", region, i, got[i].node, want[i].node)
			}
		}
	}
}

func TestDiffSurfaceRegionReplaysToNext(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	entries := func() []surfaceEntry {
		perm := rng.Perm(8)[:rng.IntN(8)]
		out := make([]surfaceEntry, len(perm))
		for i, n := range perm {
			out[i] = surfaceEntry{id: fmt.Sprintf("n%d", n), node: frontend.Lines{Lines: []string{fmt.Sprint(rng.IntN(3))}}}
		}
		return out
	}
	for range 2000 {
		prev, next := entries(), entries()
		session := newRecordingSession()
		session.tree[frontend.RegionMain] = slices.Clone(prev)
		ops, state := diffSurfaceRegion(nil, frontend.RegionMain, prev, next)
		if err := session.Apply(frontend.Frame{Ops: ops}); err != nil {
			t.Fatalf("prev %v next %v: %v", prev, next, err)
		}
		// An empty region may be nil or empty; both mean no nodes.
		same := func(a, b []surfaceEntry) bool { return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b)) }
		if !same(session.tree[frontend.RegionMain], next) {
			t.Fatalf("replay = %v, want %v", session.tree[frontend.RegionMain], next)
		}
		if !same(state, next) {
			t.Fatalf("state = %v, want %v", state, next)
		}
	}
}

func TestDiffSurfaceRegionSendsNothingForAnUnchangedTree(t *testing.T) {
	tree := []surfaceEntry{{id: "a", node: frontend.Lines{Lines: []string{"x"}}}, {id: "b", node: frontend.Lines{Lines: []string{"y"}}}}
	ops, _ := diffSurfaceRegion(nil, frontend.RegionMain, tree, slices.Clone(tree))
	if len(ops) != 0 {
		t.Fatalf("ops = %v", ops)
	}
}

// The first frame reports the session file, and a later frame only after it
// changed, as a frame of its own when no node changed; "" is a session that
// is not persisted.
func TestTuiSurfaceReportsTheSessionFileWhenItChanges(t *testing.T) {
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	file := "/sessions/a.jsonl"
	surface.SetHooks(SurfaceHooks{SessionFile: func() string { return file }})
	text := NewText("doc")
	surface.SetLayout(NewContainer(text), nil)
	surface.Start()
	text.SetText("doc 2")
	surface.Render()
	surface.Render()
	file = "/sessions/b.jsonl"
	surface.Render()
	file = ""
	surface.Render()
	surface.Render()
	file = "/sessions/a.jsonl"
	text.SetText("doc 3")
	surface.Render()

	var got []string
	for _, frame := range session.frames {
		entry := fmt.Sprintf("ops=%t", len(frame.Ops) > 0)
		if frame.SessionFile != nil {
			entry += " file=" + strconv.Quote(*frame.SessionFile)
		}
		got = append(got, entry)
	}
	want := []string{`ops=true file="/sessions/a.jsonl"`, "ops=true", `ops=false file="/sessions/b.jsonl"`, `ops=false file=""`, `ops=true file="/sessions/a.jsonl"`}
	if !slices.Equal(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
}

// The first frame reports the mark, and a later frame only after it changed,
// as a frame of its own when no node changed; each frame carries a copy, and
// a surface without the hook reports none.
func TestTuiSurfaceReportsTheMarkWhenItChanges(t *testing.T) {
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	pink := frontend.Mark{Width: 2, Height: 1, Pixels: []frontend.MarkPixel{{X: 0, Y: 0, Color: "#ffa8b7"}, {X: 1, Y: 0, Color: "#18141e"}}}
	blue := frontend.Mark{Width: 2, Height: 1, Pixels: []frontend.MarkPixel{{X: 0, Y: 0, Color: "#7ab8ff"}, {X: 1, Y: 0, Color: "#18141e"}}}
	mark, reads := pink, 0
	surface.SetHooks(SurfaceHooks{Mark: func() frontend.Mark { reads++; return mark }})
	text := NewText("doc")
	surface.SetLayout(NewContainer(text), nil)
	surface.Start()
	text.SetText("doc 2")
	surface.Render()
	surface.Render()
	mark = blue
	surface.Render()
	mark = frontend.Mark{Width: 2, Height: 1, Pixels: slices.Clone(blue.Pixels)}
	surface.Render()
	mark = frontend.Mark{Width: 3, Height: 1, Pixels: blue.Pixels}
	surface.Render()
	mark = frontend.Mark{}
	surface.Render()
	mark = pink
	text.SetText("doc 3")
	surface.Render()

	var got []string
	for _, frame := range session.frames {
		entry := fmt.Sprintf("ops=%t", len(frame.Ops) > 0)
		if frame.Mark != nil {
			entry += fmt.Sprintf(" mark=%v", *frame.Mark)
		}
		got = append(got, entry)
	}
	want := []string{
		"ops=true mark={2 1 [{0 0 #ffa8b7} {1 0 #18141e}]}", "ops=true",
		"ops=false mark={2 1 [{0 0 #7ab8ff} {1 0 #18141e}]}", "ops=false mark={3 1 [{0 0 #7ab8ff} {1 0 #18141e}]}",
		"ops=false mark={0 0 []}", "ops=true mark={2 1 [{0 0 #ffa8b7} {1 0 #18141e}]}",
	}
	if !slices.Equal(got, want) || reads != 8 {
		t.Fatalf("frames = %q (%d reads), want %q (8 reads)", got, reads, want)
	}
	session.frames[0].Mark.Pixels[0].Color = "#000000"
	if pink.Pixels[0].Color != "#ffa8b7" {
		t.Fatal("a frame's mark shares the hook's pixels")
	}

	plain := newRecordingSession()
	other := NewTuiSurfaceWithSize(plain, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(other)
	other.SetLayout(NewContainer(NewText("doc")), nil)
	other.Start()
	if len(plain.frames) != 1 || plain.frames[0].Mark != nil {
		t.Fatalf("a surface without the hook reported %#v", plain.frames)
	}
}

// Two tool calls in flight are two retained nodes. Finishing one updates only
// its own node; the other keeps its state (the D89-only spike left a stale
// pending card here because a rendered line could not address it).
func TestTuiSurfaceParallelToolsUpdateTheirOwnNodes(t *testing.T) {
	chat := NewContainer()
	card := func(name, args string) *ToolExecutionComponent {
		c := NewToolExecutionComponent(name, "", nil, ToolExecutionOptions{}, nil, nil, "")
		c.UpdateArgs(json.RawMessage(args))
		return c
	}
	readCard := card("read", `{"path":"notes.txt"}`)
	bashCard := card("bash", `{"command":"echo ok"}`)
	chat.Add(readCard)
	chat.Add(bashCard)
	surface, session := startedSurface(t, NewContainer(chat), nil)

	// The header is PiG's styled card header without its terminal styling.
	want := []frontend.Node{
		frontend.ToolCard{Name: "read", Arguments: map[string]any{"path": "notes.txt"}, Header: "read notes.txt", Status: frontend.ToolPending},
		frontend.ToolCard{Name: "bash", Arguments: map[string]any{"command": "echo ok"}, Header: "$ echo ok", Status: frontend.ToolPending},
	}
	if got := session.mainNodes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("first frame = %#v", got)
	}

	bashCard.MarkExecutionStarted()
	surface.Render()
	bashCard.SetResult("ok", false, 0)
	surface.Render()

	last := session.frames[len(session.frames)-1]
	if len(last.Ops) != 1 || last.Ops[0].Kind != frontend.Update || last.Ops[0].ID != session.tree[frontend.RegionMain][1].id {
		t.Fatalf("finishing bash sent %#v", last.Ops)
	}
	nodes := session.mainNodes()
	if got := nodes[0].(frontend.ToolCard).Status; got != frontend.ToolPending {
		t.Fatalf("read status = %s", got)
	}
	if got := nodes[1].(frontend.ToolCard); got.Status != frontend.ToolDone || got.Output != "ok" {
		t.Fatalf("bash = %#v", got)
	}
}

func TestTuiSurfaceToolStatusFollowsTheCardLifecycle(t *testing.T) {
	card := NewToolExecutionComponent("bash", "sleep 1", nil, ToolExecutionOptions{}, nil, nil, "")
	surface, session := startedSurface(t, NewContainer(card), nil)
	status := func() frontend.ToolStatus { return session.mainNodes()[0].(frontend.ToolCard).Status }
	if status() != frontend.ToolPending {
		t.Fatalf("new card = %s", status())
	}
	card.MarkExecutionStarted()
	surface.Render()
	if status() != frontend.ToolRunning {
		t.Fatalf("started card = %s", status())
	}
	card.SetResult("Command aborted", true, 0)
	surface.Render()
	if status() != frontend.ToolError {
		t.Fatalf("failed card = %s", status())
	}
}

// An aborted call is cancelled, not failed. The mark survives the tool's own
// "Operation aborted" error arriving later and yields to a success. The card
// draws the same terminal lines either way.
func TestTuiSurfaceReportsAnAbortedCallAsCancelled(t *testing.T) {
	running := NewToolExecutionComponent("bash", "sleep 9", nil, ToolExecutionOptions{}, nil, nil, "")
	running.MarkExecutionStarted()
	running.SetStreaming("partial")
	pending := NewToolExecutionComponent("read", "read notes.txt", nil, ToolExecutionOptions{}, nil, nil, "")
	failed := NewToolExecutionComponent("bash", "false", nil, ToolExecutionOptions{}, nil, nil, "")
	surface, session := startedSurface(t, NewContainer(running, pending, failed), nil)
	statuses := func() []frontend.ToolStatus {
		var out []frontend.ToolStatus
		for _, node := range session.mainNodes() {
			out = append(out, node.(frontend.ToolCard).Status)
		}
		return out
	}

	running.FinalizeAborted(time.Second)
	pending.SetResult("Operation aborted", true, 0)
	pending.MarkAborted()
	failed.SetResult("exit 1", true, 0)
	surface.Render()
	if got, want := statuses(), []frontend.ToolStatus{frontend.ToolCancelled, frontend.ToolCancelled, frontend.ToolError}; !slices.Equal(got, want) {
		t.Fatalf("after abort = %v, want %v", got, want)
	}
	plain := NewToolExecutionComponent("read", "read notes.txt", nil, ToolExecutionOptions{}, nil, nil, "")
	plain.SetResult("Operation aborted", true, 0)
	if got, want := pending.Render(60), plain.Render(60); !slices.Equal(got, want) {
		t.Fatalf("aborted card lines %q, plain error %q", got, want)
	}

	running.SetResult("Operation aborted", true, time.Second)
	pending.SetResult("notes", false, 0)
	surface.Render()
	if got, want := statuses(), []frontend.ToolStatus{frontend.ToolCancelled, frontend.ToolDone, frontend.ToolError}; !slices.Equal(got, want) {
		t.Fatalf("after late results = %v, want %v", got, want)
	}
	assertReplayIsFresh(t, surface, session)
}

func TestTuiSurfaceRemovesUnmountedEntriesAndForgetsTheirIDs(t *testing.T) {
	chat := NewContainer()
	first, second := NewText("first"), NewText("second")
	chat.Add(first)
	chat.Add(second)
	surface, session := startedSurface(t, NewContainer(chat), nil)
	chat.Remove(first)
	surface.Render()
	if got := len(session.tree[frontend.RegionMain]); got != 1 {
		t.Fatalf("main nodes = %d", got)
	}
	if _, ok := surface.ids[first]; ok {
		t.Fatal("removed component kept its id")
	}
}

func TestTuiSurfaceResendReplacesTheWholeTree(t *testing.T) {
	surface, session := startedSurface(t, NewContainer(NewText("doc")), NewText("dock"))
	surface.RepaintAll()
	last := session.frames[len(session.frames)-1]
	var removes, inserts int
	for _, op := range last.Ops {
		switch op.Kind {
		case frontend.Remove:
			removes++
		case frontend.Insert:
			inserts++
		}
	}
	if removes != 2 || inserts != 2 {
		t.Fatalf("repaint ops = %#v", last.Ops)
	}
	if len(session.tree[frontend.RegionMain]) != 1 || len(session.tree[frontend.RegionDock]) != 1 {
		t.Fatalf("tree after repaint = %#v", session.tree)
	}
}

func TestTuiSurfaceDrawsNothingUntilStartedOrAfterStop(t *testing.T) {
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 40, 10, nil)
	renderOnlyWhenAsked(surface)
	surface.SetLayout(NewContainer(NewText("doc")), nil)
	surface.Render()
	if len(session.frames) != 0 {
		t.Fatalf("frames before Start = %d", len(session.frames))
	}
	surface.Start()
	surface.Stop()
	frames := len(session.frames)
	surface.RepaintAll()
	if len(session.frames) != frames {
		t.Fatal("rendered after Stop")
	}
}

func TestTuiSurfaceStripsTheCursorMarkerFromDockLines(t *testing.T) {
	_, session := startedSurface(t, nil, NewText("type"+widthx.CursorMarker+"here"))
	lines := session.tree[frontend.RegionDock][0].node.(frontend.Lines).Lines
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, widthx.CursorMarker) || !strings.Contains(widthx.StripAnsi(joined), "typehere") {
		t.Fatalf("dock lines = %q", lines)
	}
}

// The surface reuses a component's node while its lines are unchanged; a
// rewrite of the same slice still reaches the session.
func TestTuiSurfaceUpdatesLinesRewrittenInPlace(t *testing.T) {
	component := &inPlaceComponent{lines: []string{"first", "second"}}
	surface, session := startedSurface(t, NewContainer(component), nil)
	frames := len(session.frames)
	surface.Render()
	if len(session.frames) != frames {
		t.Fatalf("an unchanged component sent a frame: %#v", session.frames[len(session.frames)-1])
	}
	component.lines[1] = "rewritten"
	surface.Render()
	got := session.tree[frontend.RegionMain][0].node.(frontend.Lines).Lines
	if !slices.Equal(got, []string{"first", "rewritten"}) {
		t.Fatalf("main lines = %q, want the rewritten lines", got)
	}
	if len(session.tree[frontend.RegionMain]) != 1 {
		t.Fatalf("main holds %d nodes, want 1", len(session.tree[frontend.RegionMain]))
	}
}

// The frontend lays main out narrower than the terminal (Tern's chat column),
// so PiG renders main's lines at the session's width and repaints when an
// input sequence changes it. Wider than the terminal is capped.
func TestTuiSurfaceRendersRegionsAtSessionWidths(t *testing.T) {
	text := strings.Repeat("word ", 20)
	session := newRecordingSession()
	session.mainCols, session.dockCols = 30, 0
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	surface.SetLayout(NewContainer(NewText(text)), NewText(text))
	surface.Start()
	widest := func(region frontend.Region) int {
		width := 0
		for _, line := range session.tree[region][0].node.(frontend.Lines).Lines {
			width = max(width, widthx.VisibleWidth(line))
		}
		return width
	}
	if main, dock := widest(frontend.RegionMain), widest(frontend.RegionDock); main > 30 || dock <= 30 || dock > 60 {
		t.Fatalf("widths main=%d dock=%d", main, dock)
	}
	if !surface.HandleFrontendInput("cols 45") {
		t.Fatal("input not taken")
	}
	surface.Render()
	if main := widest(frontend.RegionMain); main <= 30 || main > 45 {
		t.Fatalf("main width after change = %d", main)
	}
	session.mainCols = 500
	surface.RepaintAll()
	if main := widest(frontend.RegionMain); main > 60 {
		t.Fatalf("main width past the terminal = %d", main)
	}
}

// A tool drawn through registered renderers sends only its result rendering:
// the frontend draws the call from the arguments, so the call's lines would
// repeat it.
func TestTuiSurfaceSendsADefinitionsResultWithoutItsCall(t *testing.T) {
	card := NewToolExecutionComponent("read", "", nil, ToolExecutionOptions{}, nil, nil, "")
	card.UpdateArgs(json.RawMessage(`{"path":"notes.txt"}`))
	card.SetDefinition(&ToolDefinitionRenderers{
		Call:   func(ToolRenderInput) (Component, bool) { return NewText("custom call"), true },
		Result: func(ToolRenderInput) (Component, bool) { return NewText("custom result"), true },
	}, nil)
	surface, session := startedSurface(t, NewContainer(card), nil)
	if got := session.mainNodes()[0].(frontend.ToolCard); got.Result == nil || len(got.Result) != 0 {
		t.Fatalf("result before any = %#v", got.Result)
	}
	card.MarkExecutionStarted()
	card.SetResult("file text", false, 0)
	surface.Render()
	got := session.mainNodes()[0].(frontend.ToolCard)
	if joined := widthx.StripAnsi(strings.Join(got.Result, "\n")); !strings.Contains(joined, "custom result") || strings.Contains(joined, "custom call") {
		t.Fatalf("result = %q", got.Result)
	}
}

// Messages arrive as Markdown source with their role. An assistant message
// is one node per part under the message's id: a part keeps its node while
// later parts stream in, and only the streaming message's last text part is
// marked streaming.
func TestTuiSurfaceSendsMessagesAsMarkdown(t *testing.T) {
	chat := NewContainer()
	user := NewUserMessageComponent("Fix **the** build", nil, 1, nil)
	assistant := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	chat.Add(user)
	chat.Add(assistant)
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	streaming := true
	surface.SetHooks(SurfaceHooks{Streaming: func() *AssistantMessageComponent {
		if !streaming {
			return nil
		}
		return assistant
	}})
	surface.SetLayout(NewContainer(chat), nil)
	surface.Start()
	if got, want := session.mainNodes(), []frontend.Node{frontend.MarkdownText{Role: frontend.RoleUser, Text: "Fix **the** build"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before the reply = %#v", got)
	}

	assistant.SetTextDelta("Looking")
	surface.Render()
	textID := session.tree[frontend.RegionMain][1].id
	assistant.SetTextDelta(" at it.")
	surface.Render()
	last := session.frames[len(session.frames)-1]
	want := frontend.MarkdownText{Role: frontend.RoleAssistant, Text: "Looking at it.", Streaming: true}
	if len(last.Ops) != 1 || last.Ops[0].Kind != frontend.Update || last.Ops[0].ID != textID || !reflect.DeepEqual(last.Ops[0].Node, want) {
		t.Fatalf("streamed delta sent %#v", last.Ops)
	}

	assistant.SetContent([]AssistantSegment{{Text: "Looking at it."}, {Thinking: true, Text: "the test"}, {Text: "\n\nDone."}})
	streaming = false
	surface.Render()
	nodes := session.mainNodes()
	if len(nodes) != 4 || session.tree[frontend.RegionMain][1].id != textID {
		t.Fatalf("after the reply = %#v", nodes)
	}
	if got := nodes[1]; !reflect.DeepEqual(got, frontend.MarkdownText{Role: frontend.RoleAssistant, Text: "Looking at it."}) {
		t.Fatalf("first text = %#v", got)
	}
	if got := nodes[2]; !reflect.DeepEqual(got, frontend.Thinking{Text: "the test"}) {
		t.Fatalf("thinking = %#v", got)
	}
	if got := nodes[3]; !reflect.DeepEqual(got, frontend.MarkdownText{Role: frontend.RoleAssistant, Text: "Done."}) {
		t.Fatalf("second text = %#v", got)
	}

	assistant.SetTerminalError("aborted", "")
	surface.Render()
	nodes = session.mainNodes()
	if lines, ok := nodes[len(nodes)-1].(frontend.Lines); !ok || widthx.StripAnsi(strings.Join(lines.Lines, "\n")) != "Operation aborted" {
		t.Fatalf("error part = %#v", nodes[len(nodes)-1])
	}
	assertReplayIsFresh(t, surface, session)
}

// A thinking run is a Thinking node with its source: consecutive thinking
// blocks form one run, hiding thinking hides every run, and a run that is
// still arriving streams. The ANSI lines stay Pi's: hidden runs draw the
// "Thinking..." label.
func TestTuiSurfaceSendsThinkingRuns(t *testing.T) {
	assistant := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	streaming := true
	surface.SetHooks(SurfaceHooks{Streaming: func() *AssistantMessageComponent {
		if !streaming {
			return nil
		}
		return assistant
	}})
	surface.SetLayout(NewContainer(assistant), nil)
	surface.Start()

	assistant.SetThinkingDelta("Reading")
	surface.Render()
	assistant.SetThinkingDelta(" the *file*")
	surface.Render()
	last := session.frames[len(session.frames)-1]
	if want := (frontend.Thinking{Text: "Reading the *file*", Streaming: true}); len(last.Ops) != 1 || last.Ops[0].Kind != frontend.Update || !reflect.DeepEqual(last.Ops[0].Node, want) {
		t.Fatalf("streamed thinking sent %#v", last.Ops)
	}

	assistant.SetContent([]AssistantSegment{{Thinking: true, Text: "Reading the *file*"}, {Thinking: true, Text: " twice "}, {Text: "Done."}})
	streaming = false
	assistant.SetHideThinkingBlock(true)
	surface.Render()
	want := []frontend.Node{
		frontend.Thinking{Text: "Reading the *file*\n\ntwice", Hidden: true},
		frontend.MarkdownText{Role: frontend.RoleAssistant, Text: "Done."},
	}
	if got := session.mainNodes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after hiding = %#v", got)
	}
	if joined := widthx.StripAnsi(strings.Join(assistant.Render(60), "\n")); !strings.Contains(joined, "Thinking...") || strings.Contains(joined, "twice") {
		t.Fatalf("hidden thinking lines = %q", joined)
	}
	assertReplayIsFresh(t, surface, session)
}

// A finished call whose result carries a diff reports it on its card; the
// hook sees the call's name, decoded arguments and recorded result value.
// A running or failed call reports none.
func TestTuiSurfaceSendsAToolResultsDiff(t *testing.T) {
	type result struct{ patch string }
	card := NewToolExecutionComponent("edit", "", nil, ToolExecutionOptions{}, nil, nil, "")
	card.UpdateArgs(json.RawMessage(`{"path":"a.go"}`))
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	surface.SetHooks(SurfaceHooks{ToolDiff: func(name string, arguments map[string]any, value any) *frontend.Diff {
		r, ok := value.(result)
		if name != "edit" || !ok {
			return nil
		}
		return &frontend.Diff{Path: arguments["path"].(string), Text: r.patch}
	}})
	surface.SetLayout(NewContainer(card), nil)
	surface.Start()
	diff := func() *frontend.Diff { return session.mainNodes()[0].(frontend.ToolCard).Diff }

	card.MarkExecutionStarted()
	card.SetResultValue(result{"@@ -1 +1 @@\n-a\n+b\n"})
	surface.Render()
	if diff() != nil {
		t.Fatalf("running card diff = %#v", diff())
	}
	card.SetResult("Successfully replaced 1 block(s) in a.go.", false, 0)
	surface.Render()
	if got, want := diff(), (&frontend.Diff{Path: "a.go", Text: "@@ -1 +1 @@\n-a\n+b\n"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("finished card diff = %#v", got)
	}
	assertReplayIsFresh(t, surface, session)
	card.SetResult("Could not find the text", true, 0)
	surface.Render()
	if diff() != nil {
		t.Fatalf("failed card diff = %#v", diff())
	}
}

// A finished call's card carries the result's images that Pi's terminal
// renderer shows (tool-execution.ts:338-368): those with data and a MIME
// type, in order, decoded, at the width renderImages draws them. A frame
// that changes nothing keeps the same images without decoding them again,
// and hiding images removes them.
func TestTuiSurfaceSendsAToolResultsImages(t *testing.T) {
	pngData := makePNGBase64(t, 3, 2)
	jpegData := makeJPEGBase64(t, 4, 4)
	card := NewToolExecutionComponent("read", "", nil, ToolExecutionOptions{}, nil, nil, "")
	card.UpdateArgs(json.RawMessage(`{"path":"dot.png"}`))
	card.ImageWidthCells = 20
	surface, session := startedSurface(t, NewContainer(card), nil)
	images := func() []frontend.ViewImage { return session.mainNodes()[0].(frontend.ToolCard).Images }
	if images() != nil {
		t.Fatalf("card without a result has images %#v", images())
	}

	card.ImageBlocks = []ImageBlock{
		{Data: pngData, MIMEType: "image/png"},
		{Data: "", MIMEType: "image/png"},
		{Data: jpegData, MIMEType: ""},
		{Data: "not base64!", MIMEType: "image/gif"},
		{Data: jpegData, MIMEType: "image/jpeg"},
	}
	card.SetResult("Read image file [image/png]", false, 0)
	surface.Render()
	decoded := func(data string) ([]byte, string) {
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return b, hex.EncodeToString(sum[:])
	}
	pngBytes, pngRef := decoded(pngData)
	jpegBytes, jpegRef := decoded(jpegData)
	want := []frontend.ViewImage{
		{Ref: pngRef, MimeType: "image/png", Data: pngBytes, MaxWidthCells: 20},
		{MimeType: "image/gif", MaxWidthCells: 20},
		{Ref: jpegRef, MimeType: "image/jpeg", Data: jpegBytes, MaxWidthCells: 20},
	}
	if got := images(); !reflect.DeepEqual(got, want) {
		t.Fatalf("images = %#v", got)
	}
	first := images()

	frames := len(session.frames)
	card.Invalidate()
	surface.Render()
	if got := card.frontendTool(60, nil).Images; &got[0] != &first[0] {
		t.Fatal("an unchanged card built its images again")
	}
	for _, frame := range session.frames[frames:] {
		if len(frame.Ops) != 0 {
			t.Fatalf("an unchanged card sent %#v", frame.Ops)
		}
	}

	// A wider setting than the card draws the images at the card's width
	// less 2, as renderImages does, from the same bytes.
	card.SetImageWidthCells(100)
	surface.Render()
	if got := images(); len(got) != 3 || got[0].MaxWidthCells != 58 || &got[0].Data[0] != &first[0].Data[0] {
		t.Fatalf("images at width 100 = %#v", got)
	}
	assertReplayIsFresh(t, surface, session)

	// The width is the one the terminal's Image draws at
	// (image.ts render): at least 1 on a card narrower than 3 columns, and
	// the default 60 cells when the setting is unset.
	for _, tc := range []struct{ setting, width, want int }{{20, 2, 1}, {20, 1, 1}, {0, 60, 58}, {0, 80, 60}} {
		card.ImageWidthCells = tc.setting
		if got := card.frontendTool(tc.width, nil).Images; len(got) != 3 || got[0].MaxWidthCells != tc.want {
			t.Fatalf("setting %d at width %d: %d images, first %d cells wide, want 3 and %d", tc.setting, tc.width, len(got), maxWidthCells(got), tc.want)
		}
	}
	card.SetImageWidthCells(100)

	card.SetShowImages(false)
	surface.Render()
	if images() != nil {
		t.Fatalf("hidden images = %#v", images())
	}
}

// A user message's images reach a frontend on its MarkdownText, decoded
// once: a frame that changes nothing reuses the same slice and sends no
// update. The terminal lines stay Pi's, which draws no user images.
func TestTuiSurfaceSendsAUserMessagesImages(t *testing.T) {
	pngData := makePNGBase64(t, 3, 2)
	plain := NewUserMessageComponent("Look at this", nil, 1, nil)
	user := NewUserMessageComponent("Look at this", nil, 1, nil)
	user.SetImages([]ImageBlock{{Data: pngData, MIMEType: "image/png"}, {Data: "not base64!", MIMEType: "image/gif"}})
	if got, want := user.Render(60), plain.Render(60); !slices.Equal(got, want) {
		t.Fatalf("terminal lines with images =\n%q\nwant\n%q", got, want)
	}
	surface, session := startedSurface(t, NewContainer(user), nil)
	data, err := base64.StdEncoding.DecodeString(pngData)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	want := frontend.MarkdownText{Role: frontend.RoleUser, Text: "Look at this", Images: []frontend.ViewImage{
		{Ref: hex.EncodeToString(sum[:]), MimeType: "image/png", Data: data},
		{MimeType: "image/gif"},
	}}
	if got := session.mainNodes(); !reflect.DeepEqual(got, []frontend.Node{want}) {
		t.Fatalf("nodes = %#v", got)
	}
	first, _ := user.frontendNode()
	frames := len(session.frames)
	user.Invalidate()
	surface.Render()
	if again, _ := user.frontendNode(); &again.Images[0] != &first.Images[0] {
		t.Fatal("an unchanged message decoded its images again")
	}
	for _, frame := range session.frames[frames:] {
		if len(frame.Ops) != 0 {
			t.Fatalf("an unchanged message sent %#v", frame.Ops)
		}
	}
	if node, _ := plain.frontendNode(); node.Images != nil {
		t.Fatalf("a message without images has %#v", node.Images)
	}
}

// editorDock mounts editor between a status line and a footer, as
// interactive mode docks it, and reports it through the hooks.
func editorDock(t *testing.T, editor *Editor, sendable *bool) (*TuiSurface, *recordingSession, *Container) {
	t.Helper()
	editorContainer := NewContainer(editor)
	surface, session := startedSurface(t, nil, NewContainer(NewText("status"), editorContainer, NewText("footer")))
	surface.SetHooks(SurfaceHooks{
		Editor:         func() *Editor { return editor },
		EditorSendable: func() bool { return *sendable },
	})
	surface.RepaintAll()
	return surface, session, editorContainer
}

func dockIDs(session *recordingSession) []string {
	var ids []string
	for _, entry := range session.tree[frontend.RegionDock] {
		ids = append(ids, entry.id)
	}
	return ids
}

func dockText(session *recordingSession, id string) string {
	for _, entry := range session.tree[frontend.RegionDock] {
		if entry.id == id {
			return widthx.StripAnsi(strings.Join(entry.node.(frontend.Lines).Lines, "\n"))
		}
	}
	return ""
}

// The dock reports the input editor as an Editor node between the lines
// drawn above and below its text. Text and cursor follow the editor, the
// cursor in UTF-16 code units, and a change of the sendable flag alone
// updates only the editor node.
func TestTuiSurfaceReportsTheInputEditorAsAnEditorNode(t *testing.T) {
	editor := NewEditor()
	sendable := false
	surface, session, _ := editorDock(t, editor, &sendable)
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below"}) {
		t.Fatalf("dock ids = %q", ids)
	}
	if above, below := dockText(session, "dock"), dockText(session, "dock.below"); !strings.Contains(above, "status") || strings.Contains(above, "footer") || !strings.Contains(below, "footer") || strings.Contains(below, "status") {
		t.Fatalf("above = %q, below = %q", above, below)
	}
	if got := session.tree[frontend.RegionDock][1].node; !reflect.DeepEqual(got, frontend.Editor{}) {
		t.Fatalf("empty editor = %#v", got)
	}

	editor.SetText("héllo\n😀x")
	editor.ApplyEdit(9, 9, "", 8)
	surface.Render()
	if got, want := session.tree[frontend.RegionDock][1].node, (frontend.Editor{Text: "héllo\n😀x", Cursor: 8}); !reflect.DeepEqual(got, want) {
		t.Fatalf("editor = %#v, want %#v", got, want)
	}

	frames := len(session.frames)
	sendable = true
	surface.Render()
	if len(session.frames) != frames+1 {
		t.Fatalf("frames after the flag flipped = %d, want %d", len(session.frames), frames+1)
	}
	ops := session.frames[frames].Ops
	if len(ops) != 1 || ops[0].Kind != frontend.Update || ops[0].ID != "editor" || !ops[0].Node.(frontend.Editor).Sendable {
		t.Fatalf("ops for the flag = %#v", ops)
	}
	assertReplayIsFresh(t, surface, session)
}

// A component the dock does not model in the editor's place, an overlay that
// takes the keys, and an extension's editor component all return the dock to
// one Lines node, and the editor node comes back when the editor does. The
// overlay itself arrives in the overlay region, not drawn over the dock.
func TestTuiSurfaceReportsTheDockAsLinesWithoutAMountedEditor(t *testing.T) {
	editor := NewEditor()
	sendable := true
	surface, session, editorContainer := editorDock(t, editor, &sendable)

	editorContainer.Clear()
	editorContainer.Add(NewText("selector"))
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock"}) || !strings.Contains(dockText(session, "dock"), "selector") {
		t.Fatalf("dock with a selector = %q %q", ids, dockText(session, "dock"))
	}
	editorContainer.Clear()
	editorContainer.Add(editor)
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below"}) {
		t.Fatalf("dock after the selector = %q", ids)
	}

	handle := surface.ShowOverlay(NewText("overlay"), OverlayOptions{})
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock"}) || strings.Contains(dockText(session, "dock"), "overlay") {
		t.Fatalf("dock with an overlay = %q %q", ids, dockText(session, "dock"))
	}
	if overlays := session.tree[frontend.RegionOverlay]; len(overlays) != 1 || !strings.Contains(strings.Join(overlays[0].node.(frontend.Overlay).Lines, "\n"), "overlay") {
		t.Fatalf("overlay region = %#v", overlays)
	}
	handle.Close()
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below"}) || len(session.tree[frontend.RegionOverlay]) != 0 {
		t.Fatalf("dock after the overlay = %q, overlays %d", ids, len(session.tree[frontend.RegionOverlay]))
	}
	assertReplayIsFresh(t, surface, session)
}

// viewedLines is a component whose lines a view describes, as an extension's
// ui.custom component is (D107).
type viewedLines struct{ rows int }

func (v viewedLines) Render(width int) []string {
	lines := make([]string, v.rows)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %d", i)
	}
	return lines
}
func (viewedLines) Invalidate() {}
func (v viewedLines) FrontendView(width int) *frontend.View {
	return &frontend.View{Root: frontend.ViewNode{Kind: frontend.ViewKindText, Rows: v.rows, Width: width}, Focus: "list", Seq: 7, Theme: map[string]string{"accent": "#d75f00"}}
}

// A titled overlay (the legacy modal) reports its component's view inside
// the box it draws: the borders and padding rows are lines ranges around it,
// and every row of the overlay is covered. A box that cuts the component's
// rows reports no view.
func TestTuiSurfaceTitledOverlayCarriesItsComponentsView(t *testing.T) {
	surface, session := startedSurface(t, NewContainer(NewText("doc")), nil)
	handle := surface.ShowOverlay(viewedLines{rows: 3}, OverlayOptions{Title: "Pick"})
	surface.Render()
	overlays := session.tree[frontend.RegionOverlay]
	if len(overlays) != 1 {
		t.Fatalf("overlay region = %#v", overlays)
	}
	node := overlays[0].node.(frontend.Overlay)
	view := node.View
	if view == nil {
		t.Fatal("a titled overlay dropped its component's view")
	}
	root := view.Root
	if root.Kind != frontend.ViewKindContainer || root.Rows != len(node.Lines) || root.Width != node.Width || view.Focus != "list" || view.Seq != 7 || view.Theme["accent"] != "#d75f00" {
		t.Fatalf("view = %+v, overlay %d rows wide %d", view, len(node.Lines), node.Width)
	}
	kids := root.Children
	if len(kids) != 4 || kids[0].Kind != frontend.ViewKindLines || !strings.Contains(kids[0].Lines[0], "Pick") ||
		kids[1].Kind != frontend.ViewKindHStack || kids[1].Rows != 3 || kids[3].Lines[0] != node.Lines[len(node.Lines)-1] {
		t.Fatalf("children = %+v", kids)
	}
	inner := kids[1].Children
	if len(inner) != 3 || inner[1].Kind != frontend.ViewKindText || inner[1].Width != node.Width-2 || inner[0].Lines[0] != "│" || inner[2].Rows != 3 {
		t.Fatalf("hstack = %+v", inner)
	}
	if pad := kids[2]; pad.Kind != frontend.ViewKindLines || pad.Rows != len(node.Lines)-5 {
		t.Fatalf("padding = %+v", pad)
	}
	handle.Close()

	surface.ShowOverlay(viewedLines{rows: 40}, OverlayOptions{Title: "Pick"})
	surface.Render()
	if overlays := session.tree[frontend.RegionOverlay]; len(overlays) != 1 || overlays[0].node.(frontend.Overlay).View != nil {
		t.Fatalf("a box that cut its component's rows reported a view: %#v", overlays)
	}
}

// opsSince returns the ops of the frames applied after the first n.
func opsSince(session *recordingSession, n int) []frontend.Op {
	var ops []frontend.Op
	for _, frame := range session.frames[n:] {
		ops = append(ops, frame.Ops...)
	}
	return ops
}

// A native editor draws no ANSI borders: the dock around it holds neither
// border, nor the scroll counts the terminal editor shows. The borders stay
// wherever the editor is drawn as lines, as under an overlay.
func TestTuiSurfaceDropsTheEditorBordersAroundANativeEditor(t *testing.T) {
	editor := NewEditor()
	editor.SetText(strings.Repeat("line\n", 9) + "last")
	sendable := false
	surface, session, _ := editorDock(t, editor, &sendable)
	if ansi := widthx.StripAnsi(strings.Join(editor.Render(60), "\n")); !strings.Contains(ansi, "more") || !strings.Contains(ansi, "─") {
		t.Fatalf("the terminal editor shows no border or scroll count: %q", ansi)
	}
	above, below := dockText(session, "dock"), dockText(session, "dock.below")
	if strings.Contains(above, "─") || strings.Contains(below, "─") || strings.Contains(above, "more") || strings.Contains(below, "more") {
		t.Fatalf("above = %q, below = %q", above, below)
	}
	if strings.TrimSpace(above) != "status" || strings.TrimSpace(below) != "footer" {
		t.Fatalf("above = %q, below = %q", above, below)
	}
	if got := session.tree[frontend.RegionDock][1].node.(frontend.Editor); got.Text != editor.Text() {
		t.Fatalf("editor text = %q", got.Text)
	}

	handle := surface.ShowOverlay(NewText("overlay"), OverlayOptions{})
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock"}) || !strings.Contains(dockText(session, "dock"), "─") {
		t.Fatalf("dock under an overlay = %q %q", ids, dockText(session, "dock"))
	}
	handle.Close()
	surface.Render()
	if strings.Contains(dockText(session, "dock"), "─") {
		t.Fatalf("border back after the overlay: %q", dockText(session, "dock"))
	}
	assertReplayIsFresh(t, surface, session)
}

// The working indicator is its own node above the editor, embedded in the
// editor's border or mounted on its own: starting it inserts the node,
// a new message updates it, a spinner tick sends nothing, and stopping it
// removes it. Only while the hook reports it does a mounted indicator leave
// the dock's lines.
func TestTuiSurfaceReportsTheWorkingIndicatorAboveTheEditor(t *testing.T) {
	editor := NewEditor()
	editor.EmbedWorkingStatus = true
	status := NewContainer()
	surface, session := startedSurface(t, nil, NewContainer(status, NewContainer(editor), NewText("footer")))
	var working *frontend.Working
	surface.SetHooks(SurfaceHooks{
		Editor: func() *Editor { return editor },
		Working: func() (frontend.Working, bool) {
			if working == nil {
				return frontend.Working{}, false
			}
			return *working, true
		},
	})
	surface.RepaintAll()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below"}) {
		t.Fatalf("idle dock = %q", ids)
	}

	indicator := &StatusIndicator{Kind: "working", Loader: newPlainLoader("Working")}
	editor.SetWorkingStatusIndicator(indicator)
	working = &frontend.Working{Kind: frontend.WorkingAgent, Message: "Working", Interval: 80 * time.Millisecond}
	frames := len(session.frames)
	surface.Render()
	ops := opsSince(session, frames)
	if len(ops) != 1 || ops[0].Kind != frontend.Insert || ops[0].ID != "working" || ops[0].Index != 1 || !reflect.DeepEqual(ops[0].Node, *working) {
		t.Fatalf("ops when work starts = %#v", ops)
	}
	if above := dockText(session, "dock"); strings.Contains(above, "Working") {
		t.Fatalf("the status also draws as lines: %q", above)
	}

	frames = len(session.frames)
	for range 3 {
		indicator.Tick()
		surface.Render()
	}
	if len(session.frames) != frames {
		t.Fatalf("spinner ticks sent %#v", opsSince(session, frames))
	}

	indicator.SetMessage("Reading")
	working.Message = "Reading"
	surface.Render()
	ops = opsSince(session, frames)
	if len(ops) != 1 || ops[0].Kind != frontend.Update || ops[0].ID != "working" || ops[0].Node.(frontend.Working).Message != "Reading" {
		t.Fatalf("ops for a new message = %#v", ops)
	}

	editor.EmbedWorkingStatus = false
	editor.SetWorkingStatusIndicator(nil)
	status.Add(indicator)
	frames = len(session.frames)
	surface.Render()
	if len(session.frames) != frames || strings.Contains(dockText(session, "dock"), "Reading") {
		t.Fatalf("a mounted indicator changed the dock: %#v", opsSince(session, frames))
	}

	working = nil
	status.Clear()
	surface.Render()
	ops = opsSince(session, frames)
	if len(ops) != 1 || ops[0].Kind != frontend.Remove || ops[0].ID != "working" {
		t.Fatalf("ops when work stops = %#v", ops)
	}

	status.Add(indicator)
	surface.Render()
	if !strings.Contains(dockText(session, "dock"), "Reading") {
		t.Fatalf("an indicator the hook does not report is missing from the lines: %q", dockText(session, "dock"))
	}
	assertReplayIsFresh(t, surface, session)
}

// The footer component is the dock's last node, not lines: a change of its
// data is one update of that node, an unchanged footer sends nothing, and
// while the hook does not report it (an extension's footer replaces it)
// the node goes and the extension's footer draws in the lines below.
func TestTuiSurfaceReportsTheFooterAsTheLastDockNode(t *testing.T) {
	editor := NewEditor()
	extFooter := NewText("")
	footerLines := NewText("footer lines")
	surface, session := startedSurface(t, nil, NewContainer(NewContainer(editor), extFooter, footerLines))
	footer := &frontend.Footer{Cwd: "~/pig", Model: "faux-1", ThinkingLevel: "off", AutoCompact: true, ContextUsage: frontend.ContextUsage{ContextWindow: 128000}}
	surface.SetHooks(SurfaceHooks{
		Editor: func() *Editor { return editor },
		Footer: func(c Component) (frontend.Footer, bool) {
			if c != Component(footerLines) || footer == nil {
				return frontend.Footer{}, false
			}
			return *footer, true
		},
	})
	surface.RepaintAll()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below", "footer"}) {
		t.Fatalf("dock = %q", ids)
	}
	if below := dockText(session, "dock.below"); strings.Contains(below, "footer lines") {
		t.Fatalf("the footer also draws as lines: %q", below)
	}

	frames := len(session.frames)
	surface.Render()
	if len(session.frames) != frames {
		t.Fatalf("an unchanged footer sent %#v", opsSince(session, frames))
	}
	rate := 75.0
	for _, change := range []func(){
		func() { footer.ThinkingLevel = "high" },
		func() { footer.Model, footer.Provider = "gpt-5", "openai" },
		func() {
			footer.UsageTotals = frontend.UsageTotals{Input: 1200, Output: 300, CacheRead: 900, Cost: 0.012}
			footer.LatestCacheHitRate = &rate
		},
		func() { footer.ContextUsage = frontend.ContextUsage{Tokens: 96000, Percent: 75, ContextWindow: 128000} },
		func() { footer.ExtensionStatuses = []string{"lint ok"} },
	} {
		change()
		frames = len(session.frames)
		surface.Render()
		ops := opsSince(session, frames)
		if len(ops) != 1 || ops[0].Kind != frontend.Update || ops[0].ID != "footer" || !reflect.DeepEqual(ops[0].Node, *footer) {
			t.Fatalf("ops for a footer change = %#v", ops)
		}
	}

	footer = nil
	footerLines.SetText("")
	extFooter.SetText("ext footer")
	frames = len(session.frames)
	surface.Render()
	if ops := opsSince(session, frames); len(ops) != 2 || ops[0].Kind != frontend.Remove || ops[0].ID != "footer" || ops[1].ID != "dock.below" {
		t.Fatalf("ops when an extension's footer replaces it = %#v", ops)
	}
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below"}) || !strings.Contains(dockText(session, "dock.below"), "ext footer") {
		t.Fatalf("dock with an extension's footer = %q %q", ids, dockText(session, "dock.below"))
	}
	assertReplayIsFresh(t, surface, session)
}

// Working and Footer nodes compare by value, a NaN cost included, so the
// dock's diff neither misses a change nor repeats an unchanged node.
func TestNodesEqualComparesWorkingAndFooterByValue(t *testing.T) {
	low, high := 10.0, 20.0
	base := frontend.Footer{Model: "m", LatestCacheHitRate: &low, ExtensionStatuses: []string{"a"}, UsageTotals: frontend.UsageTotals{Cost: math.NaN()}}
	same := base
	lowCopy := low
	same.LatestCacheHitRate = &lowCopy
	same.ExtensionStatuses = []string{"a"}
	if !nodesEqual(base, same) {
		t.Fatal("equal footers differ")
	}
	for name, change := range map[string]func(*frontend.Footer){
		"rate":     func(f *frontend.Footer) { f.LatestCacheHitRate = &high },
		"no rate":  func(f *frontend.Footer) { f.LatestCacheHitRate = nil },
		"statuses": func(f *frontend.Footer) { f.ExtensionStatuses = []string{"b"} },
		"none":     func(f *frontend.Footer) { f.ExtensionStatuses = nil },
		"cost":     func(f *frontend.Footer) { f.UsageTotals.Cost = 1 },
		"tokens":   func(f *frontend.Footer) { f.UsageTotals.Input = 1 },
		"context":  func(f *frontend.Footer) { f.ContextUsage.Unknown = true },
		"routed":   func(f *frontend.Footer) { f.Routed.Model = "r" },
		"cwd":      func(f *frontend.Footer) { f.Cwd = "~" },
		"thinking": func(f *frontend.Footer) { f.ThinkingLevel = "low" },
	} {
		other := same
		change(&other)
		if nodesEqual(base, other) {
			t.Fatalf("%s: different footers are equal", name)
		}
	}
	working := frontend.Working{Kind: frontend.WorkingAgent, Message: "Working", Frames: []string{"a"}}
	for name, other := range map[string]frontend.Working{
		"default frames": {Kind: frontend.WorkingAgent, Message: "Working"},
		"no frames":      {Kind: frontend.WorkingAgent, Message: "Working", Frames: []string{}},
		"message":        {Kind: frontend.WorkingAgent, Message: "Reading", Frames: []string{"a"}},
		"kind":           {Kind: frontend.WorkingRetry, Message: "Working", Frames: []string{"a"}},
		"interval":       {Kind: frontend.WorkingAgent, Message: "Working", Frames: []string{"a"}, Interval: time.Second},
	} {
		if nodesEqual(working, other) {
			t.Fatalf("%s: different indicators are equal", name)
		}
	}
	if !nodesEqual(frontend.Working{Frames: []string{}}, frontend.Working{Frames: []string{}}) || nodesEqual(working, base) {
		t.Fatal("indicator equality")
	}
}

// Each native edit is one undo step; a move of the cursor alone is none.
func TestEditorApplyEditIsOneUndoStep(t *testing.T) {
	editor := NewEditor()
	editor.SetText("hello world")
	editor.ApplyEdit(6, 11, "there", 11)
	if got := editor.Text(); got != "hello there" || editor.cursorOffset() != 11 {
		t.Fatalf("after replace: %q at %d", got, editor.cursorOffset())
	}
	editor.ApplyEdit(0, 0, "", 2)
	if editor.cursorOffset() != 2 {
		t.Fatalf("cursor after a move = %d", editor.cursorOffset())
	}
	editor.ApplyEdit(5, 5, "\n", 6)
	if got := editor.Text(); got != "hello\n there" || editor.cursorOffset() != 6 {
		t.Fatalf("after newline: %q at %d", got, editor.cursorOffset())
	}
	editor.Undo()
	if got := editor.Text(); got != "hello there" {
		t.Fatalf("first undo = %q", got)
	}
	editor.Undo()
	if got := editor.Text(); got != "hello world" {
		t.Fatalf("second undo = %q", got)
	}
}

// Inserted text is normalized as typed text is, the cursor after it moving
// with the change in length, and offsets past the text are clamped.
func TestEditorApplyEditNormalizesAndClamps(t *testing.T) {
	editor := NewEditor()
	editor.SetText("ab")
	editor.ApplyEdit(1, 1, "\t\r\n", 4)
	if got := editor.Text(); got != "a    \nb" || editor.cursorOffset() != 6 {
		t.Fatalf("after tab: %q at %d", got, editor.cursorOffset())
	}
	editor.ApplyEdit(5, 99, "😀", 99)
	if got := editor.Text(); got != "a    😀" || editor.cursorOffset() != 7 {
		t.Fatalf("after clamp: %q at %d", got, editor.cursorOffset())
	}
}

// A frame where only a node's images change updates that node once: a tool
// card whose result gains an image with the same output and status, a user
// message whose images are swapped, and each of them when an image's bytes
// change but its byte length, MIME type and filename stay the same.
func TestTuiSurfaceUpdatesANodeWhoseImagesAloneChange(t *testing.T) {
	card := NewToolExecutionComponent("read", "", nil, ToolExecutionOptions{}, nil, nil, "")
	card.UpdateArgs(json.RawMessage(`{"path":"dot.png"}`))
	card.SetResult("Read image file [image/png]", false, 0)
	user := NewUserMessageComponent("Look", nil, 1, nil)
	user.SetImages([]ImageBlock{{Data: makePNGBase64(t, 3, 2), MIMEType: "image/png"}})
	surface, session := startedSurface(t, NewContainer(card, user), nil)
	main := session.tree[frontend.RegionMain]
	if len(main) != 2 {
		t.Fatalf("main = %#v", main)
	}
	updates := func(what string, change func(), id string) {
		t.Helper()
		frames := len(session.frames)
		change()
		surface.Render()
		var ops []frontend.Op
		for _, frame := range session.frames[frames:] {
			ops = append(ops, frame.Ops...)
		}
		if len(ops) != 1 || ops[0].Kind != frontend.Update || ops[0].ID != id {
			t.Fatalf("%s: ops = %#v", what, ops)
		}
	}
	updates("tool card gains an image", func() {
		card.ImageBlocks = []ImageBlock{{Data: makePNGBase64(t, 3, 2), MIMEType: "image/png"}}
		card.Invalidate()
	}, main[0].id)
	userImage := makePNGBase64(t, 4, 4)
	updates("user message images swapped", func() {
		user.SetImages([]ImageBlock{{Data: userImage, MIMEType: "image/png"}})
	}, main[1].id)
	// Other bytes of the same length: the surface does not decode an image,
	// so flipping the last byte (the IEND CRC) changes only its Ref.
	sameLength := func(data string) string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			t.Fatal(err)
		}
		raw[len(raw)-1] ^= 0xff
		return base64.StdEncoding.EncodeToString(raw)
	}
	updates("tool card image bytes change, same length", func() {
		card.ImageBlocks = []ImageBlock{{Data: sameLength(card.ImageBlocks[0].Data), MIMEType: "image/png"}}
		card.Invalidate()
	}, main[0].id)
	updates("user message image bytes change, same length", func() {
		user.SetImages([]ImageBlock{{Data: sameLength(userImage), MIMEType: "image/png"}})
	}, main[1].id)
	assertReplayIsFresh(t, surface, session)
}

// maxWidthCells is the first image's MaxWidthCells, or -1 without one.
func maxWidthCells(images []frontend.ViewImage) int {
	if len(images) == 0 {
		return -1
	}
	return images[0].MaxWidthCells
}
