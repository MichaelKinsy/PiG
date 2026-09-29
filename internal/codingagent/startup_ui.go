package codingagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type startupComponent interface {
	tui.Component
	HandleInput(string)
	Done() bool
}

// dispatchStartupInput applies one decoded terminal batch. The caller retains
// any returned sequences for the next focused component.
func dispatchStartupInput(component startupComponent, chunks []string) []string {
	if component.Done() {
		return chunks
	}
	for i, chunk := range chunks {
		if !tui.ShouldDeliverKey(component, chunk) {
			continue
		}
		component.HandleInput(chunk)
		if component.Done() {
			return chunks[i+1:]
		}
	}
	return nil
}

var startupInputCarryover struct {
	sync.Mutex
	chunks []string
}

func retainStartupInput(chunks []string) {
	if len(chunks) == 0 {
		return
	}
	startupInputCarryover.Lock()
	startupInputCarryover.chunks = append(startupInputCarryover.chunks, chunks...)
	startupInputCarryover.Unlock()
}

func takeStartupInput() []string {
	startupInputCarryover.Lock()
	defer startupInputCarryover.Unlock()
	chunks := startupInputCarryover.chunks
	startupInputCarryover.chunks = nil
	return chunks
}

type StartupUIOptions struct {
	AgentDir   string
	Settings   Settings
	ThemePaths []string
}

// SelectStartupSession runs the same session selector used by /resume before cwd-bound runtime services exist. Loaders run off the input owner, receive cancellation and progress options, and settle before teardown returns. Confirmed deletion tries trash before unlink; renaming is unavailable. It returns selected=false on cancellation.
func SelectStartupSession(
	currentLoader func(SessionListOptions) ([]SessionInfo, error),
	allLoader func(SessionListOptions) ([]SessionInfo, error),
	opts StartupUIOptions,
) (path string, selected bool, err error) {
	selector := newStartupSessionSelector(currentLoader, allLoader, NewKeybindingsManager(opts.AgentDir))
	completed, err := runStartupComponent(selector, opts, false)
	if err != nil {
		return "", false, err
	}
	if !completed || selector.Cancelled() {
		return "", false, nil
	}
	path = selector.SelectedPath()
	return path, path != "", nil
}

// newStartupSessionSelector mirrors Pi's --resume picker: deletion operates on the path returned by the loaders, without rename or its hint.
func newStartupSessionSelector(currentLoader, allLoader func(SessionListOptions) ([]SessionInfo, error), keybindings *KeybindingsManager) *sessionSelector {
	selector := newSessionSelector(currentLoader, allLoader, nil, deleteSessionFile, "", keybindings)
	selector.showRenameHint = false
	return selector
}

// ShowStartupSelector displays a small pre-runtime choice list. It returns
// selected=false when the user cancels.
func ShowStartupSelector(title string, options []string, opts StartupUIOptions) (index int, selected bool, err error) {
	selector := tui.NewExtensionSelector(title, options)
	completed, err := runStartupComponent(selector, opts, true)
	if err != nil {
		return -1, false, err
	}
	if !completed || selector.Cancelled() {
		return -1, false, nil
	}
	index = selector.SelectedIndex()
	return index, index >= 0 && index < len(options), nil
}

// ShowStartupInput displays the extension text-input surface before runtime
// services exist. It returns selected=false when the user cancels.
func ShowStartupInput(title, placeholder string, opts StartupUIOptions) (value string, selected bool, err error) {
	input := tui.NewExtensionInputComponent(title, placeholder)
	completed, err := runStartupComponent(input, opts, true)
	if err != nil {
		return "", false, err
	}
	if !completed || input.Cancelled() {
		return "", false, nil
	}
	return input.Text(), true, nil
}

// startupTerminal is the terminal surface a startup prompt drives.
type startupTerminal interface {
	StartWithReadError(onInput func([]byte), onResize func(), onReadError func(error)) error
	Stop()
	Write(data string)
}

func runStartupComponent(component startupComponent, opts StartupUIOptions, clear bool) (bool, error) {
	configureStartupTheme(opts.Settings, opts.ThemePaths)
	ui := tui.New()
	ui.SetLogDirectory(opts.AgentDir)
	return runStartupComponentWith(component, opts, clear, ui, tui.NewProcessTerminal(os.Stdin, os.Stdout), nil)
}

