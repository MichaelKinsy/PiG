package codingagent

import (
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D614): the wiring of the terminal view client to the interactive loop. Nothing here runs unless the
// active Piglet selected the client (InteractiveOptions.TerminalViewClient).

// attachTerminalViewClient attaches the terminal to the view registry of the bound build's extension bridge. The
// attachment lives as long as the bridge is bound: a reload or a session replacement binds another build, which detaches
// this one and attaches to the next registry.
func (m *InteractiveMode) attachTerminalViewClient(bridge SubprocessUIBridge) {
	if !m.opts.TerminalViewClient {
		return
	}
	host, ok := bridge.(viewHost)
	if !ok {
		return
	}
	m.viewClient.Store(attachViewClient(m.runCtx, host, viewClientUI{
		post:     m.postUITask,
		postWait: m.runOnMain,
		layout:   m.setViewMounts,
		render:   m.requestRender,
		focus: func(mount *viewMount) {
			if mount == nil {
				m.tuiInst.SetFocus(m.editor)
				return
			}
			m.tuiInst.SetFocus(mount)
		},
		float: func(mount *viewMount, title string) viewFloat {
			handle := m.tuiInst.ShowOverlay(mount, viewDialogOverlay(title))
			if handle == nil {
				return nil
			}
			return &viewFrame{m: m, handle: handle}
		},
		waitRow: m.setViewWaitRow,
		editorFocused: func() bool {
			return m.tuiInst.ActiveOverlay() == nil && m.tuiInst.GetFocusedComponent() == tui.Component(m.editor)
		},
		editorEmpty: func() bool { return m.editor.Text() == "" },
		tabRow:      m.setViewTabRow,
		focusRow:    func(row *viewTabs) { m.tuiInst.SetFocus(row) },
		confirms:    func(data string) bool { return m.keybindings.Matches(data, string(tui.KBSelectConfirm)) },
		listen:      func(listener *tui.TuiInputListener) func() { return m.tuiInst.AddInputListener(listener) },
		interrupts:  func(data string) bool { return m.keybindings.Matches(data, string(AppInterrupt)) },
		reserved:    func(data string) string { return viewReservedAction(m.keybindings, data) },
		notify:      (&ExtUIContext{m: m}).Notify,
		size: func() viewSize {
			return viewSize{columns: m.tuiInst.Width(), rows: m.tuiInst.Height(), pointer: m.altScreen != nil}
		},
		write: func(data string) {
			if writer, ok := m.tuiInst.(interface{ WriteRaw(string) }); ok {
				writer.WriteRaw(data)
			}
		},
		placeholders: viewKittyPlaceholders(os.Getenv),
	}))
}

// viewDialogOverlay is the overlay a dialog is drawn in: the renderer's titled frame, the one the model selector has,
// centred, viewDialogFraction of the terminal's columns and of its rows.
func viewDialogOverlay(title string) tui.OverlayOptions {
	return tui.OverlayOptions{Title: title, WidthFraction: viewDialogFraction, HeightFraction: viewDialogFraction}
}

// viewFrame is a dialog's frame on the screen: the overlay Pi's ctx.ui.custom draws an extension's component in.
type viewFrame struct {
	m      *InteractiveMode
	handle *tui.OverlayHandle
}

func (f *viewFrame) Close() { f.handle.Close() }

func (f *viewFrame) Leave() { f.handle.UnfocusWith(tui.OverlayUnfocusOptions{Target: f.m.editor}) }

// viewKittyPlaceholders reports whether the environment is that of a terminal known to draw Kitty Unicode placeholders:
// kitty and Ghostty. It decides nothing by itself: the client asks it only where the TUI detected the Kitty protocol.
// A terminal that names itself otherwise (TERM_PROGRAM) is not one of them even when kitty's variables leak into it,
// and neither is a pane of Herdr.
func viewKittyPlaceholders(getenv func(string) string) bool {
	term, program := strings.ToLower(getenv("TERM")), strings.ToLower(getenv("TERM_PROGRAM"))
	switch {
	case getenv("HERDR_ENV") == "1":
		return false
	case program != "":
		return program == "kitty" || program == "ghostty"
	}
	return getenv("KITTY_WINDOW_ID") != "" || term == "xterm-kitty" || strings.Contains(term, "ghostty") || getenv("GHOSTTY_RESOURCES_DIR") != ""
}

// detachTerminalViewClient ends the terminal's attachment to the outgoing build's registry and removes its mounts.
func (m *InteractiveMode) detachTerminalViewClient() {
	if client := m.viewClient.Swap(nil); client != nil {
		client.detach()
	}
}

// viewClientResized tells the view client that the terminal's size may have changed, so it states its size again. It
// may run on any goroutine.
func (m *InteractiveMode) viewClientResized() {
	if client := m.viewClient.Load(); client != nil {
		client.wake()
	}
}

// setViewWaitRow sets the row that stands for the dialogs, directly above the editor, or none with nil, and lays the
// widgets out.
func (m *InteractiveMode) setViewWaitRow(row *viewWaiting) {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	m.viewWaitRow = row
	m.renderWidgetsLocked()
}

// setViewTabRow sets the tab row the panel region draws above its mounts, or none with nil, and lays the widgets out.
func (m *InteractiveMode) setViewTabRow(row *viewTabs) {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	m.viewTabRow = row
	m.renderWidgetsLocked()
}

// setViewMounts sets the mounts the widget containers draw and lays the widgets out.
func (m *InteractiveMode) setViewMounts(mounts []*viewMount) {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	m.viewMounts = mounts
	m.renderWidgetsLocked()
}
