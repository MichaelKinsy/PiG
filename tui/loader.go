package tui

// loader.go: animated loader component.
//
// Ports upstream packages/tui/src/components/loader.ts.
// Upstream Loader extends Text with paddingX=1, paddingY=0 and renders
// ["", ...text.render(width)]. The text content is the styled indicator
// (spinner frame + space) concatenated with the styled message.

// DefaultSpinnerFrames is the Braille spinner used by upstream.
var DefaultSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// loaderPaddingX matches upstream Loader's paddingX (inherited from Text
// constructor arg). Upstream: `super("", 1, 0)`.
const loaderPaddingX = 1

// Loader shows an animated spinner with a message.
// Matches upstream Loader which extends Text(paddingX=1, paddingY=0).
type Loader struct {
	invalidatable
	Message           string
	Frame             int
	Frames            []string
	SpinnerColor      string // ANSI fg escape for spinner (optional)
	MessageColor      string // ANSI fg escape for message (optional)
	IndicatorVerbatim bool   // extension frames include their own formatting
}

func NewLoader(message string) *Loader {
	return &Loader{Message: message, Frames: append([]string{}, DefaultSpinnerFrames...)}
}

// NewStyledLoader creates a Loader with ANSI color escapes.
func NewStyledLoader(spinnerColor, messageColor, message string, frames []string) *Loader {
	if len(frames) == 0 {
		frames = append([]string{}, DefaultSpinnerFrames...)
	}
	return &Loader{
		Message:      message,
		Frames:       frames,
		SpinnerColor: spinnerColor,
		MessageColor: messageColor,
	}
}

// Render produces ["", paddedLine] matching upstream Loader.render(width)
// which returns ["", ...super.render(width)] where super is Text(paddingX=1).
func (l *Loader) Render(width int) []string {
	content := l.styledMessage()
	if indicator := l.renderedIndicator(); indicator != "" {
		content = indicator + " " + content
	}

	// Upstream Loader extends Text(paddingX=1, paddingY=0) and prepends one
	// empty line in its render override.
	return append([]string{""}, NewPaddedText(content, loaderPaddingX, 0, nil).Render(width)...)
}

func (l *Loader) styledMessage() string {
	if l.MessageColor != "" {
		return l.MessageColor + l.Message + "\x1b[0m"
	}
	return l.Message
}

// SetMessage replaces the message and marks the loader for repaint. Mirrors
// upstream Loader.setMessage, which updates the text and requests a render.
func (l *Loader) SetMessage(message string) {
	l.Message = message
	l.Invalidate()
}

// SetIndicator replaces the animation frames, restarts at the first frame, and
// marks the loader for repaint. nil frames select DefaultSpinnerFrames and an
// empty slice hides the indicator. verbatim renders caller-formatted frames
// without the spinner color. Mirrors upstream Loader.setIndicator; the owner
// that ticks the loader applies the frame interval.
func (l *Loader) SetIndicator(frames []string, verbatim bool) {
	if frames == nil {
		frames = DefaultSpinnerFrames
	}
	l.Frames = append([]string{}, frames...)
	l.IndicatorVerbatim = verbatim
	l.Frame = 0
	l.Invalidate()
}

// Tick advances the spinner one frame.
func (l *Loader) Tick() {
	l.Frame++
	l.Invalidate()
}

func (l *Loader) renderedIndicator() string {
	if len(l.Frames) == 0 {
		return ""
	}
	frame := l.Frames[l.Frame%len(l.Frames)]
	if l.SpinnerColor != "" && !l.IndicatorVerbatim {
		return l.SpinnerColor + frame + "\x1b[0m"
	}
	return frame
}
