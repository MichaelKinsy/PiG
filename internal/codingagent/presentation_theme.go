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

// interactiveThemeState keeps query completion and modal previews on the input loop.
// Explicit extension selections publish their setting, active name, and opt-out atomically.
// Terminal input and lifecycle live in the driver rather than the paint-only renderer.
type interactiveThemeState struct {
	currentThemeSetting atomic.Pointer[string]
	terminalTheme       tui.TerminalTheme
	activeThemeName     atomic.Pointer[string]
	autoSyncEnabled     atomic.Bool
	output              io.Writer
	queries             []*interactiveThemeQuery
}

type interactiveThemeQuery struct {
	detection *startupThemeDetection
	done      chan struct{}
	// persistTheme records a high-confidence detected default for an owner that persists off-loop.
	persistTheme *string
}

// presentationTheme is the theme-controller state machine shared by InteractiveMode and standalone presentations. Every method runs on the presentation owner loop.
type presentationTheme struct {
	state      *interactiveThemeState
	getSetting func() *string
	// persist stores a detected default synchronously; nil defers persistence to the query owner.
	persist   func(string)
	output    func() io.Writer
	renderer  func() tui.Renderer
	showError func(string)
	// changed replaces the default render request after a successful theme change.
	changed func()
}

// getThemeSelection preserves an initial or explicit selection; otherwise it reads the current settings. nil means no selection; an empty name is a selection.
func (theme presentationTheme) getThemeSelection() *string {
	if setting := theme.state.currentThemeSetting.Load(); setting != nil {
		return new(*setting)
	}
	return theme.getSetting()
}

func (theme presentationTheme) setAutoSync(enabled bool) {
	if theme.state.autoSyncEnabled.Swap(enabled) == enabled {
		return
	}
	theme.writeThemeNotifications(enabled)
}

func (theme presentationTheme) writeThemeNotifications(enabled bool) {
	sequence := "\x1b[?2031l"
	if enabled {
		sequence = "\x1b[?2031h"
	}
	_, _ = io.WriteString(theme.output(), sequence)
}

func (theme presentationTheme) applyThemeName(name string, showError bool) bool {
	found := tui.ActiveThemeRegistry().Get(name) != nil
	tui.SetThemeByName(name, true)
	activeName := tui.ActiveTheme().Name
	theme.state.activeThemeName.Store(&activeName)
	if ui := theme.renderer(); ui != nil {
		ui.Invalidate()
		if theme.changed != nil {
			theme.changed()
		} else {
			ui.RequestRender()
		}
	}
	if !found && showError {
		// The name appears verbatim, without Go quoting or escaping.
		theme.showError("Failed to load theme \"" + name + "\": Theme not found: " + name + "\nFell back to dark theme.")
	}
	return found
}

func (theme presentationTheme) applyTerminalTheme(terminalTheme tui.TerminalTheme) {
	if !theme.state.autoSyncEnabled.Load() {
		return
	}
	theme.state.terminalTheme = terminalTheme
	var light, dark string
	ok := false
	if selection := theme.getThemeSelection(); selection != nil {
		light, dark, ok = tui.ParseAutoThemeSetting(*selection)
	}
	if !ok {
		theme.setAutoSync(false)
		return
	}
	name := dark
	if terminalTheme == "light" {
		name = light
	}
	activeName := theme.state.activeThemeName.Load()
	if activeName == nil || name != *activeName {
		theme.applyThemeName(name, false)
	}
}

func (theme presentationTheme) previewTheme(setting string) {
	terminalTheme := theme.state.terminalTheme
	if terminalTheme == "" {
		terminalTheme = tui.DetectTerminalBackground(tui.TerminalThemeDetectionOptions{}).Theme
	}
	name, ok := tui.ResolveThemeSettingPresence(&setting, terminalTheme)
	if !ok {
		if active := theme.state.activeThemeName.Load(); active != nil {
			name = *active
		}
	}
	if tui.ActiveThemeRegistry().Get(name) == nil {
		return
	}
	tui.SetThemeByName(name)
	theme.renderer().Invalidate()
	theme.renderer().RequestRender()
}

