package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
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
	m      *InteractiveMode
	editor extension.RemoteEditor
	ticket *inputTicket
	// acks holds the pump ticket of each key sent to the component and not yet acknowledged, oldest first. The
	// component acknowledges every key once (EditorInputDone); an app action, shortcut or submit the key runs may
	// release its ticket earlier.
	acks        []*inputTicket
	localAction bool
}

var (
	_ tui.EditorRemote           = (*remoteEditor)(nil)
	_ extension.RemoteEditorHost = (*remoteEditor)(nil)
	_ tui.EditorRemoteRenderer   = (*remoteEditor)(nil)
)

// setRemoteEditor installs editor, or with nil restores the host's editor
// with the component's text. It runs on the owner loop.
func (m *InteractiveMode) setRemoteEditor(editor extension.RemoteEditor) {
	if m.editor == nil {
		return
	}
	text := m.editor.Text()
	if m.remoteEditor != nil {
		m.remoteEditor.finishAllInput()
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
		if delegated, ok := editor.(extension.DelegatedRemoteEditor); ok && delegated.Delegated() {
			m.editor.SetDelegatedRemote(h)
		} else {
			m.editor.SetRemote(h)
		}
		editor.Bind(h)
	}
	m.placeActiveStatusIndicator()
	if editor != nil {
		editor.Configure(m.remoteEditorConfig())
		editor.SetText(text)
	}
	m.requestRender()
}

// placeActiveStatusIndicator puts the active status in the editor's border or the status container, whichever the installed editor opted into.
func (m *InteractiveMode) placeActiveStatusIndicator() {
	if m.activeStatusIndicator == nil {
		return
	}
	m.statusContainer.Clear()
	m.activeWorkingIndicatorEmbedded = m.setEditorWorkingStatusIndicator(m.activeStatusIndicator)
	if !m.activeWorkingIndicatorEmbedded {
		m.statusContainer.Add(m.activeStatusIndicator)
	}
}

