package codingagent

// Ports packages/coding-agent/src/modes/interactive/theme/theme-controller.ts

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// terminalColorQueryTimeout is how long the system theme stays grayscale before falling back to palette indices. Terminals answer the trailing DA1 request right after the color replies, so this only matters for terminals that answer neither. Replies arriving later still apply.
const terminalColorQueryTimeout = 100 * time.Millisecond

// inMemoryThemeName is the active theme name while a theme instance set through an extension is active.
const inMemoryThemeName = "<in-memory>"

// interactiveThemeState is the controller state that the owner loop mutates and other goroutines read.
// Terminal input and lifecycle live in the driver rather than the paint-only renderer.
type interactiveThemeState struct {
	currentThemeSetting atomic.Pointer[string]
	activeThemeName     atomic.Pointer[string]
	autoSyncEnabled     atomic.Bool
	output              io.Writer
	// terminalColors is the last reported colors; a query that times out keeps them instead of erasing them. Owner loop only.
	terminalColors *tui.TerminalColors
	// colorQuery is closed when the latest color query completed or timed out and its colors applied. Owner loop only.
	colorQuery chan struct{}
	// pendingColorQueries are the started color queries whose colors have not applied yet, oldest first. Owner loop only.
	pendingColorQueries []*pendingColorQuery
	// attachedTo is the renderer whose light/dark reports the theme follows, and detach unsubscribes from it. Owner loop only.
	attachedTo tui.TUI
	detach     func()
}

// presentationTheme is the theme-controller state machine shared by InteractiveMode and standalone presentations. Every method runs on the presentation owner loop.
type presentationTheme struct {
	state      *interactiveThemeState
	getSetting func() *string
	output     func() io.Writer
	renderer   func() tui.TUI
	showError  func(string)
	// changed is onChanged: it runs after every applied theme, and the caller renders.
	changed func()
	// post queues fn on the owner loop; it fails when ctx ends first.
	post func(ctx context.Context, fn func()) error
	// spawn runs a task owned by the presentation's lifetime.
	spawn func(func())
	// ctx bounds work started by the controller. It is nil before the presentation owns background work, and no color query starts then.
	ctx func() context.Context
}

// getThemeSetting is the setting the controller applies: an initial or explicit selection, otherwise the current settings. nil means no setting; an empty name is a setting.
func (theme presentationTheme) getThemeSetting() *string {
	if setting := theme.state.currentThemeSetting.Load(); setting != nil {
		return new(*setting)
	}
	return theme.getSetting()
}

// getThemeSelection is getThemeSetting, or the active theme when there is no setting.
func (theme presentationTheme) getThemeSelection() *string {
	if setting := theme.getThemeSetting(); setting != nil {
		return setting
	}
	if active := theme.state.activeThemeName.Load(); active != nil {
		return new(*active)
	}
	return nil
}

// resolveThemeName is the theme for the current setting and terminal appearance. Without a setting the system theme applies.
func (theme presentationTheme) resolveThemeName() string {
	if name, ok := tui.ResolveThemeSettingPresence(theme.getThemeSetting(), tui.GetTerminalTheme()); ok {
		return name
	}
	return tui.SystemThemeName
}

// initTheme applies the initial theme, as the theme controller's constructor does. The system theme starts in grayscale; color follows once the terminal reports its colors.
func (theme presentationTheme) initTheme() {
	theme.attach()
	name := theme.resolveThemeName()
	theme.state.activeThemeName.Store(&name)
	tui.MarkTerminalColorsPending()
	tui.SetThemeByName(name, true)
}

// applyFromSettings applies the theme setting now and queries the terminal's colors, which update the theme when they arrive. Theme pairs and the system theme follow terminal appearance changes.
func (theme presentationTheme) applyFromSettings() {
	setting := theme.getThemeSetting()
	name := theme.resolveThemeName()
	auto := false
	if setting != nil {
		_, _, auto = tui.ParseAutoThemeSetting(*setting)
	}
	theme.setAutoSync(auto || name == tui.SystemThemeName)
	// upstream 0.99.1 theme-controller.ts applyFromSettings: applyThemeName reports a load failure itself, so the result is not needed here.
	_ = theme.applyThemeName(name, setting != nil)
	theme.queryTerminalColors()
}

func (theme presentationTheme) setAutoSync(enabled bool) {
	if theme.state.autoSyncEnabled.Swap(enabled) == enabled {
		return
	}
	theme.writeThemeNotifications(enabled)
}

