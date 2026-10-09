package tui

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderCounter draws one line and counts its renders. It invalidates itself
// when its text changes, as the package's components do.
type surfaceRenderCounter struct {
	invalidatable
	text    string
	renders *int
}

func (c *surfaceRenderCounter) Render(int) []string {
	*c.renders++
	return []string{c.text}
}

func (c *surfaceRenderCounter) setText(text string) {
	c.text = text
	c.Invalidate()
}

// valueComponent is a component whose dynamic type is not comparable, so the
// surface keys it by position.
type valueComponent struct{ lines []string }

func (v valueComponent) Render(int) []string { return v.lines }
func (valueComponent) Invalidate()           {}

// A streaming token costs the same under a long transcript as under a short
// one: the frame rebuilds only the streaming message's node, renders none of
// the settled components and does not walk the document, even when the
// token starts a new part of the message.
func TestTuiSurfaceStreamingFrameWorkDoesNotGrowWithTheTranscript(t *testing.T) {
	for _, msgs := range []int{10, 1000} {
		renders := 0
		chat := buildSessionChat(msgs)
		for i := range msgs {
			chat.Add(&surfaceRenderCounter{text: fmt.Sprint(i), renders: &renders})
		}
		tail := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		tail.SetTextDelta("first")
		chat.Add(tail)
		surface, session := startedSurface(t, chat, nil)
		surface.SetHooks(SurfaceHooks{Streaming: func() *AssistantMessageComponent { return tail }})
		surface.Render()
		walks, settled := surface.frame, renders
		for i := range 20 {
			built := surface.ver
			tail.SetTextDelta(" token")
			surface.Render()
			if surface.frame != walks {
				t.Fatalf("msgs=%d token %d walked the document", msgs, i)
			}
			if got := surface.ver - built; got != 1 {
				t.Fatalf("msgs=%d token %d built %d nodes, want 1", msgs, i, got)
			}
			if renders != settled {
				t.Fatalf("msgs=%d token %d rendered %d settled components", msgs, i, renders-settled)
			}
			last := session.frames[len(session.frames)-1]
			if len(last.Ops) != 1 || last.Ops[0].Kind != frontend.Update || last.Ops[0].Index != len(session.tree[frontend.RegionMain])-1 {
				t.Fatalf("msgs=%d token %d sent %#v", msgs, i, last.Ops)
			}
		}
		tail.SetThinkingDelta("a new part")
		surface.Render()
		last := session.frames[len(session.frames)-1]
		if surface.frame != walks || renders != settled || len(last.Ops) != 2 || last.Ops[1].Kind != frontend.Insert {
			t.Fatalf("msgs=%d: a new part walked %v, rendered %d, sent %#v", msgs, surface.frame != walks, renders-settled, last.Ops)
		}
		assertReplayIsFresh(t, surface, session)
	}
}

// Mounting a message at the end of a long transcript, or taking it out,
// walks only the message: the frame's work does not grow with the
// transcript.
func TestTuiSurfaceMessageFrameWorkDoesNotGrowWithTheTranscript(t *testing.T) {
	for _, msgs := range []int{10, 1000} {
		chat := buildSessionChat(msgs)
		surface, session := startedSurface(t, chat, nil)
		walks := surface.walks
		message, spacer := NewUserMessageComponent("One more.", nil, 1, nil), NewSpacer(1)
		for i := range 10 {
			chat.Add(message)
			chat.Add(spacer)
			surface.Render()
			last := session.frames[len(session.frames)-1]
			if surface.walks != walks || surface.visited != 2 || len(last.Ops) != 2 || last.Ops[0].Kind != frontend.Insert || last.Ops[1].Index != len(session.tree[frontend.RegionMain])-1 {
				t.Fatalf("msgs=%d mount %d: whole walk %v, visited %d, sent %#v", msgs, i, surface.walks != walks, surface.visited, last.Ops)
			}
			chat.Remove(spacer)
			chat.Remove(message)
			surface.Render()
			last = session.frames[len(session.frames)-1]
			if surface.walks != walks || surface.visited != 0 || len(last.Ops) != 2 || last.Ops[0].Kind != frontend.Remove {
				t.Fatalf("msgs=%d unmount %d: whole walk %v, visited %d, sent %#v", msgs, i, surface.walks != walks, surface.visited, last.Ops)
			}
		}
		if len(surface.ids) != len(chat.Children()) {
			t.Fatalf("msgs=%d: %d component states for %d components", msgs, len(surface.ids), len(chat.Children()))
		}
		assertReplayIsFresh(t, surface, session)
	}
}