// runStartupComponentWith runs a startup prompt on ui and terminal. Mirrors
// upstream startStartupTui: the prompt renders at once while the terminal's
// color scheme and background are queried; replies retheme the prompt and
// are never delivered as input. env overrides the environment consulted
// when the terminal does not answer.
func runStartupComponentWith(component startupComponent, opts StartupUIOptions, clear bool, ui *tui.TUI, terminal startupTerminal, env map[string]string) (bool, error) {
	asyncSelector, _ := component.(*sessionSelector)
	var updates <-chan func()
	var ready <-chan struct{}
	if asyncSelector != nil {
		defer asyncSelector.close()
		updates = asyncSelector.work.updates
		ready = asyncSelector.work.ready
		asyncSelector.drainLoadUpdates()
	}
	// Mirrors upstream createStartupTui, which applies the terminal
	// capability overrides before the prompt renders.
	tui.SetCapabilityOverrides(opts.Settings.GetTerminalCapabilityOverrides())
	ui.SetShowHardwareCursor(opts.Settings.GetShowHardwareCursor())
	ui.SetClearOnShrink(opts.Settings.GetClearOnShrink())
	ui.Add(component)

	inputCh := make(chan []byte, 32)
	inputErrCh := make(chan error, 1)
	resizeCh := make(chan struct{}, 1)
	if err := terminal.StartWithReadError(func(data []byte) {
		inputCh <- append([]byte(nil), data...)
	}, func() {
		select {
		case resizeCh <- struct{}{}:
		default:
		}
	}, func(err error) {
		inputErrCh <- err
	}); err != nil {
		return false, fmt.Errorf("startup UI terminal: %w", err)
	}
	stopped := false
	ui.HideCursor()
	defer ui.Stop()
	ui.Render()

	var themeTimeout <-chan time.Time
	detection := newStartupThemeDetection(opts.Settings.themeSetting(), env, ui)
	if detection != nil {
		detection.start(func(sequence string) error { terminal.Write(sequence); return nil })
		timer := time.NewTimer(startupThemeQueryTimeout)
		defer timer.Stop()
		themeTimeout = timer.C
	}
	applyTheme := func() {
		tui.SetThemeByName(detection.themeName())
		ui.Render()
	}

	dispatch := func(chunks []string) {
		input := chunks[:0:0]
		for _, chunk := range chunks {
			if detection != nil {
				consumed, settled := detection.consume(chunk)
				if settled {
					applyTheme()
				}
				if consumed {
					continue
				}
			}
			if chunk != "" {
				input = append(input, chunk)
			}
		}
		// Sequences after the confirming key belong to the next focused
		// component (the next startup prompt or the interactive editor).
		retainStartupInput(dispatchStartupInput(component, input))
		ui.Render()
	}
	stopAndDrain := func(preserve bool) {
		if stopped {
			return
		}
		done := make(chan struct{})
		go func() {
			terminal.Stop()
			close(done)
		}()
		for {
			select {
			case data := <-inputCh:
				if preserve {
					dispatch([]string{string(data)})
				}
			case <-done:
				for {
					select {
					case data := <-inputCh:
						if preserve {
							dispatch([]string{string(data)})
						}
					default:
						stopped = true
						return
					}
				}
			}
		}
	}
	defer stopAndDrain(false)
	dispatch(takeStartupInput())

	for !component.Done() {
		select {
		case <-ready:
			asyncSelector.drainLoadUpdates()
			ui.Render()
		case result := <-asyncSelector.loadResult(sessionScopeCurrent):
			asyncSelector.finishLoad(sessionScopeCurrent, result)
			ui.Render()
		case result := <-asyncSelector.loadResult(sessionScopeAll):
			asyncSelector.finishLoad(sessionScopeAll, result)
			ui.Render()
		case update := <-updates:
			update()
			ui.Render()
		case <-asyncSelector.statusTimeout():
			asyncSelector.clearStatusMessage()
			ui.Render()
		case data := <-inputCh:
			dispatch([]string{string(data)})
		case <-themeTimeout:
			themeTimeout = nil
			if detection.timeout() {
				applyTheme()
			}
		case <-resizeCh:
			ui.Render()
		case err := <-inputErrCh:
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, fmt.Errorf("startup UI terminal input: %w", err)
		}
	}

	if asyncSelector != nil && asyncSelector.operationError != nil {
		return false, asyncSelector.operationError
	}

	// Stop joins the terminal's decoder before retaining its final events for the next owner.
	stopAndDrain(true)

	if clear {
		ui.Clear()
		ui.Render()
		time.Sleep(25 * time.Millisecond)
	}
	return true, nil
}

func configureStartupTheme(settings Settings, paths []string) {
	registry := tui.NewThemeRegistry()
	// paths are in upstream precedence order, the first theme of a name
	// winning; the registry keeps the last one added.
	for _, path := range slices.Backward(paths) {

		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = registry.LoadDir(path)
			continue
		}
		if theme, err := tui.LoadThemeFile(path); err == nil {
			registry.AddFile(theme, path)
		}
	}
	tui.SetThemeRegistry(registry)
	tui.SetThemeSettingPresence(settings.themeSetting())
}