// EditorEmbedWorkingStatusChanged applies a component's embedWorkingStatus opt-in, which Pi reads from the editor each time it shows a status; a delegated component declares it after its factory returned.
func (h *remoteEditor) EditorEmbedWorkingStatusChanged() {
	h.onLoop(func() {
		h.m.placeActiveStatusIndicator()
		h.m.reconfigureRemoteEditor()
		h.m.requestRender()
	})
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
			SpinnerColor:      indicator.SpinnerColorPrefix(),
			MessageColor:      indicator.MessageColorPrefix(),
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
	for keyID := range m.newRunner.Shortcuts(m.keybindings.effectiveBindings()) {
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

// remoteEventQueue runs an editor component's events, or a frontend
// session's editor actions, on the owner loop in arrival order. Posting never
// blocks: the bridge delivers the events from the extension connection's read
// loop, which must keep reading while the owner loop waits on that extension
// (for example for its session_shutdown handler during /reload).
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
	h.acks = append(h.acks, h.ticket)
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

// finishInput releases the pump ticket of the oldest key the component still handles, before an app action, shortcut
// or submit that may enter a modal input loop. That key's later acknowledgement then releases nothing else.
func (h *remoteEditor) finishInput() {
	for _, ticket := range h.acks {
		if ticket != nil && !ticket.settled() {
			releaseTicket(ticket)
			break
		}
	}
	releaseTicket(h.ticket)
	h.ticket = nil
}

// finishAllInput releases every outstanding key's ticket when the component is replaced or removed.
func (h *remoteEditor) finishAllInput() {
	for _, ticket := range h.acks {
		releaseTicket(ticket)
	}
	h.acks = nil
	releaseTicket(h.ticket)
	h.ticket = nil
}

func releaseTicket(ticket *inputTicket) {
	ticket.resume()
	ticket.settle()
}

// EditorInputDone acknowledges the oldest key sent to the component.
func (h *remoteEditor) EditorInputDone() {
	h.onLoop(func() {
		if len(h.acks) == 0 {
			return
		}
		ticket := h.acks[0]
		h.acks = h.acks[1:]
		releaseTicket(ticket)
		if h.ticket == ticket {
			h.ticket = nil
		}
	})
}
func (h *remoteEditor) Mouse(event tui.TuiMouseEvent) {
	h.editor.Mouse(remoteMouseEvent(event))
}

// StateChanged sends the component the editor state again. The editor
// reports the change while it renders, under the renderer's lock, so the
// state is read and sent on the owner loop afterwards.
func (h *remoteEditor) StateChanged() {
	h.onLoop(func() { h.editor.Configure(h.m.remoteEditorConfig()) })
}

// RenderRemote is tui.EditorRemoteRenderer: an editor that renders in the host's process is asked at each render; one in an extension's process has only its frames.
func (h *remoteEditor) RenderRemote(width int) ([]string, bool) {
	live, ok := h.editor.(extension.LiveRemoteEditor)
	if !ok {
		return nil, false
	}
	return live.RenderFrame(width), true
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
		switch match, matched := h.m.slashRegistry.Match(text); {
		case matched && match.Builtin != nil:
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
		key, ok := appActionKey(AppKeybinding(action.Action))
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
func appActionKey(action AppKeybinding) (keyAction, bool) {
	switch action {
	case AppInterrupt:
		return actionInterrupt, true
	case AppExit:
		return actionExit, true
	case AppClipboardPasteImage:
		return actionPasteImage, true
	case AppClear:
		return actionClearEditor, true
	case AppSuspend:
		return actionSuspend, true
	case AppThinkingCycle:
		return actionCycleThinking, true
	case AppModelCycleForward:
		return actionCycleModelForward, true
	case AppModelCycleBackward:
		return actionCycleModelBackward, true
	case AppModelSelect:
		return actionModelPicker, true
	case AppToolsExpand:
		return actionToggleTools, true
	case AppThinkingToggle:
		return actionToggleThinking, true
	case AppEditorExternal:
		return actionExternalEditor, true
	case AppMessageCopy:
		return actionMessageCopy, true
	case AppMessageFollowUp:
		return actionFollowUp, true
	case AppMessageDequeue:
		return actionDequeue, true
	case AppSessionNew:
		return actionSessionNew, true
	case AppSessionTree:
		return actionSessionTree, true
	case AppSessionFork:
		return actionSessionFork, true
	case AppSessionResume:
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

var _ extension.RemoteEditorBaseHost = (*remoteEditor)(nil)

// EditorBase runs one base-editor operation of a delegated component on the owner loop.
func (h *remoteEditor) EditorBase(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	var result json.RawMessage
	err := h.m.runOnMainAndWait(ctx, func() error {
		if !h.current() {
			return errors.New("the editor component is no longer installed")
		}
		var err error
		result, err = h.editorBase(op, args)
		return err
	})
	return result, err
}

// editorBase is a delegated component's `super` call: the host's stock editor, with its extension shortcuts and app actions, on the owner loop.
func (h *remoteEditor) editorBase(op string, args json.RawMessage) (json.RawMessage, error) {
	m := h.m
	var p struct {
		Data  string                     `json:"data"`
		Text  string                     `json:"text"`
		Width int                        `json:"width"`
		N     int                        `json:"n"`
		Event extension.RemoteMouseEvent `json:"event"`
	}
	if len(args) > 0 {
		if err := sdkjson.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("editor base %s: %w", op, err)
		}
	}
	// The editor's strings are JavaScript strings: the SDK's JSON carries a lone UTF-16 unit as a surrogate escape, which encoding/json would replace.
	reply := func(v any) (json.RawMessage, error) { return sdkjson.Marshal(v) }
	switch op {
	case "handleInput":
		m.terminalInputMu.Lock()
		listener := m.extensionShortcutListener
		m.terminalInputMu.Unlock()
		if listener != nil && listener(p.Data) {
			h.finishInput()
			return reply(struct{}{})
		}
		action := classifyKeyWithBindings(p.Data, m.keybindings)
		if action != actionInsert {
			h.finishInput()
		}
		var err error
		m.editor.AsBase(func() { err = m.handleEditorAction(m.runCtx, action, p.Data) })
		if err != nil {
			m.failInputLoop(err)
		}
		m.tuiInst.RequestRender()
		return reply(struct{}{})
	case "handleMouse":
		e := p.Event
		m.editor.AsBase(func() {
			m.editor.HandleMouse(tui.TuiMouseEvent{
				Type: tui.TuiMouseEventType(e.Type), Button: tui.TuiMouseButton(e.Button), X: e.X, Y: e.Y, ScreenX: e.ScreenX,
				ScreenY: e.ScreenY, Width: e.Width, Height: e.Height, Shift: e.Shift, Alt: e.Alt, Ctrl: e.Ctrl, ClickCount: e.ClickCount,
			})
		})
		return reply(struct{}{})
	case "render":
		var lines []string
		m.editor.AsBase(func() { lines = m.editor.Render(p.Width) })
		return reply(map[string]any{"lines": lines, "wantsKeyRelease": false})
	case "getText":
		return reply(map[string]any{"text": m.editor.Text()})
	case "getExpandedText":
		var text string
		m.editor.AsBase(func() { text = m.editor.GetExpandedText() })
		return reply(map[string]any{"text": text})
	case "getLines":
		return reply(map[string]any{"lines": m.editor.GetLines()})
	case "getCursor":
		cursor := m.editor.GetCursor()
		return reply(map[string]any{"line": cursor.Line, "col": cursor.Col})
	case "setText":
		m.editor.AsBase(func() { m.editor.SetText(p.Text) })
	case "insertTextAtCursor":
		m.editor.AsBase(func() { m.editor.InsertTextAtCursor(p.Text) })
	case "addToHistory":
		m.editor.AsBase(func() { m.editor.AddToHistory(p.Text) })
	case "setPaddingX":
		m.editor.AsBase(func() { m.editor.SetPaddingX(p.N) })
	case "setAutocompleteMaxVisible":
		m.editor.AsBase(func() { m.editor.SetAutocompleteMaxVisible(p.N) })
	case "isShowingAutocomplete":
		var open bool
		m.editor.AsBase(func() { open = m.editor.AutocompleteOpen() })
		return reply(map[string]any{"value": open})
	default:
		return nil, fmt.Errorf("editor base: unknown operation %q", op)
	}
	m.requestRender()
	return reply(struct{}{})
}
