package components

// Copied from coding-agent interactive/components/{status-indicator,countdown-timer,custom-editor}.ts with the instance theme and key hints
// (section 5.4). The editor keeps pi's embedded working status in its top border and its empty-editor Ctrl+D rule; the paste-image hook and
// extension shortcuts are gone (launcher-owned, section 3).
//
// The TypeScript CustomEditor overrides Editor.renderTopBorder. Go has no virtual dispatch, so PiG's tui.Editor owns that override: with
// EmbedWorkingStatus set it draws the indicator set by SetWorkingStatusIndicator in the top border (tui/editor_status.go renderStatusBorder).
//
// pi: apps/p2/src/tui/components/status.ts

import (
	"fmt"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
)

// StatusIndicator is the shared loader of the working, retry and compaction statuses; Kind and RenderInBorder are PiG's tui.StatusIndicator.
// Dispose is the TypeScript's `dispose`.
type StatusIndicator struct {
	*tui.StatusIndicator
	// dispose is what the subclass adds to stopping the loader.
	dispose func()
}

func newStatusIndicator(kind string, ui tui.TUI, spinnerColor, messageColor func(string) string, message string) *StatusIndicator {
	inner := &tui.StatusIndicator{Kind: kind, Loader: tui.NewLoader(ui, spinnerColor, messageColor, message, nil)}
	// pi-tui's Loader constructor starts the animation.
	inner.Start()
	return &StatusIndicator{StatusIndicator: inner}
}

// Dispose stops the animation and any countdown timer.
func (s *StatusIndicator) Dispose() {
	if s.dispose != nil {
		s.dispose()
	}
	s.Stop()
}

// NewWorkingStatusIndicator is WorkingStatusIndicator; colorFn (nil for the theme's accent spinner and muted message) colors both.
func NewWorkingStatusIndicator(ui tui.TUI, ctx render.RenderContext, message string, colorFn func(string) string) *StatusIndicator {
	spinner, text := colorFn, colorFn
	if colorFn == nil {
		spinner = func(text string) string { return ctx.Theme().Current().Fg("accent", text) }
		text = func(text string) string { return ctx.Theme().Current().Fg("muted", text) }
	}
	return newStatusIndicator("working", ui, spinner, text, message)
}

// NewRetryStatusIndicator counts down to a server-scheduled retry (`live.generation.retry.at`, milliseconds since the epoch); Esc stops the run.
func NewRetryStatusIndicator(ui tui.TUI, ctx render.RenderContext, attempt int, at int64, errText string) *StatusIndicator {
	message := func() string {
		seconds := (at - time.Now().UnixMilli() + 999) / 1000
		if seconds < 0 {
			seconds = 0
		}
		return fmt.Sprintf("Retrying (attempt %d) in %ds: %s (%s to cancel)", attempt+1, seconds, errText, ctx.KeyText("app.interrupt"))
	}
	indicator := newStatusIndicator("retry", ui,
		func(spinner string) string { return ctx.Theme().Current().Fg("warning", spinner) },
		func(text string) string { return ctx.Theme().Current().Fg("muted", text) }, message())
	var mu sync.Mutex
	stopped := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopped) }) }
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ticker.C:
				mu.Lock()
				indicator.SetMessage(message())
				mu.Unlock()
				ui.RequestRender()
				if time.Now().UnixMilli() >= at {
					return
				}
			}
		}
	}()
	indicator.dispose = stop
	return indicator
}

// NewCompactionStatusIndicator is CompactionStatusIndicator; trigger is "manual", "threshold" or "overflow", retry the retry attempt or nil.
func NewCompactionStatusIndicator(ui tui.TUI, ctx render.RenderContext, trigger string, retry *int) *StatusIndicator {
	// pi: Escape cancels a compaction (Chat.abortCompaction).
	cancel := ""
	if key := ctx.KeyText("app.interrupt"); key != "" {
		cancel = " (" + key + " to cancel)"
	}
	var label string
	switch {
	case retry != nil:
		label = fmt.Sprintf("Retrying %s compaction (attempt %d)...%s", trigger, *retry+1, cancel)
	case trigger == "manual":
		label = "Compacting context..." + cancel
	default:
		prefix := ""
		if trigger == "overflow" {
			prefix = "Context overflow detected, "
		}
		label = prefix + "Auto-compacting..." + cancel
	}
	return newStatusIndicator("compaction", ui,
		func(spinner string) string { return ctx.Theme().Current().Fg("accent", spinner) },
		func(text string) string { return ctx.Theme().Current().Fg("muted", text) }, label)
}

// CustomEditor is pi's CustomEditor: app actions by binding id, Escape and the empty-editor exit rule, embedded working status.
type CustomEditor struct {
	*tui.Editor
	actions []customEditorAction
	// OnEscape and OnCtrlD replace the handlers registered for app.interrupt and app.exit while set.
	OnEscape func()
	OnCtrlD  func()
}

type customEditorAction struct {
	id      string
	handler func()
}

// NewCustomEditor builds the editor with the ref's editor theme (`super(tui, ctx.theme.editor, options)`).
func NewCustomEditor(ui tui.TUI, ctx render.RenderContext, options tui.EditorOptions) *CustomEditor {
	editor := tui.NewEditorWithTUI(ui, ctx.Theme().Editor, options)
	editor.EmbedWorkingStatus = true
	return &CustomEditor{Editor: editor}
}

// SetWorkingStatusIndicator embeds an indicator in the top border; nil clears it.
func (e *CustomEditor) SetWorkingStatusIndicator(indicator *StatusIndicator) {
	if indicator == nil {
		e.Editor.SetWorkingStatusIndicator(nil)
		return
	}
	e.Editor.SetWorkingStatusIndicator(indicator.StatusIndicator)
}

// OnAction registers the handler of an app action; registering an action again replaces its handler and keeps its place (a Map).
func (e *CustomEditor) OnAction(id string, handler func()) {
	for i := range e.actions {
		if e.actions[i].id == id {
			e.actions[i].handler = handler
			return
		}
	}
	e.actions = append(e.actions, customEditorAction{id: id, handler: handler})
}

func (e *CustomEditor) actionHandler(id string) func() {
	for _, action := range e.actions {
		if action.id == id {
			return action.handler
		}
	}
	return nil
}

// HandleInput is custom-editor.ts handleInput.
func (e *CustomEditor) HandleInput(data string) {
	keys := tui.GetKeybindings()
	// Escape/interrupt, only when autocomplete is not active.
	if keys.Matches(data, "app.interrupt") {
		if !e.AutocompleteOpen() {
			handler := e.OnEscape
			if handler == nil {
				handler = e.actionHandler("app.interrupt")
			}
			if handler != nil {
				handler()
				return
			}
		}
		e.Editor.HandleInput(data)
		return
	}
	// Exit (Ctrl+D) only when the editor is empty; otherwise it is delete-char-forward.
	if keys.Matches(data, "app.exit") && e.Text() == "" {
		handler := e.OnCtrlD
		if handler == nil {
			handler = e.actionHandler("app.exit")
		}
		if handler != nil {
			handler()
		}
		return
	}
	// Explicit history bindings take precedence over app actions while the editor is focused.
	if keys.Matches(data, "tui.editor.historyPrevious") || keys.Matches(data, "tui.editor.historyNext") {
		e.Editor.HandleInput(data)
		return
	}
	for _, action := range e.actions {
		if action.id != "app.interrupt" && action.id != "app.exit" && keys.Matches(data, action.id) {
			action.handler()
			return
		}
	}
	e.Editor.HandleInput(data)
}
