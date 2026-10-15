package tui

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// frameRenderer is a renderer whose frame holds its tuiBase mutex while it renders components.
type frameRenderer struct {
	name string
	ui   TUI
	base *tuiBase
}

func fixedSizeRenderers(cols, rows int) []frameRenderer {
	main := NewWithOutput(io.Discard, cols, rows)
	alt := NewTuiAltScreenWithOutput(io.Discard, cols, rows, TuiAltScreenOptions{})
	return []frameRenderer{
		{name: "main screen", ui: main, base: &main.tuiBase},
		{name: "fullscreen", ui: alt, base: &alt.tuiBase},
	}
}

// returnsWithin fails t when fn has not returned within the hang budget. A deadlocked fn stays blocked; the test reports it instead of hanging.
func returnsWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("%s did not return: it re-entered the renderer lock", what)
	}
}

func editorTextRows(lines []string) int {
	n := 0
	for _, line := range lines {
		if strings.Contains(line, "line x") {
			n++
		}
	}
	return n
}

// editor.ts render reads this.tui.terminal.rows while the TUI renders it. The renderer holds its lock for the frame, so the Editor's
// terminal-rows read must not take it, and the rows must be the renderer's current size.
func TestEditorRenderUnderTheRendererLockReadsCurrentRowsWithoutBlocking(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat("line x\n", 40), "\n")
	for _, r := range fixedSizeRenderers(80, 40) {
		t.Run(r.name, func(t *testing.T) {
			editor := NewEditorWithTUI(r.ui, EditorTheme{BorderColor: func(s string) string { return s }}, EditorOptions{})
			editor.SetText(text)
			var lines []string
			r.base.mu.Lock()
			returnsWithin(t, "Editor.Render under the renderer lock", func() { lines = editor.Render(80) })
			r.base.mu.Unlock()
			// max(5, floor(rows * 0.3)) (editor.ts:536).
			if got := editorTextRows(lines); got != 12 {
				t.Fatalf("40 rows: %d text rows rendered, want 12", got)
			}
			r.base.mu.Lock()
			r.base.setFixedDimensionsLocked(80, 20)
			returnsWithin(t, "Editor.Render under the renderer lock after a resize", func() { lines = editor.Render(80) })
			r.base.mu.Unlock()
			if got := editorTextRows(lines); got != 6 {
				t.Fatalf("20 rows: %d text rows rendered, want 6", got)
			}
		})
	}
}

// The complete frame path: the renderer renders a mounted Editor that owns it.
func TestRendererFrameRendersAnEditorThatOwnsIt(t *testing.T) {
	for _, r := range fixedSizeRenderers(80, 40) {
		t.Run(r.name, func(t *testing.T) {
			editor := NewEditorWithTUI(r.ui, EditorTheme{BorderColor: func(s string) string { return s }}, EditorOptions{})
			editor.SetText("typed")
			r.ui.AddChild(editor)
			if alt, ok := r.ui.(*TuiAltScreen); ok {
				alt.altScreenActive = true
			}
			returnsWithin(t, "the frame", r.ui.Render)
			if snapshot := strings.Join(r.ui.RenderSnapshot(80), "\n"); !strings.Contains(snapshot, "typed") {
				t.Fatalf("the snapshot lacks the editor text: %q", snapshot)
			}
		})
	}
}

// requestingComponent requests a render from its Render, as Pi's Loader.invalidate (loader.ts updateDisplay) and the tool image
// transcoder registration callback do while the TUI renders them.
type requestingComponent struct {
	ui TUI

	mu       sync.Mutex
	renders  int
	force    bool
	rendered chan struct{}
}

func (c *requestingComponent) Invalidate() {}

func (c *requestingComponent) Render(int) []string {
	c.mu.Lock()
	c.renders++
	first := c.renders == 1
	c.mu.Unlock()
	if first {
		c.ui.RequestRender(c.force)
	}
	c.rendered <- struct{}{}
	return []string{"requesting"}
}

