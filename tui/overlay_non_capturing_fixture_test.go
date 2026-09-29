package tui

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
)

type ncUpstreamComponent struct {
	lines   []string
	inputs  []string
	onInput func(string)
	focused bool
}

func (c *ncUpstreamComponent) Render(int) []string { return c.lines }

// SetFocused records the TUI focus flag, as the upstream FocusableOverlay's focused field does.
func (c *ncUpstreamComponent) SetFocused(focused bool) { c.focused = focused }
func (*ncUpstreamComponent) Invalidate()               {}
func (c *ncUpstreamComponent) HandleInput(data string) {
	if c.onInput != nil {
		c.onInput(data)
	} else {
		c.inputs = append(c.inputs, data)
	}
}

type ncUpstreamTerminal struct {
	out  bytes.Buffer
	grid *termsim.Grid
	ui   *TUI
}

func newNCUpstreamTerminal(width, height int) *ncUpstreamTerminal {
	return &ncUpstreamTerminal{grid: termsim.New(height, width)}
}

func (terminal *ncUpstreamTerminal) newTUI(t *testing.T) *TUI {
	t.Helper()
	terminal.ui = NewWithOutput(&terminal.out, terminal.grid.Cols, terminal.grid.Rows)
	t.Cleanup(terminal.ui.CancelPendingRender)
	return terminal.ui
}

func (terminal *ncUpstreamTerminal) flush(t *testing.T) {
	t.Helper()
	terminal.ui.ForceFullRender()
	terminal.ui.Render()
	terminal.grid.Write(terminal.out.Bytes())
	terminal.out.Reset()
}

// Input restoration belongs to the renderer, while the Go application's input loop delivers to the resulting focus target.
func (terminal *ncUpstreamTerminal) sendInput(data string) {
	terminal.ui.ActiveOverlay()
	if handler, ok := terminal.ui.FocusedComponent().(InputHandler); ok {
		handler.HandleInput(data)
	}
}

func (terminal *ncUpstreamTerminal) viewport() []string {
	return strings.Split(terminal.grid.String(), "\n")
}
func ncFirstColumn(line string) string {
	if line == "" {
		return ""
	}
	return line[:1]
}

func ncEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
func ncInputsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("inputs = %q, want %q", got, want)
	}
}

// .upstream/v0.87.1/packages/tui/test/overlay-non-capturing.test.ts:255
func testNCUpstreamMicrotaskCleanup(t *testing.T) {
	t.Helper()
	terminal := newNCUpstreamTerminal(80, 24)
	ui := terminal.newTUI(t)
	editor := &ncUpstreamComponent{lines: []string{"EDITOR"}}
	timer := &ncUpstreamComponent{lines: []string{"TIMER"}}
	controller := &ncUpstreamComponent{lines: []string{"CTRL"}}
	ui.Add(&recordingComponent{})
	ui.SetFocus(editor)
	ui.Render()
	resolved := make(chan struct{})
	var timerHandle *OverlayHandle
	done := func() {
		if timerHandle == nil {
			t.Fatal("timerHandle was not initialized")
		}
		timerHandle.Close()
		ui.hideOverlay()
		close(resolved)
	}
	timerHandle = ui.OpenOverlay(timer, OverlayOptions{nonCapturing: true})
	// The synchronous factory completes before this continuation. Calling it at the await boundary preserves the upstream Promise.resolve(controller).then(...) order without a detached goroutine.
	microtask := func() { ui.OpenOverlay(controller, OverlayOptions{}) }
	microtask()
	terminal.flush(t)
	ncEqual(t, ui.FocusedComponent() == controller, true)
	ncEqual(t, ui.FocusedComponent() == editor, false)
	done()
	<-resolved
	terminal.flush(t)
	ncEqual(t, ui.FocusedComponent() == editor, true)
	ncEqual(t, ui.FocusedComponent() == controller, false)
	ncEqual(t, ui.FocusedComponent() == timer, false)
	terminal.sendInput("x")
	terminal.flush(t)
	ncInputsEqual(t, editor.inputs, []string{"x"})
	ncInputsEqual(t, controller.inputs, nil)
}
