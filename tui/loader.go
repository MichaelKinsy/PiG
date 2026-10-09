package tui

import (
	"math"
	"strings"
	"sync"
	"time"
)

// loader.go: animated loader component.
//
// Ports packages/tui/src/components/loader.ts.
// Upstream Loader extends Text with paddingX=1, paddingY=0 and renders
// ["", ...text.render(width)]. The text content is the styled indicator
// (spinner frame + space) concatenated with the styled message.

// DefaultSpinnerFrames is the Braille spinner used by upstream.
var DefaultSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// DefaultLoaderIntervalMs is upstream's DEFAULT_INTERVAL_MS.
const DefaultLoaderIntervalMs = 80

// loaderPaddingX matches upstream Loader's paddingX (inherited from Text
// constructor arg). Upstream: `super("", 1, 0)`.
const loaderPaddingX = 1

// Loader shows an animated spinner with a message.
// Matches upstream Loader which extends Text(paddingX=1, paddingY=0).
//
// Loader embeds Text, as upstream Loader extends Text: SetText and SetCustomBgFn are Text's, and the text shown is rewritten from the indicator and message on every render, as upstream updateDisplay does.
type Loader struct {
	Text
	// IntervalMs is the Start animation interval; zero selects DefaultLoaderIntervalMs.
	IntervalMs int

	mu                sync.Mutex
	stopCh            chan struct{}
	Message           string
	Frame             int
	Frames            []string
	IndicatorVerbatim bool // extension frames include their own formatting

	// ui is the TUI the Loader asks for a render after each change, nil for none (loader.ts `private ui: TUI | null`).
	ui TUI
	// spinnerColorFn and messageColorFn style the spinner frame and the message; nil leaves the text as it is (loader.ts spinnerColorFn, messageColorFn).
	spinnerColorFn func(string) string
	messageColorFn func(string) string

	// paddingX is the horizontal padding set by SetPaddingX; a Loader that never set it keeps loaderPaddingX.
	paddingX    int
	paddingXSet bool
}

// NewLoader is `new Loader(ui, spinnerColorFn, messageColorFn, message, indicator)` (components/loader.ts:28-41). ui may be nil; it receives a RequestRender after every change to the shown text. A nil color function leaves its text unstyled. A nil indicator is upstream's undefined (the default spinner); the constructor applies a non-nil one through SetIndicator, as upstream's constructor calls setIndicator(indicator).
func NewLoader(ui TUI, spinnerColorFn, messageColorFn func(string) string, message string, indicator *LoaderIndicatorOptions) *Loader {
	l := &Loader{ui: ui, spinnerColorFn: spinnerColorFn, messageColorFn: messageColorFn, Message: message}
	l.SetIndicator(indicator)
	return l
}

// requestRender asks the Loader's TUI for a render (loader.ts updateDisplay: `if (this.ui) this.ui.requestRender()`). The caller must not hold l.mu.
func (l *Loader) requestRender() {
	if l.ui != nil {
		l.ui.RequestRender()
	}
}

// Render produces ["", paddedLine] matching upstream Loader.render(width)
// which returns ["", ...super.render(width)] where super is Text(paddingX=1).
func (l *Loader) Render(width int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.updateDisplay()
	// Upstream Loader extends Text(paddingX=1, paddingY=0) and prepends one
	// empty line in its render override.
	return append([]string{""}, l.Text.Render(width)...)
}

// updateDisplay rewrites the Text content from the current indicator frame and message (upstream updateDisplay). The caller holds mu.
func (l *Loader) updateDisplay() {
	content := l.styledMessage()
	if indicator := l.renderedIndicator(); indicator != "" {
		content = indicator + " " + content
	}
	l.PaddingX, l.PaddingY = loaderPaddingX, 0
	if l.paddingXSet {
		l.PaddingX = l.paddingX
	}
	if l.Content != content {
		l.Content = content
		l.Text.Invalidate()
	}
}

// Start renders the current frame and restarts the animation timer: with more than one frame, a goroutine advances the frame every IntervalMs until Stop (upstream Loader.start).
func (l *Loader) Start() {
	l.mu.Lock()
	l.updateDisplay()
	l.restartAnimation()
	l.mu.Unlock()
	l.requestRender()
}

// Stop ends the animation timer (upstream Loader.stop). It is safe to call more than once.
func (l *Loader) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopAnimation()
}

func (l *Loader) stopAnimation() {
	if l.stopCh != nil {
		//portlint:allow doubleclose the channel is set to nil right after the close, and every caller holds l.mu
		close(l.stopCh)
		l.stopCh = nil
	}
}

func (l *Loader) restartAnimation() {
	l.stopAnimation()
	if len(l.Frames) <= 1 {
		return
	}
	interval := time.Duration(l.IntervalMs) * time.Millisecond
	if l.IntervalMs <= 0 {
		interval = DefaultLoaderIntervalMs * time.Millisecond
	}
	stop := make(chan struct{})
	l.stopCh = stop
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				l.mu.Lock()
				select {
				case <-stop:
					l.mu.Unlock()
					return
				default:
				}
				l.Frame = (l.Frame + 1) % len(l.Frames)
				l.updateDisplay()
				l.invalidatable.Invalidate()
				l.mu.Unlock()
				l.requestRender()
			}
		}
	}()
}