// writeThemeNotifications turns the terminal's light/dark reports on or off through the renderer, which also restores them across Stop and Start.
func (theme presentationTheme) writeThemeNotifications(enabled bool) {
	if ui := theme.renderer(); ui != nil {
		ui.SetTerminalColorSchemeNotifications(enabled)
		return
	}
	sequence := "\x1b[?2031l"
	if enabled {
		sequence = "\x1b[?2031h"
	}
	_, _ = io.WriteString(theme.output(), sequence)
}

// applyThemeName selects a theme, falling back to the system theme, and reports why it could not load it.
func (theme presentationTheme) applyThemeName(name string, showError bool) error {
	err := tui.SetThemeByNameChecked(name, true)
	active := name
	if err != nil {
		active = tui.SystemThemeName
	}
	theme.state.activeThemeName.Store(&active)
	theme.notifyChanged()
	if err != nil && showError {
		// The name appears verbatim, without Go quoting or escaping.
		theme.showError("Failed to load theme \"" + name + "\": " + err.Error() + "\nFell back to the system theme.")
	}
	return err
}

func (theme presentationTheme) notifyChanged() {
	if ui := theme.renderer(); ui != nil {
		ui.Invalidate()
	}
	if theme.changed != nil {
		theme.changed()
	}
}

// setThemeName applies a theme selected by name, as an extension's setTheme does.
func (theme presentationTheme) setThemeName(name string, showError bool) error {
	theme.setAutoSync(name == tui.SystemThemeName)
	err := theme.applyThemeName(name, showError)
	if err == nil {
		theme.state.currentThemeSetting.Store(&name)
	}
	return err
}

// setThemeInstance applies a theme object, as an extension's setTheme does (theme-controller.ts:138 setThemeInstance).
func (theme presentationTheme) setThemeInstance(instance *tui.Theme) {
	theme.setAutoSync(false)
	tui.SetThemeInstance(instance)
	name := tui.InMemoryThemeName
	theme.state.activeThemeName.Store(&name)
	theme.notifyChanged()
}

// previewTheme applies a setting or name without storing it.
func (theme presentationTheme) previewTheme(setting string) {
	name, ok := tui.ResolveThemeSettingPresence(&setting, tui.GetTerminalTheme())
	if !ok {
		if active := theme.state.activeThemeName.Load(); active != nil {
			name = *active
		}
	}
	if name == "" {
		return
	}
	if err := tui.SetThemeByNameChecked(name, true); err != nil {
		return
	}
	if ui := theme.renderer(); ui != nil {
		ui.Invalidate()
		ui.RequestRender()
	}
}

// queryTerminalColors queries the terminal's colors without waiting for them; waitForTerminalColors waits for this query. The colors apply on the owner loop when the query completes or times out, and again if the terminal answers after the timeout.
//
// Pi applies them in requestTerminalColors' `query.then(apply)`, a microtask: a reply that completes the query applies its colors before the terminal input that follows it is handled. consumeInput therefore applies a query the input completed before it returns. The timeout completes the query on the renderer's timer, so a task posted to the owner loop at the timeout applies it then.
func (theme presentationTheme) queryTerminalColors() {
	ui, ctx := theme.renderer(), theme.ctx()
	if ui == nil || ctx == nil {
		return
	}
	query := &pendingColorQuery{done: make(chan struct{})}
	theme.state.colorQuery = query.done
	// Late replies arrive at the input boundary, which runs on the owner loop.
	query.results = ui.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: float64(terminalColorQueryTimeout / time.Millisecond), OnLateReply: theme.applyTerminalColors})
	theme.state.pendingColorQueries = append(theme.state.pendingColorQueries, query)
	theme.spawn(func() {
		timeout := time.NewTimer(terminalColorQueryTimeout)
		defer timeout.Stop()
		select {
		case <-timeout.C:
		case <-ctx.Done():
			query.release()
			return
		}
		if err := theme.post(ctx, func() { theme.applyTimedOutColorQuery(ctx, query) }); err != nil {
			query.release()
		}
	})
	// A failed write completes the query at once.
	theme.applyCompletedColorQueries()
}

// pendingColorQuery is a started terminal color query whose colors have not applied yet.
type pendingColorQuery struct {
	// results is QueryTerminalColors' completion. Only the owner loop receives from it.
	results <-chan tui.TerminalColorsResult
	// done closes once the colors applied, or when the presentation ends first.
	done        chan struct{}
	releaseOnce sync.Once
}

func (query *pendingColorQuery) release() { query.releaseOnce.Do(func() { close(query.done) }) }

// applyCompletedColorQueries applies, oldest first, the color queries that completed. A later query never completes before an earlier one: replies answer the oldest query, and the earlier one times out first.
func (theme presentationTheme) applyCompletedColorQueries() {
	for len(theme.state.pendingColorQueries) > 0 {
		query := theme.state.pendingColorQueries[0]
		select {
		case result := <-query.results:
			theme.applyColorQueryResult(query, result)
		default:
			return
		}
	}
}

