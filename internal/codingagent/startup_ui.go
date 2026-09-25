package codingagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
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

// SelectStartupSession runs the same session selector used by /resume before
// cwd-bound runtime services exist. It returns selected=false on cancellation.
func SelectStartupSession(
	currentLoader func() ([]SessionInfo, error),
	allLoader func() ([]SessionInfo, error),
	opts StartupUIOptions,
) (path string, selected bool, err error) {
	keybindings := NewKeybindingsManager(opts.AgentDir)
	selector := newSessionSelector(currentLoader, allLoader, nil, nil, "", keybindings)
	selector.showRenameHint = false
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
	detection := newStartupThemeDetection(opts.Settings.Theme, env)
	if detection != nil {
		terminal.Write(detection.start())
		timer := time.NewTimer(startupThemeQueryTimeout)
		defer timer.Stop()
		themeTimeout = timer.C
	}
	applyTheme := func() {
		tui.SetThemeByName(detection.themeName())
		ui.Render()
	}

	buffer := newProcessStdinBuffer()
	var flush stdinFlushTimer
	defer flush.stop()
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
					dispatch(normalizeInputSequences(buffer.ProcessTerminalBytes(data)))
				}
			case <-done:
				for {
					select {
					case data := <-inputCh:
						if preserve {
							dispatch(normalizeInputSequences(buffer.ProcessTerminalBytes(data)))
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
	// Mirrors StdinBuffer's timeout for an incomplete sequence.
	scheduleFlush := func() {
		flush.sync(buffer)
	}

	for !component.Done() {
		select {
		case data := <-inputCh:
			dispatch(normalizeInputSequences(buffer.ProcessTerminalBytes(data)))
			scheduleFlush()
		case <-flush.C:
			flush.stop()
			dispatch(normalizeInputSequences(buffer.Flush()))
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

	// Stop waits until the startup reader can no longer consume stdin. Drain
	// anything it delivered before cancellation through the same decoder, then
	// preserve unread OS bytes for the next terminal owner.
	stopAndDrain(true)
	dispatch(normalizeInputSequences(buffer.Flush()))
	flush.stop()

	if clear {
		ui.Clear()
		ui.Render()
		time.Sleep(25 * time.Millisecond)
	}
	return true, nil
}

func configureStartupTheme(settings Settings, paths []string) {
	registry := tui.NewThemeRegistry()
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = registry.LoadDir(path)
			continue
		}
		if theme, err := tui.LoadThemeFile(path); err == nil {
			registry.Add(theme)
		}
	}
	tui.SetThemeRegistry(registry)
	tui.SetThemeSetting(settings.Theme)
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
	// terminalBackgroundQuery is OSC 11 `ESC ] 11 ; ? BEL`.
	terminalBackgroundQuery = "\x1b]11;?\x07"
)

var (
	osc11BackgroundResponsePattern = regexp.MustCompile(`(?i)^\x1b\]11;([^\x07\x1b]*)(?:\x07|\x1b\\)$`)
	colorSchemeReportPattern       = regexp.MustCompile(`^(?:\x1b\[\?997;(1|2)n)+$`)
)

// parseTerminalColorSchemeReport mirrors upstream
// parseTerminalColorSchemeReport.
func parseTerminalColorSchemeReport(data string) tui.TerminalTheme {
	match := colorSchemeReportPattern.FindStringSubmatch(data)
	if match == nil {
		return ""
	}
	if match[1] == "2" {
		return tui.TerminalTheme("light")
	}
	return tui.TerminalTheme("dark")
}

// startupThemeDetection tracks the concurrent color-scheme and OSC 11
// queries a startup prompt sends. The color-scheme reply wins; at the
// timeout the OSC 11 background, then the environment, decides.
type startupThemeDetection struct {
	themeSetting string
	env          map[string]string

	pendingBackgroundReplies int
	backgroundAnswered       bool
	background               *tui.RgbColor
	scheme                   tui.TerminalTheme
	settled                  bool
}

// newStartupThemeDetection returns nil when the theme setting names a fixed
// theme, which upstream applies without querying the terminal.
func newStartupThemeDetection(themeSetting string, env map[string]string) *startupThemeDetection {
	if themeSetting != "" {
		if _, _, auto := tui.ParseAutoThemeSetting(themeSetting); !auto {
			return nil
		}
	}
	return &startupThemeDetection{themeSetting: themeSetting, env: env}
}

// start returns the query bytes to write, color scheme first as upstream
// issues them.
func (d *startupThemeDetection) start() string {
	d.pendingBackgroundReplies++
	return terminalColorSchemeQuery + terminalBackgroundQuery
}

// consume reports whether chunk is a terminal color reply, which is never
// delivered as input, and whether it settled detection.
func (d *startupThemeDetection) consume(chunk string) (consumed, settled bool) {
	if d.pendingBackgroundReplies > 0 && osc11BackgroundResponsePattern.MatchString(chunk) {
		d.pendingBackgroundReplies--
		if !d.settled && !d.backgroundAnswered {
			d.backgroundAnswered = true
			d.background = tui.ParseOsc11BackgroundColor(chunk)
		}
		return true, false
	}
	if scheme := parseTerminalColorSchemeReport(chunk); scheme != "" {
		if d.settled {
			return true, false
		}
		d.scheme = scheme
		d.settled = true
		return true, true
	}
	return false, false
}

// timeout settles detection with whatever arrived before the deadline.
func (d *startupThemeDetection) timeout() bool {
	if d.settled {
		return false
	}
	d.settled = true
	return true
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
	if name, ok := tui.ResolveThemeSetting(d.themeSetting, terminalTheme); ok {
		return name
	}
	return string(terminalTheme)
}
