package codingagent

import (
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
		editorFocused: func() bool {
			return m.tuiInst.ActiveOverlay() == nil && m.tuiInst.GetFocusedComponent() == tui.Component(m.editor)
		},
		listen:     func(listener *tui.TuiInputListener) func() { return m.tuiInst.AddInputListener(listener) },
		interrupts: func(data string) bool { return m.keybindings.Matches(data, string(AppInterrupt)) },
		notify:     (&ExtUIContext{m: m}).Notify,
		size: func() viewSize {
			return viewSize{columns: m.tuiInst.Width(), rows: m.tuiInst.Height(), pointer: m.altScreen != nil}
		},
	}))
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

// setViewMounts sets the mounts the widget containers draw and lays the widgets out.
func (m *InteractiveMode) setViewMounts(mounts []*viewMount) {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	m.viewMounts = mounts
	m.renderWidgetsLocked()
}