// The windowed diff of the main region sends exactly the ops the diff of the
// whole region sends, whatever changed between the frames.
func TestDiffSurfaceMainMatchesTheWholeRegionDiff(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	ver := uint64(0)
	entry := func(id int) surfaceEntry {
		ver++
		return surfaceEntry{id: fmt.Sprintf("n%d", id), node: frontend.Lines{Lines: []string{fmt.Sprint(rng.IntN(3))}}, ver: ver}
	}
	for range 5000 {
		var prev []surfaceEntry
		for _, id := range rng.Perm(12)[:rng.IntN(12)] {
			prev = append(prev, entry(id))
		}
		// next keeps, rebuilds, drops, moves and adds entries.
		var next []surfaceEntry
		for _, e := range prev {
			switch rng.IntN(6) {
			case 0:
			case 1:
				rebuilt := entry(0)
				rebuilt.id = e.id
				next = append(next, rebuilt)
			default:
				next = append(next, e)
			}
		}
		if len(next) > 1 && rng.IntN(3) == 0 {
			i, j := rng.IntN(len(next)), rng.IntN(len(next))
			next[i], next[j] = next[j], next[i]
		}
		for range rng.IntN(3) {
			id := 100 + rng.IntN(1000)
			if !slices.ContainsFunc(next, func(e surfaceEntry) bool { return e.id == fmt.Sprintf("n%d", id) }) {
				next = slices.Insert(next, rng.IntN(len(next)+1), entry(id))
			}
		}
		want, _ := diffSurfaceRegion(nil, frontend.RegionMain, prev, next)
		if got := diffSurfaceMain(nil, prev, next); !reflect.DeepEqual(got, want) {
			t.Fatalf("prev %v next %v: windowed ops %v, whole-region ops %v", prev, next, got, want)
		}
	}
}

// Random edits of every kind keep the session's tree equal to a fresh
// render and keep each mounted component's id, and every frame sends the ops
// of the whole region's diff from the last frame's nodes to the new ones,
// as rebuilding every component would. In the tracked case frames apply
// node changes without a walk and container changes by walking only the
// changed children; the positional case adds components keyed by position,
// which every frame walks the whole document for.
func TestTuiSurfaceRandomEditsReplayToAFreshRender(t *testing.T) {
	for _, tc := range []struct {
		name                                                      string
		positional                                                bool
		structuralOneIn, steps, minUnwalked, minSpliced, maxWhole int
	}{
		{name: "tracked", structuralOneIn: 4, steps: 400, minUnwalked: 180, minSpliced: 100, maxWhole: 60},
		{name: "restructuring", structuralOneIn: 1, steps: 400, minSpliced: 100, maxWhole: 300},
		{name: "positional", positional: true, structuralOneIn: 4, steps: 400, maxWhole: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := randomSurfaceEdits(t, rand.New(rand.NewPCG(5, 6)), tc.positional, tc.structuralOneIn, tc.steps)
			t.Logf("%d frames: %d without a walk, %d spliced, %d whole walks", tc.steps, got.unwalked, got.spliced, got.whole)
			if got.unwalked < tc.minUnwalked || got.spliced < tc.minSpliced || got.whole > tc.maxWhole {
				t.Fatalf("frames %+v, want at least %d without a walk and %d spliced, at most %d whole walks", got, tc.minUnwalked, tc.minSpliced, tc.maxWhole)
			}
		})
	}
}

// frameKinds counts frames by the walk they needed.
type frameKinds struct{ unwalked, spliced, whole int }

