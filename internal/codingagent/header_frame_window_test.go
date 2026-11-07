package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// headerFrameProbe is the interactive component tree with the built-in header mounted, and a transcript of children
// children of lines lines each.
func headerFrameProbe(t testing.TB, mode string, children, lines int) (*InteractiveMode, *terminalRecorder) {
	t.Helper()
	m, term := newTickRenderProbe(t, mode)
	m.opts.LoginVisible = true
	m.setBuiltInHeader(false)
	row := strings.Repeat("transcript row ", 4)
	for range children {
		rows := make([]string, lines)
		for i := range rows {
			rows[i] = row
		}
		m.chatContainer.Add(tui.NewStaticText(rows))
	}
	m.tuiInst.Render()
	term.take()
	return m, term
}

// A header or footer an extension draws from a render function, or from a view, stays dirty: a frame draws what the
// component draws now, with no push to say it changed (Pi's header and footer components draw every frame).
func TestExtensionHeaderAndFooterRendersReachTheNextFrame(t *testing.T) {
	m, term := headerFrameProbe(t, "regular", 8, 3)
	ui := &ExtUIContext{m: m}
	header, footer := &mutableComponent{text: "header v1"}, &mutableComponent{text: "footer v1"}
	ui.SetHeader(func(tui.TUI, *tui.Theme) extension.DisposableComponent { return header })
	ui.SetFooter(func(tui.TUI, *tui.Theme, extension.ReadonlyFooterDataProvider) extension.DisposableComponent {
		return footer
	})
	for round := 1; round <= 3; round++ {
		header.text, footer.text = fmt.Sprintf("header v%d", round), fmt.Sprintf("footer v%d", round)
		m.tuiInst.Render()
		frame := widthx.StripAnsi(term.take())
		for _, want := range []string{header.text, footer.text} {
			if !strings.Contains(frame, want) {
				t.Fatalf("round %d: the frame after the component changed lacks %q: %q", round, want, frame)
			}
		}
	}
	if !m.extHeader.IsDirty() || !m.extFooter.IsDirty() {
		t.Fatal("a header or footer drawn by an extension's render function is clean between frames")
	}
}

// An extension's view draws what its state is now, so it renders on every frame; its lines pushed in a frame reach the next
// frame too, and a built-in header restored over it is drawn again.
func TestExtensionHeaderViewAndLinesReachTheNextFrame(t *testing.T) {
	m, term := headerFrameProbe(t, "regular", 8, 3)
	view := &mutableView{text: "view v1"}
	m.extHeader.SetView(view, 0)
	for round := 1; round <= 3; round++ {
		view.text = fmt.Sprintf("view v%d", round)
		m.tuiInst.Render()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, view.text) {
			t.Fatalf("round %d: the frame after the view changed lacks %q: %q", round, view.text, frame)
		}
	}
	if !m.extHeader.IsDirty() {
		t.Fatal("a header drawn by a view is clean between frames")
	}
	for round := 1; round <= 3; round++ {
		line := fmt.Sprintf("pushed header v%d", round)
		m.extHeader.SetLinesAt([]string{line}, 0)
		m.tuiInst.Render()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, line) {
			t.Fatalf("round %d: the frame after lines were pushed lacks %q: %q", round, line, frame)
		}
	}
	m.tuiInst.Render()
	if m.extHeader.IsDirty() {
		t.Fatal("pushed lines stay dirty after a frame drew them")
	}
	m.restoreBuiltInHeader()
	m.tuiInst.Render()
	if frame := widthx.StripAnsi(term.take()); strings.Contains(frame, "pushed header") || !strings.Contains(frame, "v") {
		t.Fatalf("restoring the built-in header left the pushed lines or drew nothing: %q", frame)
	}
}

// The built-in header is clean between frames until an input of its lines changes, so the frame window stays unbroken, and
// each change of an input is drawn by the next frame.
func TestBuiltInHeaderIsCleanUntilItsInputsChange(t *testing.T) {
	m, term := headerFrameProbe(t, "regular", 8, 3)
	m.tuiInst.Render()
	if m.extHeader.IsDirty() {
		t.Fatal("the built-in header is dirty in a frame that changed nothing")
	}
	collapsed := widthx.StripAnsi(term.take())
	m.toolMu.Lock()
	m.builtInHeaderExpanded = true
	m.toolMu.Unlock()
	if !m.extHeader.IsDirty() {
		t.Fatal("the built-in header is clean after the startup help was expanded")
	}
	m.tuiInst.Render()
	expanded := widthx.StripAnsi(term.take())
	if expanded == collapsed {
		t.Fatalf("expanding the startup help drew nothing: %q", expanded)
	}
	if m.extHeader.IsDirty() {
		t.Fatal("the built-in header is dirty after a frame drew its expansion")
	}
}

// mutableComponent is a header or footer component whose rows change without the host being told.
type mutableComponent struct{ text string }

func (c *mutableComponent) Render(int) []string { return []string{c.text} }
func (*mutableComponent) Invalidate()           {}
func (*mutableComponent) Dispose()              {}

// mutableView is a ViewSurface whose rows change without the host being told.
type mutableView struct{ text string }

func (v *mutableView) Render(int) []string           { return []string{v.text} }
func (*mutableView) Invalidate()                     {}
func (*mutableView) HandleViewInput(string) bool     { return false }
func (*mutableView) FrontendView(int) *frontend.View { return nil }