// Startup theme detection. Mirrors applyDetectedStartupTheme in upstream
// packages/coding-agent/src/cli/startup-ui.ts, detectTerminalThemeForAuto in
// modes/interactive/theme/theme.ts, and the queryTerminalColorScheme /
// queryTerminalBackgroundColor reply handling of packages/tui/src/tui.ts and
// terminal-colors.ts.

const (
	startupThemeQueryTimeout = 100 * time.Millisecond
	// terminalColorSchemeQuery is DSR `CSI ? 996 n`; terminals reply
	// `CSI ? 997 ; 1 n` (dark) or `CSI ? 997 ; 2 n` (light).
	terminalColorSchemeQuery = "\x1b[?996n"
)

// startupThemeDetection tracks initial appearance queries for startup prompts and the interactive mode. Background-only detection settles on OSC 11; automatic detection prefers the color-scheme reply and falls back to OSC 11 or the environment at the deadline.
type startupThemeDetection struct {
	themeSetting *string
	env          map[string]string

	renderer           tui.Renderer
	backgroundQuery    <-chan tui.TerminalBackgroundColorResult
	backgroundAnswered bool
	background         *tui.RgbColor
	scheme             tui.TerminalTheme
	settled            bool
	schemeUnavailable  bool
	backgroundOnly     bool
}

// newStartupThemeDetection returns nil when the theme setting names a nonempty
// fixed theme, which startup prompts apply without querying the terminal. An
// unset or empty setting still queries; the empty name then resolves to itself.
func newStartupThemeDetection(themeSetting *string, env map[string]string, renderer tui.Renderer) *startupThemeDetection {
	if themeSetting != nil && *themeSetting != "" {
		if _, _, auto := tui.ParseAutoThemeSetting(*themeSetting); !auto {
			return nil
		}
	}
	return &startupThemeDetection{themeSetting: themeSetting, env: env, renderer: renderer}
}

// start issues OSC 11 through the renderer's shared FIFO after the optional color-scheme query.
func (d *startupThemeDetection) start(writeScheme func(string) error) {
	if !d.backgroundOnly {
		d.schemeUnavailable = writeScheme(terminalColorSchemeQuery) != nil
	}
	d.backgroundQuery = d.renderer.QueryTerminalBackgroundColor(tui.TerminalColorQueryOptions{TimeoutMs: float64(startupThemeQueryTimeout / time.Millisecond)})
}

// consume reports whether chunk is a terminal color reply, which is never
// delivered as input, and whether it settled detection.
func (d *startupThemeDetection) consume(chunk string) (consumed, settled bool) {
	if d.renderer.ConsumeOsc11BackgroundResponse(chunk) {
		return true, d.readBackground()
	}
	if scheme := tui.ParseTerminalColorSchemeReport(chunk); scheme != "" {
		if d.settled || d.backgroundOnly || d.schemeUnavailable {
			return true, false
		}
		d.scheme = scheme
		d.settled = true
		return true, true
	}
	return false, false
}

// readBackground observes completion without blocking the input owner. Timed-out reply slots remain renderer-owned after this detection finishes.
func (d *startupThemeDetection) readBackground() bool {
	select {
	case result := <-d.backgroundQuery:
		d.backgroundQuery = nil
		d.backgroundAnswered = true
		if d.settled {
			return false
		}
		if result.Err == nil {
			d.background = result.Color
		}
		if d.backgroundOnly || d.schemeUnavailable {
			d.settled = true
			return true
		}
	default:
	}
	return false
}

// timeout settles detection with whatever arrived before the deadline.
func (d *startupThemeDetection) timeout() bool {
	wasSettled := d.settled
	d.readBackground()
	d.settled = true
	return !wasSettled
}

// terminalTheme mirrors detectTerminalThemeForAuto's result.
func (d *startupThemeDetection) terminalTheme() tui.TerminalTheme {
	if d.scheme != "" {
		return d.scheme
	}
	if d.background != nil {
		return tui.GetThemeForRgbColor(*d.background)
	}
	return tui.DetectTerminalBackground(tui.TerminalThemeDetectionOptions{Env: d.env}).Theme
}

// themeName resolves the setting against the detected appearance.
func (d *startupThemeDetection) themeName() string {
	terminalTheme := d.terminalTheme()
	if name, ok := tui.ResolveThemeSettingPresence(d.themeSetting, terminalTheme); ok {
		return name
	}
	return string(terminalTheme)
}
