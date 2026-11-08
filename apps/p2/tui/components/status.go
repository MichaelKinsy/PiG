package components

// Copied from coding-agent interactive/components/{status-indicator,countdown-timer,custom-editor}.ts with the instance theme and key hints
// (section 5.4). The editor keeps pi's embedded working status in its top border and its empty-editor Ctrl+D rule; the paste-image hook and
// extension shortcuts are gone (launcher-owned, section 3).
//
// The TypeScript CustomEditor overrides Editor.renderTopBorder. Go has no virtual dispatch, so CustomEditor.Render (status_border.go) draws
// the indicator over the top row of the embedded tui.Editor, as that override does; PiG's Editor keeps no p2-only state for it.
//
// pi: apps/p2/src/tui/components/status.ts

import (
	"context"
	"fmt"
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
//
// The countdown runs on one goroutine that ends when ctx is done, when the retry time has passed, or when Dispose is called; Dispose
// returns only after it has exited, so a disposed indicator never touches ui again.
func NewRetryStatusIndicator(ctx context.Context, ui tui.TUI, rc render.RenderContext, attempt int, at int64, errText string) *StatusIndicator {
	return newRetryStatusIndicator(ctx, ui, rc, attempt, at, errText, time.Second, time.Now)
}

// newRetryStatusIndicator is NewRetryStatusIndicator with the tick period and the clock injected for tests.
func newRetryStatusIndicator(ctx context.Context, ui tui.TUI, rc render.RenderContext, attempt int, at int64, errText string, period time.Duration, now func() time.Time) *StatusIndicator {
	message := func(nowMs int64) string {
		seconds := max(0, (at-nowMs+999)/1000)
		return fmt.Sprintf("Retrying (attempt %d) in %ds: %s (%s to cancel)", attempt+1, seconds, errText, rc.KeyText("app.interrupt"))
	}
	indicator := newStatusIndicator("retry", ui,
		func(spinner string) string { return rc.Theme().Current().Fg("warning", spinner) },
		func(text string) string { return rc.Theme().Current().Fg("muted", text) }, message(now().UnixMilli()))
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// One clock read per tick: the message and the stop decision must agree.
				nowMs := now().UnixMilli()
				indicator.SetMessage(message(nowMs))
				ui.RequestRender()
				if nowMs >= at {
					return
				}
			}
		}
	}()
	indicator.dispose = func() {
		cancel()
		<-done
	}
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
	// indicator is drawn by Render in the top border; the embedded Editor.BorderColor colors the dashes around it.
	indicator *StatusIndicator
	actions   []customEditorAction
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
	theme := ctx.Theme().Editor
	return &CustomEditor{Editor: tui.NewEditorWithTUI(ui, theme, options)}
}

// SetWorkingStatusIndicator shows an indicator in the top border; nil clears it. The indicator keeps its own colors (accent and muted, or
// warning and muted for a retry): only the dashes around it take the border color.
func (e *CustomEditor) SetWorkingStatusIndicator(indicator *StatusIndicator) {
	e.indicator = indicator
	e.Editor.Invalidate()
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
