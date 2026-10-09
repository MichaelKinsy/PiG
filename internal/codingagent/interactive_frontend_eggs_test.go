package codingagent

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// newScreenEggMode is newLogoClickMode on a frontend surface of 100x30 whose
// session is session: the header and the chat in main, the editor in the
// dock, renders and posted work on the owner loop the test runs.
func newScreenEggMode(t *testing.T, session frontend.Session) (*InteractiveMode, *tui.TuiSurface) {
	t.Helper()
	isolatePigHome(t)
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	surface := tui.NewTuiSurfaceWithSize(session, 100, 30, func(err error) { t.Errorf("apply: %v", err) })
	m := &InteractiveMode{
		opts:          InteractiveModeOptions{LoginVisible: true},
		keybindings:   km,
		extHeader:     newSpecialLinesComponent(nil),
		tuiInst:       surface,
		surface:       surface,
		editor:        tui.NewEditor(),
		isIdle:        true,
		chatContainer: tui.NewContainer(),
		uiTaskCh:      make(chan func(), 64),
	}
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
	ctx := m.backgroundCtx
	surface.SetRenderDispatcher(func(render func()) {
		select {
		case m.uiTaskCh <- render:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() {
		m.backgroundCancel()
		m.disposeLogoAnimation()
		m.disposeArminComponents()
		m.backgroundTasks.Wait()
		surface.Stop()
	})
	m.restoreBuiltInHeader()
	surface.SetLayout(tui.NewContainer(m.headerContainer(), m.chatContainer), m.editor)
	surface.Start()
	surface.Render()
	return m, surface
}

// sessionTree replays a fake session's frames into each region's nodes by id.
func sessionTree(session *fakeFrontendSession) map[frontend.Region][]frontend.Op {
	tree := map[frontend.Region][]frontend.Op{}
	for _, frame := range session.frames {
		for _, op := range frame.Ops {
			nodes := tree[op.Region]
			at := slices.IndexFunc(nodes, func(n frontend.Op) bool { return n.ID == op.ID })
			switch {
			case op.Kind == frontend.Remove:
				if at >= 0 {
					nodes = slices.Delete(nodes, at, at+1)
				}
			case at >= 0:
				nodes[at] = op
			default:
				nodes = append(nodes, op)
			}
			tree[op.Region] = nodes
		}
	}
	return tree
}

// headerClick returns the id of the main node with a Click area and the area.
func headerClick(t *testing.T, session *fakeFrontendSession) (string, frontend.Area) {
	t.Helper()
	for _, op := range sessionTree(session)[frontend.RegionMain] {
		if lines, ok := op.Node.(frontend.Lines); ok && lines.Click != nil {
			return op.ID, *lines.Click
		}
	}
	t.Fatal("no main node with a Click area")
	return "", frontend.Area{}
}

// shownOverlays returns the overlay nodes the session shows.
func shownOverlays(session *fakeFrontendSession) []frontend.Op {
	return sessionTree(session)[frontend.RegionOverlay]
}

func newScreenSession(screen frontend.Screen) *fakeScreenSession {
	return &fakeScreenSession{fakeFrontendSession: &fakeFrontendSession{}, screen: screen, ok: true}
}

// The header reports the pig head as its Click area. A click there plays the
// logo animation as a fullscreen overlay of the session's screen, starting
// from the logo's cell on the screen as the session shows it; a click on the
// overlay plays the exit, and escape skips it and removes the overlay.
func TestFrontendLogoClickPlaysTheAnimationOnTheScreen(t *testing.T) {
	session := newScreenSession(frontend.Screen{Rows: -2, MainColumn: 3})
	m, surface := newScreenEggMode(t, session)
	id, area := headerClick(t, session.fakeFrontendSession)
	if area != (frontend.Area{Column: 1, Rows: piglogin.HeadRows, Columns: piglogin.HeadCells}) {
		t.Fatalf("header click area = %#v", area)
	}
	surface.Click(frontend.Click{Node: id, Row: 0, Column: 0})
	if m.logoAnimationPlaying {
		t.Fatal("a click left of the pig played the animation")
	}
	surface.Click(frontend.Click{Node: id, Row: 0, Column: 1})
	if !m.logoAnimationPlaying {
		t.Fatal("a click on the pig did not play the animation")
	}
	runOwnerTasks(t, m, func() bool { return m.logoAnimation != nil })
	animation := m.logoAnimation
	// Main starts at column 3 of the screen, and the header's first line is main's second, after the spacer.
	if o := animation.options; o.logoColumn != 4 || o.logoRow != 1 || o.clearColumns != piglogin.HeadCells || o.clearRows != piglogin.HeadRows {
		t.Fatalf("logo at (%d, %d) %dx%d, want (4, 1)", o.logoColumn, o.logoRow, o.clearColumns, o.clearRows)
	}
	head := piglogin.HeadLines(piglogin.Default(), tui.TerminalColorModeTrueColor)
	if len(animation.options.screen) != 28 || !strings.HasPrefix(stripANSITest(animation.options.screen[1]), "    "+stripANSITest(head[0])) {
		t.Fatalf("the screen is not drawn as the session shows it: %q", animation.options.screen[:3])
	}
	runOwnerTasks(t, m, func() bool { return len(shownOverlays(session.fakeFrontendSession)) == 1 })
	op := shownOverlays(session.fakeFrontendSession)[0]
	node := op.Node.(frontend.Overlay)
	if !node.Fullscreen || node.Width != 100 || len(node.Lines) != 28 || node.Click == nil || *node.Click != (frontend.Area{Rows: 28, Columns: 100}) {
		t.Fatalf("overlay = %d lines %#v", len(node.Lines), node)
	}
	surface.Click(frontend.Click{Node: op.ID})
	if animation.exit == nil {
		t.Fatal("a click on the overlay did not start the exit")
	}
	if err := m.dispatchKey(t.Context(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	runOwnerTasks(t, m, func() bool { return !m.logoAnimationPlaying && len(shownOverlays(session.fakeFrontendSession)) == 0 })
	if surface.HasOverlay() {
		t.Fatal("the overlay stays")
	}
}

// /arminsayshi and /pigsayhi play the 3D pig on a session's screen, and
// show the inline pig head for a session without one, or under reduced
// motion, where a click on the header's pig does nothing.
func TestFrontendSlashEggsPlayOnTheScreenOrInline(t *testing.T) {
	t.Run("screen", func(t *testing.T) {
		session := newScreenSession(frontend.Screen{})
		m, _ := newScreenEggMode(t, session)
		m.handleArminSaysHi()
		if !m.logoAnimationPlaying || len(m.arminComponents) != 0 {
			t.Fatalf("playing %v, inline heads %d", m.logoAnimationPlaying, len(m.arminComponents))
		}
		runOwnerTasks(t, m, func() bool { return len(shownOverlays(session.fakeFrontendSession)) == 1 })
		if node := shownOverlays(session.fakeFrontendSession)[0].Node.(frontend.Overlay); !node.Fullscreen || len(node.Lines) != 30 {
			t.Fatalf("overlay = %#v", node)
		}
		if m.logoAnimation.model.origin {
			t.Fatal("the 3D pig starts from the header")
		}
	})
	inline := func(t *testing.T, session frontend.Session, fake *fakeFrontendSession) {
		m, surface := newScreenEggMode(t, session)
		m.handleArminSaysHi()
		if m.logoAnimationPlaying || len(m.arminComponents) != 1 {
			t.Fatalf("playing %v, inline heads %d", m.logoAnimationPlaying, len(m.arminComponents))
		}
		id, _ := headerClick(t, fake)
		surface.Click(frontend.Click{Node: id, Row: 0, Column: 1})
		if m.logoAnimationPlaying {
			t.Fatal("a click on the pig played the animation")
		}
	}
	t.Run("no screen", func(t *testing.T) {
		session := &fakeFrontendSession{}
		inline(t, session, session)
	})
	t.Run("screen not shown now", func(t *testing.T) {
		session := newScreenSession(frontend.Screen{})
		session.ok = false
		inline(t, session, session.fakeFrontendSession)
	})
	t.Run("reduced motion", func(t *testing.T) {
		session := newScreenSession(frontend.Screen{ReduceMotion: true})
		inline(t, session, session.fakeFrontendSession)
	})
}

// The clock stands still while the view is hidden and goes on from there,
// and the animation requests no frame meanwhile.
func TestEggClockStandsStillWhileHidden(t *testing.T) {
	wall := time.Unix(1000, 0)
	hidden := false
	clock := &eggClock{hidden: func() bool { return hidden }, wall: func() time.Time { return wall }}
	if got := clock.now(); !got.Equal(wall) {
		t.Fatalf("shown clock = %v, want the wall clock %v", got, wall)
	}
	renders := 0
	render := clock.unlessHidden(func() { renders++ })
	render()
	hidden = true
	wall = wall.Add(time.Second)
	paused := clock.now()
	wall = wall.Add(3 * time.Second)
	if got := clock.now(); !got.Equal(paused) {
		t.Fatalf("hidden clock moved from %v to %v", paused, got)
	}
	render()
	if renders != 1 {
		t.Fatalf("renders = %d while hidden", renders)
	}
	hidden = false
	if got := clock.now(); !got.Equal(paused) {
		t.Fatalf("shown again at %v, want %v", got, paused)
	}
	wall = wall.Add(time.Second)
	if got := clock.now(); !got.Equal(paused.Add(time.Second)) {
		t.Fatalf("clock after showing = %v, want %v", got, paused.Add(time.Second))
	}
	render()
	if renders != 2 {
		t.Fatalf("renders = %d once shown", renders)
	}
}

// Env.Click after the session closed does nothing and never blocks.
func TestFrontendClickAfterCloseDoesNothing(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	session := newScreenSession(frontend.Screen{})
	fe := &fakeFrontend{session: session.fakeFrontendSession, screen: session}
	m, _ := newFrontendProbe(t, fe, "regular")
	m.opts.LoginVisible = true
	m.restoreBuiltInHeader()
	m.tuiInst.Render()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m.backgroundCtx = ctx
	id, _ := headerClick(t, session.fakeFrontendSession)
	// While the session runs, the click plays the animation through the owner loop.
	fe.env.Click(frontend.Click{Node: id, Row: 0, Column: 1})
	runPostedTasks(t, m)
	if !m.logoAnimationPlaying {
		t.Fatal("a click on the pig did not play the animation")
	}
	m.disposeLogoAnimation()
	m.stopInteractiveTui()
	done := make(chan struct{})
	go func() {
		fe.env.Click(frontend.Click{Node: id, Row: 0, Column: 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Env.Click blocked after Close")
	}
	runPostedTasks(t, m)
	if m.logoAnimationPlaying {
		t.Fatal("a click after Close played the animation")
	}
}

// renderCounter counts its renders on the owner loop.
type renderCounter struct{ renders int }

func (c *renderCounter) Render(int) []string { c.renders++; return []string{"counter"} }
func (c *renderCounter) Invalidate()         {}

// A read the frontend session takes whole paints nothing; a key read still
// paints at once.
func TestFrontendTakenReadPaintsNothing(t *testing.T) {
	l := newFrontendSelectorLoop(t)
	counter := &renderCounter{}
	l.onOwner(t, func() {
		l.m.chatContainer.Add(counter)
		l.m.tuiInst.Render()
	})
	renders := func() int {
		var n int
		l.onOwner(t, func() { n = counter.renders })
		return n
	}
	// Let renders requested so far run.
	time.Sleep(100 * time.Millisecond)
	before := renders()
	for range 5 {
		l.key(t, tspEvent)
	}
	time.Sleep(100 * time.Millisecond)
	if got := renders(); got != before {
		t.Fatalf("frontend reads painted %d times", got-before)
	}
	if len(l.session.inputs) < 5 {
		t.Fatalf("session took %d reads", len(l.session.inputs))
	}
	l.key(t, "a")
	if got := renders(); got == before {
		t.Fatal("a key read did not paint")
	}
}
