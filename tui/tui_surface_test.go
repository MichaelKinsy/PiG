package tui

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type recordingSession struct {
	frames []frontend.Frame
	tree   map[frontend.Region][]surfaceEntry
	// mainCols and dockCols are what Columns reports; inputs that start with
	// "cols" are taken and set mainCols to the rest of the sequence.
	mainCols, dockCols int
}

func newRecordingSession() *recordingSession {
	return &recordingSession{tree: map[frontend.Region][]surfaceEntry{}}
}

// Apply replays ops as a frontend must, so tests check the retained tree the
// frontend ends with, not only the op list.
func (s *recordingSession) Apply(frame frontend.Frame) error {
	s.frames = append(s.frames, frame)
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
		_, _ = fmt.Sscan(cols, &s.mainCols)
	}
	return ok
}
func (*recordingSession) Close() error                { return nil }
func (*recordingSession) InputReady()                 {}
func (s *recordingSession) Columns() (main, dock int) { return s.mainCols, s.dockCols }

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
	surface.SetLayout(document, dock)
	surface.Start()
	return surface, session
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

// Two tool calls in flight are two retained nodes. Finishing one updates only
// its own node; the other keeps its state (the D89-only spike left a stale
// pending card here because a rendered line could not address it).
func TestTuiSurfaceParallelToolsUpdateTheirOwnNodes(t *testing.T) {
	chat := NewContainer()
	card := func(name, args string) *ToolExecutionComponent {
		c := NewToolExecutionComponent(name, "")
		c.UpdateArgs(name, args)
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
	card := NewToolExecutionComponent("bash", "sleep 1")
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

// The frontend lays main out narrower than the terminal (Tern's chat column),
// so PiG renders main's lines at the session's width and repaints when an
// input sequence changes it. Wider than the terminal is capped.
func TestTuiSurfaceRendersRegionsAtSessionWidths(t *testing.T) {
	text := strings.Repeat("word ", 20)
	session := newRecordingSession()
	session.mainCols, session.dockCols = 30, 0
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
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
	card := NewToolExecutionComponent("read", "")
	card.UpdateArgs("read", `{"path":"notes.txt"}`)
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
