package sdk

import (
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// The app actions Pi's CustomEditor.handleInput dispatches to a handler of its own (custom-editor.ts).
const (
	editorActionInterrupt = "app.interrupt"
	editorActionExit      = "app.exit"
	editorActionPaste     = "app.clipboard.pasteImage"
	// editorHookShortcut is the hooks entry of a component that sets its own onExtensionShortcut.
	editorHookShortcut = "extensionShortcut"
)

// EditorStatus is the working, compaction, summarization or retry status the host shows while the editor embeds it (an
// [EditorStatusEmbedder]): Pi's StatusIndicator as the host snapshots it. The host advances the animation and sends a
// snapshot with each frame (D94), so Frame is the current frame of Frames.
type EditorStatus struct {
	Kind              string   `json:"kind"`
	Message           string   `json:"message"`
	Frames            []string `json:"frames"`
	Frame             int      `json:"frame"`
	SpinnerColor      string   `json:"spinnerColor"`
	MessageColor      string   `json:"messageColor"`
	IndicatorVerbatim bool     `json:"indicatorVerbatim"`
}

// editorHooks are the handlers a component sets on its CustomEditor base: the app-level handlers Pi's CustomEditor keeps as public fields.
type editorHooks struct {
	mu                  sync.Mutex
	onEscape            func() error
	onCtrlD             func() error
	onPasteImage        func() error
	onExtensionShortcut func(data string) (bool, error)
	actionHandlers      map[string]func() error
}

// SetOnEscape is CustomEditor's onEscape: the handler of the interrupt key while no autocomplete shows, in place of the host's. nil
// restores the host's.
func (e *Editor) SetOnEscape(handler func() error) {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	e.hooks.onEscape = handler
}

// SetOnCtrlD is CustomEditor's onCtrlD: the handler of the exit key on an empty editor, in place of the host's. nil restores the host's.
func (e *Editor) SetOnCtrlD(handler func() error) {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	e.hooks.onCtrlD = handler
}

// SetOnPasteImage is CustomEditor's onPasteImage: the handler of the paste-image key, in place of the host's. nil restores the host's.
func (e *Editor) SetOnPasteImage(handler func() error) {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	e.hooks.onPasteImage = handler
}

// SetOnExtensionShortcut is CustomEditor's onExtensionShortcut: it runs first for every key, and a key it reports handled goes no
// further. It replaces the host's, which runs the extensions' shortcuts. nil restores the host's.
func (e *Editor) SetOnExtensionShortcut(handler func(data string) (bool, error)) {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	e.hooks.onExtensionShortcut = handler
}

// ActionHandlers is CustomEditor's actionHandlers: the handlers of the app actions the component handles itself, by Pi's action id
// ("app.clear", "app.model.select"), replacing the host's handler for the action. The map is live: set an entry or delete one, from
// the component's methods.
func (e *Editor) ActionHandlers() map[string]func() error {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	if e.hooks.actionHandlers == nil {
		e.hooks.actionHandlers = map[string]func() error{}
	}
	return e.hooks.actionHandlers
}

// OnAction is CustomEditor's onAction: it registers handler for an app action.
func (e *Editor) OnAction(action string, handler func() error) {
	e.ActionHandlers()[action] = handler
}

// hooked lists the app actions whose handler the component set, for the host to answer instead of running. Pi's handleInput consults
// onEscape or the interrupt entry, onCtrlD or the exit entry, onPasteImage alone, and every other entry in turn.
func (e *Editor) hooked() []string {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	h := &e.hooks
	var ids []string
	if h.onEscape != nil || h.actionHandlers[editorActionInterrupt] != nil {
		ids = append(ids, editorActionInterrupt)
	}
	if h.onCtrlD != nil || h.actionHandlers[editorActionExit] != nil {
		ids = append(ids, editorActionExit)
	}
	if h.onPasteImage != nil {
		ids = append(ids, editorActionPaste)
	}
	for _, id := range slices.Sorted(maps.Keys(h.actionHandlers)) {
		if h.actionHandlers[id] != nil && id != editorActionInterrupt && id != editorActionExit && id != editorActionPaste {
			ids = append(ids, id)
		}
	}
	if h.onExtensionShortcut != nil {
		ids = append(ids, editorHookShortcut)
	}
	return ids
}

// handler returns the component's handler of an app action, as Pi's handleInput picks it.
func (e *Editor) handler(action string) func() error {
	e.hooks.mu.Lock()
	defer e.hooks.mu.Unlock()
	h := &e.hooks
	switch action {
	case editorActionInterrupt:
		if h.onEscape != nil {
			return h.onEscape
		}
	case editorActionExit:
		if h.onCtrlD != nil {
			return h.onCtrlD
		}
	case editorActionPaste:
		return h.onPasteImage
	}
	return h.actionHandlers[action]
}

// HandleInput is the default editor's handleInput, CustomEditor's: the component's onExtensionShortcut first, then the host's
// extension shortcuts, the paste-image, interrupt and exit actions and the other app actions, each through the handler the component
// set for it when it set one, then the editing.
func (e *Editor) HandleInput(data string) error {
	e.hooks.mu.Lock()
	shortcut := e.hooks.onExtensionShortcut
	e.hooks.mu.Unlock()
	if shortcut != nil {
		handled, err := shortcut(data)
		if err != nil || handled {
			return err
		}
	}
	args := map[string]any{"data": data}
	if hooks := e.hooked(); len(hooks) > 0 {
		args["hooks"] = hooks
	}
	var reply struct {
		Hook string `json:"hook"`
	}
	if err := e.field("handleInput", args, &reply); err != nil {
		return err
	}
	if reply.Hook == "" {
		return nil
	}
	if handler := e.handler(reply.Hook); handler != nil {
		return handler()
	}
	return nil
}

// GetPaddingX is the default editor's getPaddingX.
func (e *Editor) GetPaddingX() (int, error) {
	var reply struct {
		N int `json:"n"`
	}
	err := e.field("getPaddingX", nil, &reply)
	return reply.N, err
}

// GetAutocompleteMaxVisible is the default editor's getAutocompleteMaxVisible.
func (e *Editor) GetAutocompleteMaxVisible() (int, error) {
	var reply struct {
		N int `json:"n"`
	}
	err := e.field("getAutocompleteMaxVisible", nil, &reply)
	return reply.N, err
}

// BorderColor is the default editor's borderColor, called on text: the editor's border color for its thinking level or bash mode.
func (e *Editor) BorderColor(text string) (string, error) {
	var reply struct {
		Text string `json:"text"`
	}
	err := e.field("borderColor", map[string]any{"text": text}, &reply)
	return reply.Text, err
}

// DisableSubmit is the default editor's disableSubmit.
func (e *Editor) DisableSubmit() (bool, error) {
	var reply struct {
		Value bool `json:"value"`
	}
	err := e.field("getDisableSubmit", nil, &reply)
	return reply.Value, err
}

// SetDisableSubmit sets the default editor's disableSubmit.
func (e *Editor) SetDisableSubmit(disabled bool) error {
	_, err := e.call("setDisableSubmit", map[string]any{"value": disabled})
	return err
}

// Focused is the default editor's focused, which the TUI sets as the editor takes and loses the focus.
func (e *Editor) Focused() (bool, error) {
	var reply struct {
		Value bool `json:"value"`
	}
	err := e.field("getFocused", nil, &reply)
	return reply.Value, err
}

// SetFocused sets the default editor's focused.
func (e *Editor) SetFocused(focused bool) error {
	_, err := e.call("setFocused", map[string]any{"value": focused})
	return err
}

// SetAutocompleteProvider is the default editor's setAutocompleteProvider with Pi's CombinedAutocompleteProvider(commands, basePath);
// nil leaves the editor without a provider.
func (e *Editor) SetAutocompleteProvider(provider *kit.Autocomplete) error {
	_, err := e.call("setAutocompleteProvider", map[string]any{"provider": provider})
	return err
}

// OnSubmit calls the host's onSubmit callback with text, as an editor does when the user submits it.
func (e *Editor) OnSubmit(text string) error {
	_, err := e.call("onSubmit", map[string]any{"text": text})
	return err
}

// OnChange calls the host's onChange callback with text, as an editor does when its text changed.
func (e *Editor) OnChange(text string) error {
	_, err := e.call("onChange", map[string]any{"text": text})
	return err
}

// SetWorkingStatusIndicator is CustomEditor's setWorkingStatusIndicator, which the host calls when an editor that embeds the status
// ([EditorStatusEmbedder]) gets a new status, a new frame of it, or none (nil). The host draws the status in the rows [Editor.Render]
// returns; override this to read it.
func (e *Editor) SetWorkingStatusIndicator(*EditorStatus) error { return nil }

// Invalidate is the editor's invalidate, which the host calls when the theme changes. Pi's Editor has no cached state to drop; override
// this to drop the component's.
func (e *Editor) Invalidate() error { return nil }

// editorStatusSetter is an [EditorComponent] that takes the status the host shows in the editor's border.
type editorStatusSetter interface {
	SetWorkingStatusIndicator(status *EditorStatus) error
}

// editorInvalidator is an [EditorComponent] that drops its cached rows when the theme changes.
type editorInvalidator interface {
	Invalidate() error
}

var (
	_ editorStatusSetter = (*Editor)(nil)
	_ editorInvalidator  = (*Editor)(nil)
)

func (s *editorSession) setWorkingStatus(status *EditorStatus) error {
	if setter, ok := s.component.(editorStatusSetter); ok {
		return setter.SetWorkingStatusIndicator(status)
	}
	return nil
}

func (s *editorSession) invalidate() error {
	if invalidator, ok := s.component.(editorInvalidator); ok {
		return invalidator.Invalidate()
	}
	return nil
}

// editorConfigured applies the host's configuration to the component: a changed working status goes to its setWorkingStatusIndicator.
func (s *editorSession) editorConfigured(status *EditorStatus) {
	if s.statusSent && statusEqual(s.lastStatus, status) {
		return
	}
	if !s.statusSent && status == nil {
		return
	}
	s.statusSent, s.lastStatus = true, status
	s.fail("setWorkingStatusIndicator", s.setWorkingStatus(status))
}

func statusEqual(a, b *EditorStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Message == b.Message && a.Frame == b.Frame && a.SpinnerColor == b.SpinnerColor &&
		a.MessageColor == b.MessageColor && a.IndicatorVerbatim == b.IndicatorVerbatim && slices.Equal(a.Frames, b.Frames)
}