// randomSurfaceEdits runs steps frames of random edits.
func randomSurfaceEdits(t *testing.T, rng *rand.Rand, positional bool, structuralOneIn, steps int) frameKinds {
	t.Helper()
	renders := 0
	root := NewContainer()
	var leaves []Component
	var streaming *AssistantMessageComponent
	newLeaf := func() Component {
		switch rng.IntN(8) {
		case 0:
			return NewText(fmt.Sprint("text ", rng.IntN(100)))
		case 1:
			return &surfaceRenderCounter{text: fmt.Sprint("count ", rng.IntN(100)), renders: &renders}
		case 2:
			return &inPlaceComponent{lines: []string{fmt.Sprint("in place ", rng.IntN(100))}}
		case 3:
			block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
			block.SetTextDelta(fmt.Sprint("reply ", rng.IntN(100)))
			return block
		case 4:
			return NewUserMessageComponent(fmt.Sprint("ask ", rng.IntN(100)), nil, 1, nil)
		case 5:
			card := NewToolExecutionComponent("read", "", nil, ToolExecutionOptions{}, nil, nil, "")
			card.UpdateArgs(json.RawMessage(fmt.Sprintf(`{"path":"f%d"}`, rng.IntN(100))))
			return card
		case 6:
			if positional {
				return valueComponent{lines: []string{fmt.Sprint("value ", rng.IntN(100))}}
			}
			return NewText(fmt.Sprint("text ", rng.IntN(100)))
		default:
			return NewSpacer(1)
		}
	}
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	renderOnlyWhenAsked(surface)
	surface.SetHooks(SurfaceHooks{Streaming: func() *AssistantMessageComponent { return streaming }})
	surface.SetLayout(root, nil)
	surface.Start()
	structural := func() {
		// Change a mounted container: one in the document.
		var containers []*Container
		var collect func(c *Container)
		collect = func(c *Container) {
			containers = append(containers, c)
			for _, kid := range c.Children() {
				if nested, ok := kid.(*Container); ok {
					collect(nested)
				}
			}
		}
		collect(root)
		container := containers[rng.IntN(len(containers))]
		children := container.Children()
		switch op := rng.IntN(5); {
		case op == 0:
			container.Add(NewContainer())
		case op == 1 && len(children) > 0:
			// Remove compares components, which a valueComponent does not
			// allow, so the positional case sets the children.
			i := rng.IntN(len(children))
			if positional {
				container.SetChildren(slices.Delete(children, i, i+1)...)
			} else {
				container.Remove(children[i])
			}
		case op == 2 && len(children) > 0:
			leaf := newLeaf()
			leaves = append(leaves, leaf)
			i := rng.IntN(len(children))
			if positional {
				children[i] = leaf
				container.SetChildren(children...)
			} else {
				container.Replace(children[i], leaf)
			}
		case op == 3 && positional && len(leaves) > 0:
			// A second mount of a mounted component.
			container.Add(leaves[rng.IntN(len(leaves))])
		case op == 4 && !positional && len(children) > 0:
			// An insert before a child, as a custom entry goes before the
			// streaming message.
			leaf := newLeaf()
			leaves = append(leaves, leaf)
			container.InsertBefore(children[rng.IntN(len(children))], leaf)
		default:
			leaf := newLeaf()
			leaves = append(leaves, leaf)
			container.Add(leaf)
		}
	}
	change := func() {
		switch {
		case rng.IntN(8) == 0:
			streaming = nil
			for _, leaf := range slices.Backward(leaves) {
				if block, ok := leaf.(*AssistantMessageComponent); ok && rng.IntN(2) == 0 {
					streaming = block
					break
				}
			}
		case rng.IntN(20) == 0:
			session.mainCols = 30 + rng.IntN(30)
		case len(leaves) > 0:
			switch c := leaves[rng.IntN(len(leaves))].(type) {
			case *Text:
				c.SetText(fmt.Sprint("edited ", rng.IntN(100)))
			case *surfaceRenderCounter:
				c.setText(fmt.Sprint("edited ", rng.IntN(100)))
			case *inPlaceComponent:
				c.lines[0] = fmt.Sprint("rewritten ", rng.IntN(100))
			case *AssistantMessageComponent:
				switch rng.IntN(6) {
				case 0:
					c.SetThinkingDelta(" hmm")
				case 1:
					c.SetTerminalError("aborted", "")
				case 2:
					c.SetHideThinkingBlock(rng.IntN(2) == 0)
				default:
					c.SetTextDelta(" more")
				}
			case *ToolExecutionComponent:
				c.SetResult(fmt.Sprint("out ", rng.IntN(100)), rng.IntN(2) == 0, 0)
			}
		}
	}
	var kinds frameKinds
	for step := range steps {
		prevTree := slices.Clone(session.tree[frontend.RegionMain])
		prevIDs := map[Component]string{}
		for c, state := range surface.ids {
			if state.mounted {
				prevIDs[c] = state.id
			}
		}
		for range 1 + rng.IntN(3) {
			if rng.IntN(structuralOneIn) == 0 {
				structural()
			} else {
				change()
			}
		}
		frames, walked, whole := len(session.frames), surface.frame, surface.walks
		surface.Render()
		switch {
		case surface.walks != whole:
			kinds.whole++
		case surface.frame != walked:
			kinds.spliced++
		default:
			kinds.unwalked++
		}
		want, _ := diffSurfaceRegion(nil, frontend.RegionMain, prevTree, session.tree[frontend.RegionMain])
		var got []frontend.Op
		for _, op := range opsSince(session, frames) {
			if op.Region == frontend.RegionMain {
				got = append(got, op)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: sent %#v, the whole-region diff is %#v", step, got, want)
		}
		for c, id := range prevIDs {
			if state := surface.ids[c]; state != nil && state.mounted && state.id != id {
				t.Fatalf("step %d: a mounted component's id changed from %s to %s", step, id, state.id)
			}
		}
		assertSurfaceIndex(t, surface, step)
		assertReplayIsFresh(t, surface, session)
	}
	return kinds
}

// assertSurfaceIndex checks the retained main region against the document:
// each mounted container holds its children's states and cumulative entry
// counts, and each component's entries sit at its start in the region.
func assertSurfaceIndex(t *testing.T, surface *TuiSurface, step int) {
	t.Helper()
	if surface.walkAlways {
		return
	}
	at := 0
	var check func(c Component)
	check = func(c Component) {
		if container, ok := c.(*Container); ok {
			sc := surface.containers[container]
			children := container.Children()
			if sc == nil || !sc.mounted || !slices.Equal(sc.kids, children) {
				t.Fatalf("step %d: container state %+v does not hold its children %v", step, sc, children)
			}
			base := at
			for i, kid := range children {
				check(kid)
				if sc.ends[i] != at-base {
					t.Fatalf("step %d: child %d ends at %d, recorded %d", step, i, at-base, sc.ends[i])
				}
			}
			return
		}
		state := surface.ids[c]
		if state == nil || !state.mounted {
			t.Fatalf("step %d: component %T has no mounted state", step, c)
		}
		if got := state.start(); got != at {
			t.Fatalf("step %d: component %s starts at %d, computed %d", step, state.id, at, got)
		}
		if !sameIDs(surface.main[at:at+len(state.entries)], state.entries) {
			t.Fatalf("step %d: component %s entries are not at its start", step, state.id)
		}
		at += len(state.entries)
	}
	check(surface.document)
	if at != len(surface.main) {
		t.Fatalf("step %d: the region holds %d entries, the document %d", step, len(surface.main), at)
	}
}

// A message that stops streaming without changing sends its last part as no
// longer streaming.
func TestTuiSurfaceRebuildsAMessageThatStopsStreaming(t *testing.T) {
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	block.SetTextDelta("Done.")
	streaming := block
	surface, session := startedSurface(t, NewContainer(block), nil)
	surface.SetHooks(SurfaceHooks{Streaming: func() *AssistantMessageComponent { return streaming }})
	surface.Render()
	if got := session.mainNodes()[0].(frontend.MarkdownText); !got.Streaming {
		t.Fatalf("streaming message = %#v", got)
	}
	streaming = nil
	surface.Render()
	if got := session.mainNodes()[0].(frontend.MarkdownText); got.Streaming {
		t.Fatalf("finished message = %#v", got)
	}
}

// The surface rebuilds every frame a component whose changes do not all
// invalidate it: a running shell command's spinner, and a tool result
// component it cannot receive changes from. A result component it can is
// rebuilt when it changes, without invalidating its card.
func TestTuiSurfacePollsComponentsThatChangeWithoutInvalidating(t *testing.T) {
	bash := NewBashExecutionComponent("sleep 1", nil, false, 1)
	tracked := NewText("first result")
	untracked := &inPlaceComponent{lines: []string{"first"}}
	card := func(result Component) *ToolExecutionComponent {
		c := NewToolExecutionComponent("read", "", nil, ToolExecutionOptions{}, nil, nil, "")
		c.SetDefinition(&ToolDefinitionRenderers{Result: func(ToolRenderInput) (Component, bool) { return result, true }}, nil)
		c.MarkExecutionStarted()
		c.SetResult("done", false, 0)
		return c
	}
	trackedCard, untrackedCard := card(tracked), card(untracked)
	surface, session := startedSurface(t, NewContainer(bash, trackedCard, untrackedCard), nil)
	lines := func(i int) string {
		return widthx.StripAnsi(strings.Join(session.mainNodes()[i].(frontend.Lines).Lines, "\n"))
	}
	result := func(i int) string {
		return widthx.StripAnsi(strings.Join(session.mainNodes()[i].(frontend.ToolCard).Result, "\n"))
	}
	before := lines(0)
	bash.Loader().Tick()
	surface.Render()
	if lines(0) == before {
		t.Fatalf("the spinner did not move: %q", before)
	}

	tracked.SetText("second result")
	untracked.lines[0] = "second"
	surface.Render()
	if !strings.Contains(result(1), "second result") || !strings.Contains(result(2), "second") {
		t.Fatalf("results = %q, %q", result(1), result(2))
	}

	bash.SetComplete(nil, false, nil, "")
	surface.Render()
	if surface.ids[bash].live || !surface.ids[untrackedCard].live || surface.ids[trackedCard].live {
		t.Fatalf("live: bash %v, untracked card %v, tracked card %v", surface.ids[bash].live, surface.ids[untrackedCard].live, surface.ids[trackedCard].live)
	}
	assertReplayIsFresh(t, surface, session)
}

// A stopped surface stops receiving its components' changes, and a restart
// draws the changes made meanwhile.
func TestTuiSurfaceStopReleasesTheComponents(t *testing.T) {
	text := NewText("before")
	surface, session := startedSurface(t, NewContainer(text), nil)
	if text.sink.Load() != surface.sink {
		t.Fatal("the surface does not receive the text's changes")
	}
	surface.Stop()
	text.SetText("while stopped")
	if text.sink.Load() != nil || len(surface.sink.pending) != 0 {
		t.Fatalf("a stopped surface still receives changes: %d pending", len(surface.sink.pending))
	}
	surface.Start()
	if got := widthx.StripAnsi(strings.Join(session.mainNodes()[0].(frontend.Lines).Lines, "")); !strings.Contains(got, "while stopped") {
		t.Fatalf("after restart = %q", got)
	}
}

// Every component that reports dirty without its own invalidation must be
// one the surface polls; a new IsDirty override needs a SurfaceLive method
// or must not embed invalidatable.
func TestSurfacePollsEveryIsDirtyOverride(t *testing.T) {
	known := map[string]bool{
		"tui.invalidatable":                     true, // the flag itself
		"tui.Editor":                            true, // SurfaceLive
		"tui.BashExecutionComponent":            true, // SurfaceLive
		"tui.Markdown":                          true, // SurfaceLive
		"tui.CustomMessageComponent":            true, // SurfaceLive
		"codingagent.CustomEntryComponent":      true, // SurfaceLive
		"codingagent.SessionSelectorComponent":  true, // SurfaceLive
		"codingagent.SettingsSelectorComponent": true, // SurfaceLive
		"tui.ToolExecutionComponent":            true, // the surface tracks its result component
		"tui.StaticText":                        true, // never dirty, embeds no invalidatable
		"subprocess.toolRenderProxy":            true, // embeds no invalidatable
		"subprocess.renderProxyComponent":       true, // embeds no invalidatable
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "IsDirty" {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			ident, ok := recv.(*ast.Ident)
			if !ok {
				continue
			}
			if name := file.Name.Name + "." + ident.Name; !known[name] {
				t.Errorf("%s overrides IsDirty at %s; classify it for TuiSurface (SurfaceLive) and add it here", name, fset.Position(fn.Pos()))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Component(NewEditor()).(surfacePolled); !ok {
		t.Error("Editor is not polled")
	}
	if _, ok := Component(NewBashExecutionComponent("x", nil, false, 1)).(surfacePolled); !ok {
		t.Error("BashExecutionComponent is not polled")
	}
}

// Invalidate may run on any goroutine, as the git branch watcher's does,
// while the surface draws a frame.
func TestTuiSurfaceTakesInvalidationsFromOtherGoroutines(t *testing.T) {
	renders := 0
	counters := []*surfaceRenderCounter{{text: "a", renders: &renders}, {text: "b", renders: &renders}, {text: "c", renders: &renders}}
	surface, session := startedSurface(t, NewContainer(counters[0], counters[1], counters[2]), nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 2000 {
			counters[i%len(counters)].Invalidate()
		}
	}()
	for range 200 {
		surface.Render()
	}
	<-done
	counters[1].setText("changed")
	surface.Render()
	if got := session.mainNodes()[1].(frontend.Lines).Lines; !slices.Equal(got, []string{"changed"}) {
		t.Fatalf("second component = %q", got)
	}
	assertReplayIsFresh(t, surface, session)
}