// beginThemeDetection applies any non-automatic selection, including an empty or malformed name, as a fixed theme without querying the terminal.
// An automatic pair starts both queries together, and scheme reports take precedence.
// OSC 11 can settle an unset selection immediately but an auto pair waits for the scheme deadline.
func (theme presentationTheme) beginThemeDetection(output io.Writer) *interactiveThemeQuery {
	if theme.state.output == nil {
		theme.state.output = output
	}
	setting := theme.getThemeSelection()
	if setting != nil {
		if _, _, auto := tui.ParseAutoThemeSetting(*setting); !auto {
			theme.setAutoSync(false)
			theme.applyThemeName(*setting, true)
			return nil
		}
	}
	detection := newStartupThemeDetection(setting, nil, theme.renderer())
	detection.backgroundOnly = setting == nil
	if detection.backgroundOnly {
		theme.setAutoSync(false)
	}
	query := &interactiveThemeQuery{detection: detection, done: make(chan struct{})}
	theme.state.queries = append(theme.state.queries, query)
	// theme.ts starts OSC 11 even when the scheme query fails, and otherwise awaits scheme precedence.
	detection.start(func(sequence string) error { _, err := io.WriteString(output, sequence); return err })
	if detection.readBackground() {
		theme.finishThemeDetection(query)
	}
	return query
}

func (theme presentationTheme) finishThemeDetection(query *interactiveThemeQuery) {
	select {
	case <-query.done:
		return
	default:
	}
	detection := query.detection
	theme.state.terminalTheme = detection.terminalTheme()
	if !detection.backgroundOnly {
		theme.setAutoSync(true)
	}
	success := theme.applyThemeName(detection.themeName(), !detection.backgroundOnly)
	if success && detection.backgroundOnly && (detection.background != nil || tui.DetectTerminalBackground(tui.TerminalThemeDetectionOptions{}).Confidence == "high") {
		name := detection.themeName()
		if theme.persist != nil {
			theme.persist(name)
		} else {
			query.persistTheme = &name
		}
	}
	close(query.done)
	theme.pruneThemeQueries()
}

func (theme presentationTheme) pruneThemeQueries() {
	theme.state.queries = slices.DeleteFunc(theme.state.queries, func(query *interactiveThemeQuery) bool {
		return query.detection.settled
	})
}

// consumeInput precedes extension listeners, viewport input, and focused components.
// Reports are consumed even without an outstanding query or automatic selection.
func (theme presentationTheme) consumeInput(data string) bool {
	if ui := theme.renderer(); ui != nil && ui.ConsumeOsc11BackgroundResponse(data) {
		for _, query := range slices.Clone(theme.state.queries) {
			if query.detection.readBackground() {
				theme.finishThemeDetection(query)
			}
		}
		return true
	}
	if scheme := tui.ParseTerminalColorSchemeReport(data); scheme != "" {
		theme.applyTerminalTheme(scheme)
		// A report settles every outstanding scheme listener. Copy the pointers because
		// finishing a query can remove it from the pending background-reply queue.
		for _, query := range slices.Clone(theme.state.queries) {
			if _, settled := query.detection.consume(data); settled {
				theme.finishThemeDetection(query)
			}
		}
		return true
	}
	for _, query := range theme.state.queries {
		if consumed, settled := query.detection.consume(data); consumed {
			if settled {
				theme.finishThemeDetection(query)
			}
			theme.pruneThemeQueries()
			return true
		}
	}
	return false
}

func (theme presentationTheme) dispose() {
	theme.setAutoSync(false)
	for _, query := range theme.state.queries {
		select {
		case <-query.done:
		default:
			close(query.done)
		}
	}
	theme.state.queries = nil
}

// InteractiveThemeControllerOptions binds the shared theme state machine to a presentation owner and settings store. Callbacks run on the UI owner; settings persistence runs off-loop after detection completes.
type InteractiveThemeControllerOptions struct {
	GetSettingsManager  func() *SettingsManager
	ShowError           func(string)
	OnChanged           func()
	InitialThemeSetting *string
	Output              io.Writer
	RunOnMain           func(context.Context, func()) error
}

