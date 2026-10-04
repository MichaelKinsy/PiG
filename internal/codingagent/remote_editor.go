package codingagent

import (
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts
// remoteEditor is an extension's editor component (ctx.ui.setEditorComponent)
// installed in place of the editor. Pi's setCustomEditorComponent puts the
// factory's editor in the editor container, copies the default editor's
// text, callbacks and settings to it and focuses it; the component, usually
// a CustomEditor subclass, then handles every key, and Pi's CustomEditor
// dispatches the app actions to the handlers Pi bound on the default editor.
// Here the component runs in the extension process: the tui.Editor forwards
// keys and text operations to it and shows its frames (tui/editor_remote.go),
// and remoteEditor runs the component's callbacks on the owner loop with the
// host's handlers for them.
type remoteEditor struct {
	m           *InteractiveMode
	editor      extension.RemoteEditor
	ticket      *inputTicket
	localAction bool
}

var (
	_ tui.EditorRemote           = (*remoteEditor)(nil)
	_ extension.RemoteEditorHost = (*remoteEditor)(nil)
)

// setRemoteEditor installs editor, or with nil restores the host's editor
// with the component's text. It runs on the owner loop.
func (m *InteractiveMode) setRemoteEditor(editor extension.RemoteEditor) {
	if m.editor == nil {
		return
	}
	text := m.editor.Text()
	if m.remoteEditor != nil {
		m.remoteEditor.finishInput()
	}
	if editor == nil {
		m.remoteEditor = nil
		m.editor.SetRemote(nil)
		if len(m.autocompleteFactories) == 0 {
			m.editor.SetAutocompleteChanged(nil)
			m.autocompleteProvider = nil
		}
	} else {
		h := &remoteEditor{m: m, editor: editor}
		m.remoteEditor = h
		m.editor.SetAutocompleteChanged(func(base tui.AutocompleteProvider) { m.rebuildAutocompleteWrappers(base, nil) })
		m.editor.SetRemote(h)
		editor.Bind(h)
	}
	if m.activeStatusIndicator != nil {
		m.statusContainer.Clear()
		m.activeWorkingIndicatorEmbedded = m.setEditorWorkingStatusIndicator(m.activeStatusIndicator)
		if !m.activeWorkingIndicatorEmbedded {
			m.statusContainer.Add(m.activeStatusIndicator)
		}
	}
	if editor != nil {
		editor.Configure(m.remoteEditorConfig())
		editor.SetText(text)
	}
	m.requestRender()
}

// remoteEditorConfig is the host editor state the component mirrors.
func (m *InteractiveMode) remoteEditorConfig() extension.RemoteEditorConfig {
	config := extension.RemoteEditorConfig{
		PaddingX:               m.editor.PaddingX(),
		AutocompleteMaxVisible: m.editor.AutocompleteMaxVisible(),
		Focused:                m.editor.Focused,
		ThinkingLevel:          m.editor.ThinkingLevel,
		Shortcuts:              m.extensionShortcutKeys(),
		Streaming:              m.runStreaming(),
		Compacting:             m.isCompacting,
		BashRunning:            m.bashCancel != nil,
		InterruptHandled:       m.isCompacting || m.branchSummaryCancel != nil || m.retryCountdownStop != nil,
	}
	if cursor, ok := m.tuiInst.(interface{ GetShowHardwareCursor() bool }); ok {
		config.ShowHardwareCursor = cursor.GetShowHardwareCursor()
	}
	if indicator := m.activeStatusIndicator; indicator != nil && m.remoteEditor.editor.EmbedWorkingStatus() {
		config.WorkingStatus = &extension.RemoteEditorStatus{
			Kind:              indicator.Kind,
			Message:           indicator.Message,
			Frames:            append([]string{}, indicator.Frames...),
			Frame:             indicator.Frame,
			SpinnerColor:      indicator.SpinnerColor,
			MessageColor:      indicator.MessageColor,
			IndicatorVerbatim: indicator.IndicatorVerbatim,
		}
	}
	return config
}

// reconfigureRemoteEditor sends updated host state to the installed editor.
func (m *InteractiveMode) reconfigureRemoteEditor() {
	if m.remoteEditor != nil {
		m.remoteEditor.editor.Configure(m.remoteEditorConfig())
	}
}

// extensionShortcutKeys lists the key ids of the extension shortcuts, which
// Pi's onExtensionShortcut matches inside the editor.
func (m *InteractiveMode) extensionShortcutKeys() []string {
	if m.newRunner == nil {
		return []string{}
	}
	keys := []string{}
	for keyID := range m.newRunner.Shortcuts(m.keybindings.ResolvedBindings()) {
		keys = append(keys, keyID)
	}
	slices.Sort(keys)
	return keys
}

// current reports whether h is still the installed component.
func (h *remoteEditor) current() bool {
	return h.m.remoteEditor == h
}

// onLoop runs fn on the owner loop while h is installed, in the order the
// component's events arrived.
func (h *remoteEditor) onLoop(fn func()) {
	h.m.remoteEditorEvents.post(h.m, func() {
		if h.current() {
			fn()
		}
	})
}

// remoteEventQueue runs an editor component's events on the owner loop in
// arrival order. Posting never blocks: the bridge delivers the events from the
// extension connection's read loop, which must keep reading while the owner
// loop waits on that extension (for example for its session_shutdown
// handler during /reload).
type remoteEventQueue struct {
	mu       sync.Mutex
	events   []func()
	draining bool
}

// post queues fn and reports whether the owner loop is running to take it.
func (q *remoteEventQueue) post(m *InteractiveMode, fn func()) bool {
	if m.tuiStopped.Load() {
		return false
	}
	q.mu.Lock()
	q.events = append(q.events, fn)
	start := !q.draining
	q.draining = true
	q.mu.Unlock()
	if start {
		go func() {
			if !m.runOnMainOK(q.drain) {
				q.mu.Lock()
				q.events = nil
				q.draining = false
				q.mu.Unlock()
			}
		}()
	}
	return true
}

// drain runs the queued events, including those queued while it runs.
func (q *remoteEventQueue) drain() {
	for {
		q.mu.Lock()
		events := q.events
		q.events = nil
		if len(events) == 0 {
			q.draining = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		for _, event := range events {
			event()
		}
	}
}

// tui.EditorRemote: the host's editor operations, sent to the component.

func (h *remoteEditor) Input(data string) {
	h.ticket.await()
	h.editor.Configure(h.m.remoteEditorConfig())
	h.editor.Input(data)
}
func (h *remoteEditor) SetText(text string) {
	if !h.localAction {
		h.editor.SetText(text)
	}
}
func (h *remoteEditor) InsertTextAtCursor(text string) { h.editor.InsertTextAtCursor(text) }
func (h *remoteEditor) AddToHistory(text string) {
	if !h.localAction {
		h.editor.AddToHistory(text)
	}
}

func (h *remoteEditor) finishInput() {
	h.ticket.resume()
	h.ticket.settle()
	h.ticket = nil
}

func (h *remoteEditor) EditorInputDone() {
	h.onLoop(h.finishInput)
}
func (h *remoteEditor) Mouse(event tui.TuiMouseEvent) {
	h.editor.Mouse(extension.RemoteEditorMouseEvent{
		Type: string(event.Type), Button: string(event.Button), X: event.X, Y: event.Y, ScreenX: event.ScreenX,
		ScreenY: event.ScreenY, Width: event.Width, Height: event.Height, Shift: event.Shift, Alt: event.Alt,
		Ctrl: event.Ctrl, ClickCount: event.ClickCount,
	})
}

// StateChanged sends the component the editor state again. The editor
// reports the change while it renders, under the renderer's lock, so the
// state is read and sent on the owner loop afterwards.
func (h *remoteEditor) StateChanged() {
	h.onLoop(func() { h.editor.Configure(h.m.remoteEditorConfig()) })
}

// extension.RemoteEditorHost: the component's output.

func (h *remoteEditor) EditorFrame(lines []string, width int, wantsKeyRelease bool) {
	h.onLoop(func() {
		// A frame answers a key or a host operation; paint it now, as the
		// input loop paints after a key.
		h.m.editor.SetRemoteFrame(lines, width, wantsKeyRelease)
		h.m.tuiInst.Render()
	})
}

func (h *remoteEditor) EditorChanged(text, expanded string) {
	h.onLoop(func() { h.m.editor.ApplyRemoteChange(text, expanded) })
}

// EditorSubmit is Pi's defaultEditor.onSubmit, which the component calls
// with the text to submit: from submitValue, after it expanded, trimmed and
// cleared its own text, or from the extension directly (pi-vim's ex line
// sets the editor text and calls onSubmit). Pi's builtin command chain clears
// the editor; a bash command that cannot start keeps it; anything else is
// added to the editor's history. done settles the promise onSubmit returns.
func (h *remoteEditor) EditorSubmit(text string, done func()) {
	if !h.m.remoteEditorEvents.post(h.m, func() {
		defer done()
		h.finishInput()
		if !h.current() {
			return
		}
		text := widthx.JSTrim(text)
		if text == "" {
			return
		}
		switch name, _ := parseSlashLine(text); {
		case strings.HasPrefix(text, "/") && h.m.slashRegistry.IsBuiltin(name):
			h.m.editor.Clear()
		case strings.HasPrefix(text, "!") && h.m.bashCancel != nil:
		default:
			if h.m.editor.OnSubmit == nil {
				h.m.editor.AddToHistory(text)
			}
		}
		if h.m.editor.OnSubmit != nil {
			h.m.editor.OnSubmit(text)
		} else {
			h.m.handleSubmit(h.m.runCtx, text)
		}
		h.m.tuiInst.RequestRender()
	}) {
		done()
	}
}

// EditorAction runs the default editor's handler for an app action, which
// Pi's CustomEditor calls: onEscape, onCtrlD, onPasteImage and the
// actionHandlers Pi copies from the default editor.
func (h *remoteEditor) EditorAction(action extension.RemoteEditorAction) {
	h.onLoop(func() {
		// A host handler may enter a modal input loop. Release the pump before that handoff; subsequent main-screen input cannot run until this owner-loop callback returns.
		h.finishInput()
		h.m.editor.ApplyRemoteChange(action.Text, action.Expanded)
		h.localAction = action.Local
		defer func() { h.localAction = false }()
		key, ok := appActionKey(action.Action)
		if !ok {
			return
		}
		if err := h.m.handleEditorAction(h.m.runCtx, key, ""); err != nil {
			h.m.failInputLoop(err)
		}

		h.m.tuiInst.RequestRender()
	})
}

// EditorShortcut is Pi's defaultEditor.onExtensionShortcut for a key the
// component matched against the shortcut keys.
func (h *remoteEditor) EditorShortcut(data string) {
	h.onLoop(func() {
		h.finishInput()
		h.m.terminalInputMu.Lock()
		listener := h.m.extensionShortcutListener
		h.m.terminalInputMu.Unlock()
		if listener != nil {
			listener(data)
		}
	})
}

// TerminalWrite is the component's tui.terminal.write. While the TUI runs
// it writes between frames on the owner loop; once the TUI stopped (Pi emits
// session_shutdown after stopping it) it writes straight to the terminal.
func (h *remoteEditor) TerminalWrite(data string) {
	if !h.m.remoteEditorEvents.post(h.m, func() { h.m.writeTerminal(data) }) {
		_, _ = io.WriteString(os.Stdout, data)
	}
}

// SetShowHardwareCursor is the component's tui.setShowHardwareCursor.
func (h *remoteEditor) SetShowHardwareCursor(enabled bool) {
	h.onLoop(func() {
		if cursor, ok := h.m.tuiInst.(interface{ SetShowHardwareCursor(bool) }); ok {
			cursor.SetShowHardwareCursor(enabled)
		}
	})
}

// EditorClosed restores the host's editor when the extension's process
// ended with its component installed.
func (h *remoteEditor) EditorClosed() {
	h.onLoop(func() { h.m.setRemoteEditor(nil) })
}

// writeTerminal writes raw output to the terminal between frames.
func (m *InteractiveMode) writeTerminal(data string) {
	if m.tuiStopped.Load() {
		_, _ = io.WriteString(os.Stdout, data)
		return
	}
	if writer, ok := m.tuiInst.(interface{ WriteRaw(string) }); ok {
		writer.WriteRaw(data)
		return
	}
	_, _ = io.WriteString(os.Stdout, data)
}

// runOnMainOK runs fn on the owner loop and reports whether the loop took it.
func (m *InteractiveMode) runOnMainOK(fn func()) bool {
	ctx := m.backgroundCtx
	if ctx == nil {
		return m.postUITask(fn)
	}
	return m.postToMain(ctx, fn) == nil
}

// appActionKey maps a Pi app action id to the key action whose handler Pi
// binds on the default editor for it.
func appActionKey(action string) (keyAction, bool) {
	switch action {
	case "app.interrupt":
		return actionInterrupt, true
	case "app.exit":
		return actionExit, true
	case "app.clipboard.pasteImage":
		return actionPasteImage, true
	case "app.clear":
		return actionClearEditor, true
	case "app.suspend":
		return actionSuspend, true
	case "app.thinking.cycle":
		return actionCycleThinking, true
	case "app.model.cycleForward":
		return actionCycleModelForward, true
	case "app.model.cycleBackward":
		return actionCycleModelBackward, true
	case "app.model.select":
		return actionModelPicker, true
	case "app.tools.expand":
		return actionToggleTools, true
	case "app.thinking.toggle":
		return actionToggleThinking, true
	case "app.editor.external":
		return actionExternalEditor, true
	case "app.message.copy":
		return actionMessageCopy, true
	case "app.message.followUp":
		return actionFollowUp, true
	case "app.message.dequeue":
		return actionDequeue, true
	case "app.session.new":
		return actionSessionNew, true
	case "app.session.tree":
		return actionSessionTree, true
	case "app.session.fork":
		return actionSessionFork, true
	case "app.session.resume":
		return actionSessionResume, true
	}
	return actionInsert, false
}

// publishExtensionKeybindings snapshots the keybinding table extensions
// receive with every state snapshot.
func (m *InteractiveMode) publishExtensionKeybindings() {
	table := m.keybindings.ExtensionKeybindingTable()
	m.extensionKeybindings.Store(&table)
}
