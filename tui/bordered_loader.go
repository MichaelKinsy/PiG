package tui

// bordered_loader.go: loader wrapped with borders.
//
// Ports upstream bordered-loader.ts (68 LOC).
// Wraps a Loader or CancellableLoader with DynamicBorder top/bottom.

// BorderedLoader renders a loader between two horizontal borders,
// optionally with an Esc cancel hint.
type BorderedLoader struct {
	invalidatable
	loader        *Loader
	cancellable   *CancellableLoader // non-nil when cancellable=true
	isCancellable bool
}

// NewBorderedLoader creates a bordered loader. When cancellable is true,
// an Esc cancel hint is shown and HandleInput cancels on Esc.
func NewBorderedLoader(message string, cancellable bool) *BorderedLoader {
	t := ActiveTheme()
	spinnerColor := t.Accent
	messageColor := t.Muted

	bl := &BorderedLoader{isCancellable: cancellable}
	if cancellable {
		cl := NewCancellableLoader(spinnerColor, messageColor, message, nil)
		bl.cancellable = cl
		bl.loader = &cl.Loader
	} else {
		bl.loader = NewStyledLoader(spinnerColor, messageColor, message, nil)
	}
	return bl
}

// Render produces border + loader + optional hint + border.
func (bl *BorderedLoader) Render(width int) []string {
	border := NewDynamicBorder("")
	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, bl.loader.Render(width)...)
	if bl.isCancellable {
		lines = append(lines, "")
		// Upstream: Text(keyHint("tui.select.cancel", "cancel"), 1, 0).
		lines = append(lines, NewPaddedText(KeyHint("escape/ctrl+c", "cancel"), 1, 0, nil).Render(width)...)
	}
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}

// HandleInput delegates to the cancellable loader if present.
func (bl *BorderedLoader) HandleInput(data string) {
	if bl.cancellable != nil {
		bl.cancellable.HandleInput(data)
	}
}

// CancellableContext returns the cancellable loader's context, or nil.
func (bl *BorderedLoader) CancellableContext() *CancellableLoader {
	return bl.cancellable
}

// Dispose stops the loader.
func (bl *BorderedLoader) Dispose() {
	if bl.cancellable != nil {
		bl.cancellable.Dispose()
	}
}

// NextFrame advances the spinner animation.
func (bl *BorderedLoader) NextFrame() {
	bl.loader.Tick()
}
