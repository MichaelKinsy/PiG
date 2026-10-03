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
	configureStartupTheme(opts.Settings, opts.ThemePaths)
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
	selector := newStartupSelector(title, options, opts)
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
	input := newStartupInput(title, placeholder, opts)
	completed, err := runStartupComponent(input, opts, true)
	if err != nil {
		return "", false, err
	}
	if !completed || input.Cancelled() {
		return "", false, nil
	}
	return input.Text(), true, nil
}

// newStartupSelector builds the selector after the startup theme is configured. Pi's showStartupSelector awaits
// createStartupTui, which registers the themes and marks the terminal colors pending, before it constructs the
// component (cli/startup-ui.ts:84-92,185-189), and ExtensionSelectorComponent themes its title and rows when it
// is constructed (extension-selector.ts:48-84), so they are drawn under the grayscale system theme.
func newStartupSelector(title string, options []string, opts StartupUIOptions) *tui.ExtensionSelectorComponent {
	configureStartupTheme(opts.Settings, opts.ThemePaths)
	return tui.NewExtensionSelector(title, options)
}

// newStartupInput builds the input after the startup theme is configured, as showStartupInput does (cli/startup-ui.ts:238-262).
func newStartupInput(title, placeholder string, opts StartupUIOptions) *tui.ExtensionInputComponent {
	configureStartupTheme(opts.Settings, opts.ThemePaths)
	return tui.NewExtensionInputComponent(title, placeholder)
}

// startupTerminal is the terminal surface a startup prompt drives.
type startupTerminal interface {
	StartWithReadError(onInput func([]byte), onResize func(), onReadError func(error)) error
	Stop()
	Write(data string)
}

// runStartupComponent runs a component built after configureStartupTheme.
func runStartupComponent(component startupComponent, opts StartupUIOptions, clear bool) (bool, error) {
	ui := tui.New()
	ui.SetLogDirectory(opts.AgentDir)
	return runStartupComponentWith(component, opts, clear, ui, tui.NewProcessTerminal(os.Stdin, os.Stdout))
}

// runStartupComponentWith runs a startup prompt on ui and terminal. Mirrors
// upstream startStartupTui: the prompt renders at once while the terminal's
// colors are queried; replies, including those after the timeout, retheme the
// prompt and are never delivered as input.
func runStartupComponentWith(component startupComponent, opts StartupUIOptions, clear bool, ui *tui.TUI, terminal startupTerminal) (bool, error) {
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

	// The terminal's colors regenerate the system theme and resolve "" tokens; re-rendering rebuilds the prompt with them.
	themeSetting := opts.Settings.themeSetting()
	themed, _ := component.(interface{ startupTheme() string })
	applyColors := func(colors tui.TerminalColors) {
		tui.SetTerminalColors(colors)
		name, ok := tui.ResolveThemeSettingPresence(themeSetting, tui.GetTerminalTheme())
		if !ok {
			name = tui.SystemThemeName
		}
		if themed != nil {
			name = themed.startupTheme()
		}
		tui.SetThemeByName(name)
		ui.Invalidate()
		ui.Render()
	}
	colorResults := ui.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: float64(terminalColorQueryTimeout / time.Millisecond), OnLateReply: applyColors})

	// settleColors applies a completed query. A completed query is applied before the next input chunk is dispatched, as upstream's promise continuation runs before the next terminal input event.
	settleColors := func(result tui.TerminalColorsResult) {
		colorResults = nil
		// A failed query applies no colors, like a terminal that does not report them.
		if result.Err != nil {
			result.Colors = tui.TerminalColors{}
		}
		applyColors(result.Colors)
	}
	dispatch := func(chunks []string) {
		select {
		case result := <-colorResults:
			settleColors(result)
		default:
		}
		input := chunks[:0:0]
		for _, chunk := range chunks {
			if ui.ConsumeTerminalColorResponse(chunk) {
				continue
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
		case result := <-colorResults:
			settleColors(result)
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

// configureStartupTheme mirrors createStartupTui (cli/startup-ui.ts:84-92): it applies the terminal capability
// overrides before it creates the theme, so the theme, and every prompt row built under it, uses the overridden
// color mode.
func configureStartupTheme(settings Settings, paths []string) {
	tui.SetCapabilityOverrides(settings.GetTerminalCapabilityOverrides())
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
	// The system theme starts in grayscale until the terminal reports its colors.
	tui.MarkTerminalColorsPending()
	tui.SetThemeSettingPresence(settings.themeSetting())
}
