package tui

// bordered_loader.go: loader wrapped with borders.
//
// Ports upstream bordered-loader.ts (68 LOC).
// Wraps a Loader or CancellableLoader with DynamicBorder top/bottom.

// BorderedLoader extends Container (upstream BorderedLoader extends Container):
// its children are a border, the loader, an optional cancel hint and a border.
type BorderedLoader struct {
	*Container
	loader        *Loader
	cancellable   *CancellableLoader // non-nil when cancellable=true
	isCancellable bool
	// OnAbort is called after the cancellable loader is aborted; ignored for a
	// non-cancellable loader (upstream `set onAbort`).
	OnAbort func()
}

// BorderedLoaderOptions is upstream's `{ cancellable?: boolean }`: a nil Cancellable is cancellable.
type BorderedLoaderOptions struct{ Cancellable *bool }

// NewBorderedLoader is bordered-loader.ts constructor(tui, theme, message, options?): the loader asks ui to render on every frame and takes
// its colours from theme. Unless options set Cancellable to false, an Esc cancel hint is shown and HandleInput cancels on Esc. A nil theme is
// the active theme.
func NewBorderedLoader(ui TUI, theme *Theme, message string, options ...BorderedLoaderOptions) *BorderedLoader {
	if theme == nil {
		theme = ActiveTheme()
	}
	cancellable := true
	if len(options) > 0 && options[0].Cancellable != nil {
		cancellable = *options[0].Cancellable
	}
	borderColor := func(text string) string { return theme.Fg("border", text) }
	spinnerColor := func(text string) string { return theme.Fg("accent", text) }
	messageColor := func(text string) string { return theme.Fg("muted", text) }

	bl := &BorderedLoader{Container: NewContainer(), isCancellable: cancellable}
	bl.AddChild(NewDynamicBorder(borderColor))
	if cancellable {
		cl := NewCancellableLoader(ui, spinnerColor, messageColor, message, nil)
		cl.OnAbort = func() {
			if bl.OnAbort != nil {
				bl.OnAbort()
			}
		}
		bl.cancellable = cl
		bl.loader = &cl.Loader
	} else {
		bl.loader = NewLoader(ui, spinnerColor, messageColor, message, nil)
	}
	bl.AddChild(bl.loader)
	if cancellable {
		bl.AddChild(NewSpacer(1))
		// Upstream: Text(keyHint("tui.select.cancel", "cancel"), 1, 0).
		bl.AddChild(NewPaddedText(loginKeyHint(KBSelectCancel, "cancel"), 1, 0, nil))
	}
	bl.AddChild(NewSpacer(1))
	bl.AddChild(NewDynamicBorder(borderColor))
	return bl
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

// Dispose is upstream dispose() (bordered-loader.ts:61): a CancellableLoader is disposed, a plain Loader is stopped.
func (bl *BorderedLoader) Dispose() {
	if bl.cancellable != nil {
		bl.cancellable.Dispose()
		return
	}
	bl.loader.Stop()
}

// NextFrame advances the spinner animation.
func (bl *BorderedLoader) NextFrame() {
	bl.loader.Tick()
}