// applyTimedOutColorQuery applies a query whose timeout passed, unless a reply completed it before. The renderer's timer, due no later than the task's, delivers the timed-out result.
func (theme presentationTheme) applyTimedOutColorQuery(ctx context.Context, query *pendingColorQuery) {
	for slices.Contains(theme.state.pendingColorQueries, query) {
		select {
		case result := <-theme.state.pendingColorQueries[0].results:
			theme.applyColorQueryResult(theme.state.pendingColorQueries[0], result)
		case <-ctx.Done():
			query.release()
			return
		}
	}
}

// applyColorQueryResult applies the result of the oldest pending query. A failed query applies no colors, like a terminal that does not report them.
func (theme presentationTheme) applyColorQueryResult(query *pendingColorQuery, result tui.TerminalColorsResult) {
	theme.state.pendingColorQueries[0] = nil
	theme.state.pendingColorQueries = theme.state.pendingColorQueries[1:]
	colors := result.Colors
	if result.Err != nil {
		colors = tui.TerminalColors{}
	}
	theme.applyTerminalColors(colors)
	query.release()
}

// applyTerminalColors records reported colors: themes use the default colors for tokens set to "", the system theme is generated from all of them, and light/dark detection uses them. It re-renders only when they changed.
func (theme presentationTheme) applyTerminalColors(reported tui.TerminalColors) {
	previous := theme.state.terminalColors
	next := tui.TerminalColors{Foreground: reported.Foreground, Background: reported.Background, Palette: reported.Palette}
	if previous != nil {
		if next.Foreground == nil {
			next.Foreground = previous.Foreground
		}
		if next.Background == nil {
			next.Background = previous.Background
		}
		if next.Palette == nil {
			next.Palette = previous.Palette
		}
		// Re-rendering rebuilds every component, so skip it when nothing changed (including timeouts).
		if sameTerminalColors(*previous, next) {
			return
		}
	}
	theme.state.terminalColors = &next
	tui.SetTerminalColors(next)
	theme.reapplyForTerminal()
	if ui := theme.renderer(); ui != nil {
		ui.Invalidate()
		ui.RequestRender()
	}
}

func sameRgb(a, b *tui.RgbColor) bool { return a == b || (a != nil && b != nil && *a == *b) }

func sameTerminalColors(a, b tui.TerminalColors) bool {
	if !sameRgb(a.Foreground, b.Foreground) || !sameRgb(a.Background, b.Background) {
		return false
	}
	if len(a.Palette) != len(b.Palette) || (a.Palette == nil) != (b.Palette == nil) {
		return false
	}
	for i := range a.Palette {
		if a.Palette[i] != b.Palette[i] {
			return false
		}
	}
	return true
}

// reapplyForTerminal re-applies the setting after the terminal's colors or appearance changed: it regenerates the system theme, or switches the theme of a pair. Themes set through extensions or previews are left alone.
func (theme presentationTheme) reapplyForTerminal() {
	if active := theme.state.activeThemeName.Load(); active != nil && *active == inMemoryThemeName {
		return
	}
	name := theme.resolveThemeName()
	if active := theme.state.activeThemeName.Load(); name == tui.SystemThemeName || active == nil || name != *active {
		_ = theme.applyThemeName(name, false)
	}
}

// consumeInput consumes terminal color replies and appearance reports before extension listeners, viewport input, and focused components. Reports are consumed even without automatic selection. A reply that completes a color query applies its colors before this returns, as Pi's `.then(apply)` microtask runs before the next input.
func (theme presentationTheme) consumeInput(data string) bool {
	if ui := theme.renderer(); ui != nil && ui.ConsumeTerminalColorResponse(data) {
		theme.applyCompletedColorQueries()
		return true
	}
	if ui := theme.renderer(); ui != nil {
		return ui.ConsumeTerminalColorSchemeReport(data)
	}
	return false
}

// attach subscribes the theme to the renderer's light/dark reports, as Pi's theme controller does with onTerminalColorSchemeChange. It follows one renderer at a time and does nothing when it already follows the current one.
func (theme presentationTheme) attach() {
	ui := theme.renderer()
	if ui == nil || theme.state.attachedTo == ui {
		return
	}
	if theme.state.detach != nil {
		theme.state.detach()
	}
	theme.state.attachedTo = ui
	theme.state.detach = ui.OnTerminalColorSchemeChange(theme.applyTerminalColorSchemeChange)
}