// tui.ts requestRender during a render schedules the next frame (renderRequested, then scheduleRender after the frame). Pig's frame
// holds the renderer lock, so a request made from a component's Render must neither block nor be lost.
func TestRenderRequestedDuringAFrameIsIssuedAfterIt(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, r := range fixedSizeRenderers(40, 10) {
			name := r.name
			if force {
				name += " forced"
			}
			t.Run(name, func(t *testing.T) {
				component := &requestingComponent{ui: r.ui, force: force, rendered: make(chan struct{}, 4)}
				r.ui.AddChild(component)
				if alt, ok := r.ui.(*TuiAltScreen); ok {
					alt.altScreenActive = true
				}
				returnsWithin(t, "a frame whose component requests a render", r.ui.Render)
				for want := 1; want <= 2; want++ {
					select {
					case <-component.rendered:
					case <-time.After(testbudget.Wait(t)):
						t.Fatalf("render %d did not happen: the request made during the frame was lost", want)
					}
				}
				r.ui.(interface{ Stop() }).Stop()
			})
		}
	}
}

// A request made on another goroutine while a frame is active is also issued once the frame ends.
func TestRenderRequestedConcurrentlyWithAFrameIsNotLost(t *testing.T) {
	ui := NewWithOutput(io.Discard, 40, 10)
	rendered := make(chan struct{}, 8)
	gate := make(chan struct{})
	ui.AddChild(&frameFuncComponent{render: func() []string {
		rendered <- struct{}{}
		<-gate
		return []string{"x"}
	}})
	go ui.Render()
	<-rendered
	requested := make(chan struct{})
	go func() {
		ui.RequestRender()
		close(requested)
	}()
	select {
	case <-requested:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("RequestRender blocked on the active frame")
	}
	close(gate)
	select {
	case <-rendered:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the render requested during the frame never happened")
	}
	ui.Stop()
}

type frameFuncComponent struct{ render func() []string }

func (c *frameFuncComponent) Render(int) []string { return c.render() }
func (c *frameFuncComponent) Invalidate()         {}

// A theme change invalidates every child during the frame (container.ts invalidate); Pi's Loader.invalidate requests a render.
func TestThemeChangeInvalidatingALoaderDuringAFrameDoesNotBlock(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(original) })
	SetTheme("dark")
	ui := NewWithOutput(io.Discard, 40, 10)
	loader := NewLoader(ui, nil, nil, "Working", &LoaderIndicatorOptions{Frames: []string{"*"}})
	ui.AddChild(NewContainer(loader))
	returnsWithin(t, "the first frame", ui.Render)
	SetTheme("light")
	returnsWithin(t, "the frame after a theme change", ui.Render)
	ui.Stop()
}

// alwaysRequestingComponent requests a render from every Render.
type alwaysRequestingComponent struct{ ui TUI }

func (c *alwaysRequestingComponent) Invalidate() {}
func (c *alwaysRequestingComponent) Render(int) []string {
	c.ui.RequestRender()
	return []string{"requesting"}
}

// Frames can run on several goroutines when no render dispatcher is set (throttle timers, RenderSnapshot). One frame ending must not
// clear the frame flag of a frame that has just started, or that frame's own render request takes the renderer lock it holds.
func TestConcurrentFramesWhoseComponentsRequestRendersDoNotBlock(t *testing.T) {
	for _, r := range fixedSizeRenderers(40, 10) {
		t.Run(r.name, func(t *testing.T) {
			r.ui.AddChild(&alwaysRequestingComponent{ui: r.ui})
			returnsWithin(t, "concurrent frames", func() {
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						for range 5000 {
							r.ui.RenderSnapshot(40)
						}
					})
				}
				wg.Wait()
			})
			r.ui.(interface{ Stop() }).Stop()
		})
	}
}

// tui-alt-screen.ts implicitDocument.invalidate walks the base children. The base Container guards them, not the renderer lock that a frame holds.
func TestAltScreenImplicitDocumentInvalidateDuringAFrameDoesNotBlock(t *testing.T) {
	alt := NewTuiAltScreenWithOutput(io.Discard, 40, 10, TuiAltScreenOptions{})
	invalidated := 0
	alt.AddChild(&invalidateCounter{count: &invalidated})
	alt.mu.Lock()
	returnsWithin(t, "implicitDocument.Invalidate under the renderer lock", alt.implicitDocument.Invalidate)
	alt.mu.Unlock()
	if invalidated != 1 {
		t.Fatalf("the base child was invalidated %d times, want 1", invalidated)
	}
}

type invalidateCounter struct{ count *int }

func (c *invalidateCounter) Render(int) []string { return nil }
func (c *invalidateCounter) Invalidate()         { *c.count++ }