func (l *Loader) styledMessage() string {
	if l.messageColorFn != nil {
		return l.messageColorFn(l.Message)
	}
	return l.Message
}

// SpinnerColorPrefix is the escape sequence the spinner color function writes before its text, "" for none. The remote editor carries it instead of the function.
func (l *Loader) SpinnerColorPrefix() string { return colorFnPrefix(l.spinnerColorFn) }

// MessageColorPrefix is the escape sequence the message color function writes before its text, "" for none.
func (l *Loader) MessageColorPrefix() string { return colorFnPrefix(l.messageColorFn) }

// colorFnPrefix is what fn writes before the text it styles: fn is applied to a marker and the text before the marker is the prefix.
func colorFnPrefix(fn func(string) string) string {
	if fn == nil {
		return ""
	}
	const marker = "\x00"
	prefix, _, _ := strings.Cut(fn(marker), marker)
	return prefix
}

// SGRColor is the color function for an SGR escape prefix: it writes prefix before the text and resets the foreground after it. An empty prefix leaves the text as it is.
func SGRColor(prefix string) func(string) string {
	if prefix == "" {
		return nil
	}
	return func(text string) string { return prefix + text + FgClose(prefix) }
}

// ThemeFg is `(text) => theme.fg(token, text)`, the color function upstream's callers pass: it styles with the active theme when it is called.
func ThemeFg(token string) func(string) string {
	return func(text string) string { return ActiveTheme().Fg(token, text) }
}

// SetMessage replaces the message and marks the loader for repaint. Mirrors
// upstream Loader.setMessage, which updates the text and requests a render.
func (l *Loader) SetMessage(message string) {
	l.mu.Lock()
	l.Message = message
	l.mu.Unlock()
	l.Invalidate()
}

// LoaderIndicatorOptions mirrors upstream LoaderIndicatorOptions. Frames nil is
// upstream's undefined (the default spinner) and an empty slice hides the
// indicator; IntervalMs zero, negative or unset selects DefaultLoaderIntervalMs.
type LoaderIndicatorOptions struct {
	Frames     []string
	IntervalMs float64
}

// SetIndicator mirrors upstream Loader.setIndicator: a nil indicator restores the
// default spinner, and a non-nil one renders its frames verbatim (without the
// spinner color), using Frames when set and IntervalMs when positive. It restarts
// at the first frame and marks the loader for repaint. The animation restarts only
// when it is running; the owner that ticks the loader starts it otherwise.
func (l *Loader) SetIndicator(indicator *LoaderIndicatorOptions) {
	l.mu.Lock()
	l.setIndicatorLocked(indicator)
	l.mu.Unlock()
	l.requestRender()
}

func (l *Loader) setIndicatorLocked(indicator *LoaderIndicatorOptions) {
	frames := DefaultSpinnerFrames
	l.IndicatorVerbatim = indicator != nil
	l.IntervalMs = DefaultLoaderIntervalMs
	if indicator != nil {
		if indicator.Frames != nil {
			frames = indicator.Frames
		}
		if indicator.IntervalMs > 0 {
			// upstream: setInterval coerces a delay below 1, or above TIMEOUT_MAX (2^31-1, Infinity included), to 1, and libuv truncates a fraction.
			l.IntervalMs = 1
			if indicator.IntervalMs >= 1 && indicator.IntervalMs <= math.MaxInt32 {
				//portlint:allow numbers the guard admits only finite values in [1, 2^31-1], whose truncation is Node's
				l.IntervalMs = int(indicator.IntervalMs)
			}
		}
	}
	l.Frames = append([]string{}, frames...)
	l.Frame = 0
	if l.stopCh != nil {
		l.restartAnimation()
	}
	l.Text.Invalidate()
}

// Invalidate marks the loader for repaint and drops the cached text lines.
func (l *Loader) Invalidate() {
	l.mu.Lock()
	l.Text.Invalidate()
	l.mu.Unlock()
	l.requestRender()
}

// Tick advances the spinner one frame.
func (l *Loader) Tick() {
	l.mu.Lock()
	l.Frame++
	l.mu.Unlock()
	l.Invalidate()
}

func (l *Loader) renderedIndicator() string {
	if len(l.Frames) == 0 {
		return ""
	}
	frame := l.Frames[l.Frame%len(l.Frames)]
	if l.spinnerColorFn != nil && !l.IndicatorVerbatim {
		return l.spinnerColorFn(frame)
	}
	return frame
}

// SetPaddingX is Text.setPaddingX on the Loader, which extends Text: the padding applies to every later render in place of the default of 1.
func (l *Loader) SetPaddingX(paddingX int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paddingX, l.paddingXSet = paddingX, true
	l.PaddingX = paddingX
	l.Text.Invalidate()
}