// detachRenderer stops following the renderer's reports.
func (theme presentationTheme) detachRenderer() {
	if theme.state.detach != nil {
		theme.state.detach()
	}
	theme.state.attachedTo, theme.state.detach = nil, nil
}

// applyTerminalColorSchemeChange handles a light/dark switch report. The terminal's colors changed too, so they are queried again: they decide the appearance. The reported scheme only matters for terminals that do not report their background.
func (theme presentationTheme) applyTerminalColorSchemeChange(scheme tui.TerminalTheme) {
	if !theme.state.autoSyncEnabled.Load() {
		return
	}
	previous := tui.GetTerminalTheme()
	tui.SetTerminalColorScheme(scheme)
	if tui.GetTerminalTheme() != previous {
		theme.reapplyForTerminal()
	}
	theme.queryTerminalColors()
}

func (theme presentationTheme) dispose() { theme.setAutoSync(false) }

// InteractiveThemeControllerOptions binds the shared theme state machine to a presentation owner and settings store. Callbacks run on the UI owner.
type InteractiveThemeControllerOptions struct {
	GetSettingsManager  func() *SettingsManager
	ShowError           func(string)
	OnChanged           func()
	InitialThemeSetting *string
	Output              io.Writer
	RunOnMain           func(context.Context, func()) error
}

// InteractiveThemeController consumes terminal reports on the owner loop. Dispose cancels and joins all admitted work.
type InteractiveThemeController struct {
	core         presentationTheme
	options      InteractiveThemeControllerOptions
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed       bool
	tasks        sync.WaitGroup
	disposeOnce  sync.Once
	disposeError error
}

// NewInteractiveThemeController resolves the initial theme on the owner loop, as theme-controller.ts's constructor calls initTheme.
func NewInteractiveThemeController(ctx context.Context, ui tui.TUI, options InteractiveThemeControllerOptions) (*InteractiveThemeController, error) {
	if ui == nil || options.GetSettingsManager == nil || options.RunOnMain == nil || options.Output == nil || options.ShowError == nil || options.OnChanged == nil {
		return nil, errors.New("Theme controller requires a renderer, settings, owner executor, output and callbacks")
	}
	lifetime, cancel := context.WithCancel(ctx)
	controller := &InteractiveThemeController{options: options, ctx: lifetime, cancel: cancel}
	state := &interactiveThemeState{output: options.Output}
	controller.core = presentationTheme{
		state:      state,
		getSetting: func() *string { return options.GetSettingsManager().GetThemeSetting() },
		output:     func() io.Writer { return options.Output }, renderer: func() tui.TUI { return ui },
		showError: options.ShowError, changed: options.OnChanged,
		post: func(ctx context.Context, fn func()) error {
			return options.RunOnMain(ctx, fn)
		},
		spawn: func(task func()) {
			controller.mu.Lock()
			defer controller.mu.Unlock()
			if controller.closed {
				return
			}
			controller.tasks.Go(task)
		},
		ctx: func() context.Context { return lifetime },
	}
	if err := options.RunOnMain(ctx, func() {
		if options.InitialThemeSetting != nil {
			state.currentThemeSetting.Store(new(*options.InitialThemeSetting))
		}
		controller.core.initTheme()
	}); err != nil {
		cancel()
		return nil, err
	}
	return controller, nil
}

// ConsumeInput consumes terminal reports before viewport or focused-component input. Call it on the owner loop.
func (controller *InteractiveThemeController) ConsumeInput(data string) bool {
	return controller.core.consumeInput(data)
}

// DisableAutoSync stops terminal color-scheme notifications on the owner loop.
func (controller *InteractiveThemeController) DisableAutoSync() { controller.core.setAutoSync(false) }

// ApplyFromSettings applies the theme setting on the owner loop and starts the terminal color query without waiting for it. WaitForTerminalColors waits for the colors.
func (controller *InteractiveThemeController) ApplyFromSettings(ctx context.Context) error {
	controller.mu.Lock()
	if controller.closed {
		controller.mu.Unlock()
		return errors.New("Theme controller is disposed")
	}
	controller.tasks.Add(1)
	controller.mu.Unlock()
	defer controller.tasks.Done()
	return controller.options.RunOnMain(ctx, controller.core.applyFromSettings)
}

// Dispose runs off-loop while the owner executor is alive. It releases query waiters and joins started work before returning.
func (controller *InteractiveThemeController) Dispose() error {
	controller.disposeOnce.Do(func() {
		controller.mu.Lock()
		controller.closed = true
		controller.cancel()
		controller.mu.Unlock()
		controller.disposeError = controller.options.RunOnMain(context.Background(), func() {
			controller.core.detachRenderer()
			controller.core.dispose()
		})
		controller.tasks.Wait()
	})
	return controller.disposeError
}