// InteractiveThemeController consumes terminal reports on the owner loop while ApplyFromSettings waits off-loop for query completion. Dispose cancels and joins all admitted applications.
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
func NewInteractiveThemeController(ctx context.Context, ui tui.Renderer, options InteractiveThemeControllerOptions) (*InteractiveThemeController, error) {
	if ui == nil || options.GetSettingsManager == nil || options.RunOnMain == nil || options.Output == nil || options.ShowError == nil || options.OnChanged == nil {
		return nil, errors.New("Theme controller requires a renderer, settings, owner executor, output and callbacks")
	}
	lifetime, cancel := context.WithCancel(ctx)
	controller := &InteractiveThemeController{options: options, ctx: lifetime, cancel: cancel}
	state := &interactiveThemeState{output: options.Output}
	controller.core = presentationTheme{
		state:      state,
		getSetting: func() *string { return options.GetSettingsManager().GetThemeSetting() },
		output:     func() io.Writer { return options.Output }, renderer: func() tui.Renderer { return ui },
		showError: options.ShowError, changed: options.OnChanged,
	}
	if err := options.RunOnMain(ctx, func() {
		if options.InitialThemeSetting != nil {
			state.currentThemeSetting.Store(new(*options.InitialThemeSetting))
		}
		state.terminalTheme = tui.DetectTerminalBackground(tui.TerminalThemeDetectionOptions{}).Theme
		name, ok := tui.ResolveThemeSettingPresence(controller.core.getThemeSelection(), state.terminalTheme)
		if ok {
			tui.SetThemeByName(name, true)
			state.activeThemeName.Store(&name)
		} else {
			tui.SetThemeByName(tui.GetDefaultTheme(), true)
		}
	}); err != nil {
		cancel()
		return nil, err
	}
	return controller, nil
}

// ConsumeInput consumes terminal appearance reports before viewport or focused-component input. Call it on the owner loop.
func (controller *InteractiveThemeController) ConsumeInput(data string) bool {
	return controller.core.consumeInput(data)
}

// DisableAutoSync stops terminal color-scheme notifications on the owner loop.
func (controller *InteractiveThemeController) DisableAutoSync() { controller.core.setAutoSync(false) }

// ApplyFromSettings starts detection on the owner loop, waits off-loop for its completion or deadline, and persists a high-confidence detected default.
func (controller *InteractiveThemeController) ApplyFromSettings(ctx context.Context) error {
	controller.mu.Lock()
	if controller.closed {
		controller.mu.Unlock()
		return errors.New("Theme controller is disposed")
	}
	controller.tasks.Add(1)
	controller.mu.Unlock()
	defer controller.tasks.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(controller.ctx, cancel)
	defer func() { stop(); cancel() }()
	if controller.ctx.Err() != nil {
		cancel()
	}
	var query *interactiveThemeQuery
	if err := controller.options.RunOnMain(ctx, func() { query = controller.core.beginThemeDetection(controller.options.Output) }); err != nil {
		return err
	}
	if query == nil {
		return nil
	}
	timer := time.NewTimer(startupThemeQueryTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-query.done:
	case <-timer.C:
		if err := controller.options.RunOnMain(ctx, func() { query.detection.timeout(); controller.core.finishThemeDetection(query) }); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if query.persistTheme != nil {
		settings := controller.options.GetSettingsManager()
		if err := settings.SetTheme(*query.persistTheme); err != nil {
			return err
		}
		return settings.Flush()
	}
	return nil
}

// Dispose runs off-loop while the owner executor is alive. It releases query waiters and joins settings applications before returning.
func (controller *InteractiveThemeController) Dispose() error {
	controller.disposeOnce.Do(func() {
		controller.mu.Lock()
		controller.closed = true
		controller.cancel()
		controller.mu.Unlock()
		controller.disposeError = controller.options.RunOnMain(context.Background(), controller.core.dispose)
		controller.tasks.Wait()
	})
	return controller.disposeError
}
