// ext_ui_context.go implements extension.UIContext for interactive mode.
//
// Bridges the coding/extension UIContext interface to the real TUI so
// subprocess extensions can show selectors, input dialogs, and
// confirmations via the editor slot (matching upstream's
// showExtensionSelector / showExtensionInput / showExtensionConfirm).
//
// pig-specific: upstream's interactive-mode.ts creates these inline;
// pig needs a separate type because the UIContext interface lives in
// coding/extension/ and the TUI lives in internal/.

package codingagent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ExtUIContext implements extension.UIContext for interactive mode.
// Subprocess extensions' Select/Confirm/Input/Editor calls are
// dispatched here via the UIBridge.
type ExtUIContext struct {
	m *InteractiveMode
}

type specialLinesComponent struct {
	mu         sync.RWMutex
	lines      []string
	linesWidth int
	render     func(width int) []string
	// mouse handles mouse events for the built-in header, and click reports the part of its lines that takes a click;
	// installing other lines or another renderer removes both.
	mouse      func(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
	click      func() (frontend.Area, bool)
	invalidate func()
	// view draws the header or footer when the extension's frame carried a
	// view (D107); linesWidth is then a Node frame's width, or 0.
	view extension.ViewSurface
}

func newSpecialLinesComponent(invalidate func()) *specialLinesComponent {
	return &specialLinesComponent{invalidate: invalidate}
}

// Render returns the header/footer rows for width. A built-in or login
// renderer renders at width, like upstream's component factories. Lines pushed
// by a subprocess extension carry the width they were rendered at; a frame for
// another width is never painted, whatever its rows (the extension
// re-renders on the host's width_change notification). Rows are otherwise
// painted as rendered: an over-wide row reaches the renderer's overflow check,
// as a component that does not truncate does in Pi.
func (c *specialLinesComponent) Render(width int) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.render != nil {
		return c.render(width)
	}
	if c.view != nil {
		return widthx.FrameAt(c.view.Render(width), c.linesWidth, width)
	}
	if len(c.lines) == 0 {
		return nil
	}
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return widthx.FrameAt(out, c.linesWidth, width)
}

// Invalidate is a no-op: the component keeps no derived state, and the container invalidates every child while it renders after a theme change (tui.ts invalidate), so requesting a render here would re-enter the renderer.
func (c *specialLinesComponent) Invalidate() {}

// repaint asks the host to draw the component again, as installing new content does.
func (c *specialLinesComponent) repaint() {
	if c.invalidate != nil {
		c.invalidate()
	}
}

func (c *specialLinesComponent) SetLines(lines []string) { c.SetLinesAt(lines, 0) }

// SetView draws the component from an extension's view surface (D107):
// an authoritative view (width 0) or one annotating lines laid out at width.
func (c *specialLinesComponent) SetView(view extension.ViewSurface, width int) {
	c.mu.Lock()
	c.render, c.mouse, c.click, c.lines = nil, nil, nil, nil
	c.view = view
	c.linesWidth = width
	c.mu.Unlock()
	if c.invalidate != nil {
		c.invalidate()
	}
}

// FrontendView reports the view's structure to a D91 frontend (D107).
func (c *specialLinesComponent) FrontendView(width int) *frontend.View {
	c.mu.RLock()
	view, linesWidth := c.view, c.linesWidth
	c.mu.RUnlock()
	if view == nil || (linesWidth > 0 && linesWidth != width) {
		return nil
	}
	return view.FrontendView(width)
}

// SetComponent installs a component the extension built: a frame the extension process rendered keeps the width it was rendered at; any other
// component renders at the width it is asked for.
func (c *specialLinesComponent) SetComponent(component tui.Component) {
	if framed, ok := component.(extension.FramedView); ok {
		c.SetView(framed.View, framed.Width)
		return
	}
	if frame, ok := component.(extension.WidthLines); ok {
		c.SetLinesAt(frame.Lines, frame.Width)
		return
	}
	c.SetRenderer(component.Render)
}

// SetLinesAt installs lines rendered at width (0 when unknown).
func (c *specialLinesComponent) SetLinesAt(lines []string, width int) {
	c.mu.Lock()
	c.render = nil
	c.mouse = nil
	c.click = nil
	c.view = nil
	c.linesWidth = width
	if len(lines) == 0 {
		c.lines = nil
	} else {
		c.lines = append([]string(nil), lines...)
	}
	c.mu.Unlock()
	if c.invalidate != nil {
		c.invalidate()
	}
}

// HasContent reports whether the component has lines or a renderer set.
func (c *specialLinesComponent) HasContent() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.render != nil || len(c.lines) > 0 || c.view != nil
}

func (c *specialLinesComponent) SetRenderer(render func(width int) []string) {
	c.SetRendererWithMouse(render, nil, nil)
}

// SetRendererWithMouse installs a renderer, the mouse handler of the component it draws, and the part of its lines that
// takes a click (nil for none).
func (c *specialLinesComponent) SetRendererWithMouse(render func(width int) []string, mouse func(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult, click func() (frontend.Area, bool)) {
	c.mu.Lock()
	c.lines = nil
	c.view = nil
	c.render = render
	c.mouse = mouse
	c.click = click
	c.mu.Unlock()
	if c.invalidate != nil {
		c.invalidate()
	}
}

// ClickArea reports the part of the lines last rendered that takes a click.
// pig additive (D91): a frontend session reports a click there.
func (c *specialLinesComponent) ClickArea() (frontend.Area, bool) {
	c.mu.RLock()
	click := c.click
	c.mu.RUnlock()
	if click == nil {
		return frontend.Area{}, false
	}
	return click()
}

// HandleMouse forwards an event to the installed mouse handler, if any.
func (c *specialLinesComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	c.mu.RLock()
	mouse := c.mouse
	c.mu.RUnlock()
	if mouse == nil {
		return nil
	}
	return mouse(event)
}

var _ extension.UIContext = (*ExtUIContext)(nil)

type extensionDialogResult struct {
	value     string
	cancelled bool
	err       error
}

// ReportsDialogInitiation reports that Select, Confirm, Input, and Editor mark
// their call initiated once the dialog is queued for installation.
func (u *ExtUIContext) ReportsDialogInitiation() bool { return true }

// dialogCountdown is a dialog's timeout option. As upstream's CountdownTimer
// does, tick shows the seconds left and expire cancels the dialog.
type dialogCountdown struct {
	timeout time.Duration
	// start runs the component's own countdown (StartCountdown) and dispose stops it (Dispose).
	start   func(timeout time.Duration, dispatch func(func()), onTick, onExpire func())
	dispose func()
}

// dialogTimeout reads ExtensionUIDialogOptions.timeout. Upstream's interactive
// dialogs count down only for a positive number of milliseconds
// (extension-selector.ts:56, extension-input.ts:60).
func dialogTimeout(opts extension.ExtensionUIDialogOptions) time.Duration {
	if opts.Timeout == nil || !(*opts.Timeout > 0) {
		return 0
	}
	timeout := *opts.Timeout
	// The countdown shows whole seconds; a timeout beyond Duration's range
	// counts down from its largest.
	longest := time.Duration(math.MaxInt64) - time.Second
	if timeout >= float64(longest/time.Millisecond) {
		return longest
	}
	return time.Duration(timeout * float64(time.Millisecond))
}

func (u *ExtUIContext) runDialog(
	ctx context.Context,
	component tui.Component,
	handle func(string),
	result func() (value string, done, cancelled bool),
	countdown dialogCountdown,
	blocked BlockedStatus,
) (string, error) {
	if u.m.layout == nil || u.m.tuiInst == nil {
		return "", fmt.Errorf("no TUI available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The dialog's context ends when it returns, which disposes its countdown.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if u.m.runCtx != nil {
		stop := context.AfterFunc(u.m.runCtx, cancel)
		defer stop()
	}

	resultCh := make(chan extensionDialogResult, 1)
	u.m.runOnMain(ctx, func() {
		if err := ctx.Err(); err != nil {
			resultCh <- extensionDialogResult{cancelled: true}
			return
		}
		if u.m.extensionDialog != nil {
			resultCh <- extensionDialogResult{err: errors.New("another extension dialog is already open")}
			return
		}

		finish := func() {
			value, done, cancelled := result()
			if !done {
				u.m.tuiInst.Render()
				return
			}
			if countdown.dispose != nil {
				countdown.dispose()
			}
			u.m.extensionDialog = nil
			u.m.programStatusReporter().SetBlocked("extension-dialog", nil)
			u.m.editorContainer.SetChildren(u.m.editor)
			u.m.setExtensionDialogViewMode(false, 0)
			u.m.tuiInst.RequestRender()
			resultCh <- extensionDialogResult{value: value, cancelled: cancelled}
		}
		u.m.setExtensionDialogViewMode(true, u.m.extensionDialogLines(component))
		u.m.editorContainer.SetChildren(component)
		u.m.extensionDialog = &extensionDialog{component: component}
		// Extension dialogs share the editor slot: opening one replaces the status of a displaced one (interactive-mode.ts:2729).
		u.m.programStatusReporter().SetBlocked("extension-dialog", &blocked)
		u.m.extensionDialog.handle = func(data string) {
			handle(data)
			finish()
		}
		if countdown.timeout > 0 {
			// Each second runs on this loop, so expiry replaces the dialog
			// before any frame shows a zero countdown.
			countdown.start(countdown.timeout, func(second func()) {
				_ = u.m.postToMain(ctx, second)
			}, func() { u.m.requestRender() }, finish)
			context.AfterFunc(ctx, countdown.dispose)
		}
		u.m.tuiInst.Render()
	})
	// The main loop installs the dialog before any task queued after this
	// one, so a later extension call applies after it, as upstream's
	// synchronous showExtensionSelector does.
	extension.CallInitiated(ctx)

	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", res.err
		}
		if res.cancelled {
			return "", context.Canceled
		}
		return res.value, nil
	case <-ctx.Done():
		cleanupCtx := u.m.runCtx
		if cleanupCtx == nil {
			cleanupCtx = context.Background()
		}
		u.m.runOnMain(cleanupCtx, func() {
			if u.m.extensionDialog == nil || u.m.extensionDialog.component != component {
				return
			}
			u.m.extensionDialog = nil
			u.m.programStatusReporter().SetBlocked("extension-dialog", nil)
			u.m.editorContainer.SetChildren(u.m.editor)
			u.m.setExtensionDialogViewMode(false, 0)
			u.m.tuiInst.RequestRender()
		})
		return "", ctx.Err()
	}
}

// ─── Interactive dialogs ─────────────────────────────────────────────────────

// Select shows a generic option list in the editor slot. Blocks until
// the user picks an option or cancels (Esc). Mirrors upstream's
// showExtensionSelector in interactive-mode.ts using the
// standalone ExtensionSelectorComponent (NO filter input, fixed
// title row, fixed hint row). A positive opts timeout counts down in the
// title and cancels the selector when it expires.
func (u *ExtUIContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	return u.selectWithStatus(ctx, title, options, opts, BlockedStatus{Kind: tui.ProgramStatusKindQuestion, Message: title})
}

// selectWithStatus is showExtensionSelector(title, options, opts, blocked): blocked is what the program reports while the selector waits.
func (u *ExtUIContext) selectWithStatus(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions, blocked BlockedStatus) (string, error) {
	sel := tui.NewExtensionSelectorComponent(title, options, nil, nil, tui.ExtensionSelectorOptions{OnToggleToolsExpanded: u.m.toggleAllTools})
	return u.runDialog(ctx, sel, sel.HandleInput, func() (string, bool, bool) {
		return sel.SelectedValue(), sel.Done(), sel.Cancelled()
	}, dialogCountdown{timeout: dialogTimeout(opts), start: sel.StartCountdown, dispose: sel.Dispose}, blocked)
}

// Confirm shows a Yes/No selector. Mirrors upstream's
// showExtensionConfirm (interactive-mode.ts:1996-2003).
func (u *ExtUIContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	prompt := title
	if message != "" {
		prompt = title + "\n" + message
	}
	result, err := u.selectWithStatus(ctx, prompt, []string{"Yes", "No"}, opts, BlockedStatus{Kind: tui.ProgramStatusKindPermission, Message: title})
	if err != nil {
		return false, err
	}
	return result == "Yes", nil
}

// Input shows a text input in the editor slot. Blocks until the user
// submits (Enter) or cancels (Esc). Mirrors upstream's
// showExtensionInput in interactive-mode.ts. A positive opts timeout counts
// down in the title and cancels the input when it expires.
func (u *ExtUIContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	input := tui.NewExtensionInputComponent(title, placeholder, nil, nil)
	return u.runDialog(ctx, input, input.HandleInput, func() (string, bool, bool) {
		return input.Text(), input.Done(), input.Cancelled()
	}, dialogCountdown{timeout: dialogTimeout(opts), start: input.StartCountdown, dispose: input.Dispose}, BlockedStatus{Kind: tui.ProgramStatusKindQuestion, Message: title})
}

// Editor shows a multi-line editor in the editor slot. Mirrors upstream's
// showExtensionEditor in interactive-mode.ts, which creates an
// ExtensionEditorComponent and swaps it into the editorContainer.
func (u *ExtUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	var value string
	var done, cancelled bool
	ed := NewExtensionEditorComponent(u.m.tuiInst, u.m.keybindings, title, prefill,
		func(text string) { value, done = text, true },
		func() { done, cancelled = true, true },
		nil, u.m.externalEditorCommand())
	ed.SetFocused(true)
	ed.SetExternalEditor(func(command, content string, apply func(string)) {
		u.m.openExternalEditorBuffer(ctx, command, content, apply)
	})
	return u.runDialog(ctx, ed, ed.HandleInput, func() (string, bool, bool) {
		return value, done, cancelled
	}, dialogCountdown{}, BlockedStatus{Kind: tui.ProgramStatusKindQuestion, Message: title})
}

// ─── Notifications & Status ──────────────────────────────────────────────────

func (u *ExtUIContext) Notify(message, kind string) {
	u.m.runOnMain(u.m.runCtx, func() {
		u.m.showExtensionNotify(message, kind)
	})
}

// showExtensionNotify mirrors interactive-mode.ts showExtensionNotify.
func (m *InteractiveMode) showExtensionNotify(message, notifyType string) {
	switch notifyType {
	case "error":
		m.showError(message)
	case "warning":
		m.showWarning(message)
	default:
		m.showStatus(message)
	}
}

// SetStatus forwards a keyed status to the footer and requests a render, like
// upstream setExtensionStatus. Extensions call it from their own timers, so the
// render request is what paints it while the session is idle.
func (u *ExtUIContext) SetStatus(key, text string) {
	if u.m.statusLine != nil {
		u.m.statusLine.SetExtensionStatus(key, text)
	}
	if u.m.tuiInst != nil {
		u.m.requestRender()
	}
}

func (u *ExtUIContext) SetWorkingMessage(message string) {
	if u.m != nil {
		u.m.updateStatusOnOwner(func() { u.m.setWorkingMessage(message) })
	}
}

func (u *ExtUIContext) SetWorkingVisible(visible bool) {
	if u.m != nil {
		u.m.updateStatusOnOwner(func() { u.m.setWorkingVisible(visible) })
	}
}

func (u *ExtUIContext) SetWorkingIndicator(options extension.WorkingIndicatorOptions) {
	if u.m == nil {
		return
	}
	// The update runs later on the owner loop, so it takes its own copy of the caller's frames and interval.
	owned := extension.WorkingIndicatorOptions{}
	if options.Frames != nil {
		owned.Frames = new(slices.Clone(*options.Frames))
	}
	if options.IntervalMs != nil {
		owned.IntervalMs = new(*options.IntervalMs)
	}
	u.m.updateStatusOnOwner(func() { u.m.setWorkingIndicator(&owned) })
}

func (u *ExtUIContext) SetHiddenThinkingLabel(label string) {
	u.m.updateStatusOnOwner(func() { u.m.setHiddenThinkingLabel(label) })
}

func (u *ExtUIContext) SetFooter(factory extension.FooterFactory) {
	if u.m.extFooter == nil {
		return
	}
	// Upstream setExtensionFooter swaps the built-in footer for the
	// extension's component whatever it renders, so a frame with no lines
	// is an empty footer; only a cleared footer (nil) restores the built-in. Ownership changes before SetLinesAt, whose invalidation callback can paint synchronously.
	// upstream: interactive-mode.ts:2530 setExtensionFooter disposes the existing custom footer first.
	u.m.extComponentMu.Lock()
	previous := u.m.extFooterComponent
	u.m.extFooterComponent = nil
	u.m.extComponentMu.Unlock()
	if previous != nil {
		previous.Dispose()
	}
	if factory != nil {
		var footerData extension.ReadonlyFooterDataProvider
		if u.m.statusLine != nil {
			footerData = u.m.statusLine.FooterDataProvider
		}
		// upstream: interactive-mode.ts setExtensionFooter calls the factory with (this.ui, theme, this.footerDataProvider).
		component := factory(u.m.tuiInst, tui.ActiveTheme(), footerData)
		if component != nil {
			u.m.extComponentMu.Lock()
			u.m.extFooterComponent = component
			u.m.extComponentMu.Unlock()
			if u.m.statusLine != nil {
				u.m.statusLine.SetSuppressedByExtFooter(true)
			}
			u.m.extFooter.SetComponent(component)
			return
		}
	}
	if u.m.statusLine != nil {
		u.m.statusLine.SetSuppressedByExtFooter(false)
	}
	u.m.extFooter.SetLines(nil)
}

func (u *ExtUIContext) SetHeader(factory extension.HeaderFactory) {
	if u.m.extHeader == nil {
		return
	}
	// upstream: interactive-mode.ts:2558 setExtensionHeader disposes the existing custom header first.
	u.m.extComponentMu.Lock()
	previous := u.m.extHeaderComponent
	u.m.extHeaderComponent = nil
	u.m.extComponentMu.Unlock()
	if previous != nil {
		previous.Dispose()
	}
	if factory != nil {
		// upstream: interactive-mode.ts setExtensionHeader calls the factory with (this.ui, theme).
		if component := factory(u.m.tuiInst, tui.ActiveTheme()); component != nil {
			u.m.extComponentMu.Lock()
			u.m.extHeaderComponent = component
			u.m.extComponentMu.Unlock()
			u.m.extHeader.SetComponent(component)
			return
		}
	}
	u.m.restoreBuiltInHeader()
}

func (u *ExtUIContext) SetLogin(definition extension.LoginDefinition) error {
	validated, err := extension.ValidateLoginDefinition(definition)
	if err != nil {
		return err
	}
	if u.m.extHeader == nil {
		return extension.ErrUIUnavailable
	}
	if !u.m.opts.LoginVisible {
		u.m.extHeader.SetLines(nil)
		return nil
	}
	options := u.m.opts.LoginHeaderOptions
	options.ColorOverrides = nil
	renderer := newLoginHeaderRenderer(validated, options)
	u.m.extHeader.SetRenderer(renderer.Render)
	return nil
}

var _ extension.SpriteRegistrar = (*ExtUIContext)(nil)

// RegisterSprite adds an extension's sprite to /sprite (D2). The header redraws, since it may be the saved sprite, drawn
// as the default until now.
func (u *ExtUIContext) RegisterSprite(owner string, definition extension.ValidatedSpriteDefinition) error {
	u.m.spriteOwnersMu.Lock()
	err := piglogin.Register(owner, definition)
	if err == nil {
		if u.m.spriteOwners == nil {
			u.m.spriteOwners = map[string]struct{}{}
		}
		u.m.spriteOwners[owner] = struct{}{}
	}
	u.m.spriteOwnersMu.Unlock()
	if err != nil {
		return err
	}
	u.m.invalidateBuiltInHeader()
	return nil
}

// UnregisterSprites removes the sprites of an extension that unloaded; the header redraws in case it showed one.
func (u *ExtUIContext) UnregisterSprites(owner string) {
	u.m.spriteOwnersMu.Lock()
	piglogin.Unregister(owner)
	delete(u.m.spriteOwners, owner)
	u.m.spriteOwnersMu.Unlock()
	u.m.invalidateBuiltInHeader()
}

// dropBoundSprites removes the sprites of the build whose bridge was bound, before another build's bridge replays its own,
// and reports whether it removed any. The outgoing bridge is detached by then, so its host's shutdown no longer reaches the
// UI to remove them.
// pig divergence (D2): extension sprites leave /sprite with the build that registered them; Pi has no sprites.
func (m *InteractiveMode) dropBoundSprites() bool {
	m.spriteOwnersMu.Lock()
	defer m.spriteOwnersMu.Unlock()
	for owner := range m.spriteOwners {
		piglogin.Unregister(owner)
	}
	dropped := len(m.spriteOwners) > 0
	m.spriteOwners = nil
	return dropped
}

// invalidateBuiltInHeader redraws the header slot, which draws the active sprite while it shows the built-in header.
func (m *InteractiveMode) invalidateBuiltInHeader() {
	if m.extHeader != nil {
		m.extHeader.repaint()
	}
}

func (m *InteractiveMode) restoreBuiltInHeader() {
	m.toolMu.Lock()
	expanded := m.toolsExpanded
	m.toolMu.Unlock()
	m.setBuiltInHeader(expanded)
}

func (m *InteractiveMode) setBuiltInHeader(expanded bool) {
	if m.extHeader == nil {
		return
	}
	if !m.opts.LoginVisible {
		m.extHeader.SetLines(nil)
		return
	}
	if m.opts.BuiltInHeaderLines != nil {
		m.extHeader.SetLines(m.opts.BuiltInHeaderLines)
		return
	}
	showDetails := m.shouldShowStartupDetails()
	m.toolMu.Lock()
	m.builtInHeaderExpanded = expanded
	if !m.builtInHeaderBuilt {
		m.builtInHeaderShowDetails, m.builtInHeaderBuilt = showDetails, true
	}
	m.toolMu.Unlock()
	// pig divergence (D2): the built-in header draws the saved PiG sprite. Reading it here, off the render loop, picks up a
	// selection a game or another PiG wrote since startup (/reload restores the built-in header).
	piglogin.Refresh()
	piglogin.Active()
	m.extHeader.SetRendererWithMouse(m.renderBuiltInHeader, m.handleBuiltInHeaderMouse, m.builtInHeaderClickArea)
}

func (u *ExtUIContext) SetTitle(title string) {
	if u.m.tuiInst != nil {
		_, _ = fmt.Fprintf(os.Stdout, "\033]0;%s\007", title)
	}
}

// RunRemoteOverlay mounts remote custom UI as an overlay or editor-slot replacement and waits for its result. The owner loop mounts the component and transfers renderer focus; this caller drains its modal input lease off-loop. Normal completion restores the editor slot and focus before returning.
func (u *ExtUIContext) RunRemoteOverlay(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	if u.m.tuiInst == nil {
		return nil, false
	}

	invalidateOverlay := func() {
		if u.m.runCtx == nil {
			u.m.requestRender()
			return
		}
		u.m.postUITask(func() { u.m.tuiInst.Render() })
	}
	overlay := newCustomOverlay(invalidateOverlay)
	overlay.terminalWidth = u.m.tuiInst.Width
	if host != nil {
		overlay.SetOnInput(host.OnInput)
		if mouse, ok := host.(extension.RemoteOverlayMouseHost); ok {
			overlay.SetOnMouse(mouse.OnMouse)
		}
	}
	defer overlay.setInputActive(u.m, false)

	ctx := u.m.runCtx
	runOnOwner := u.m.runOnOwner
	if ctx == nil {
		ctx = context.Background()
		runOnOwner = func(_ context.Context, fn func()) { fn() }
	}
	setup := make(chan func(), 1)
	runOnOwner(ctx, func() {
		var cleanup func()
		if opts.Overlay || u.m.layout == nil {
			h := u.m.tuiInst.ShowOverlay(overlay, remoteOverlayTUIOptions(opts))
			u.bindOverlayControls(overlay, h, runOnOwner)
			cleanup = h.Close
		} else {
			// Match upstream ctx.ui.custom(): replace the editor slot directly
			// rather than wrapping the component in a second modal shell.
			u.m.editorContainer.SetChildren(overlay)
			// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:showExtensionCustom
			u.m.tuiInst.SetFocus(overlay)
			overlay.setInputActive(u.m, true)
			cleanup = func() {
				u.m.editorContainer.SetChildren(u.m.editor)
				u.m.tuiInst.SetFocus(u.m.editor)
			}
		}
		setup <- cleanup
	})
	var cleanup func()
	select {
	case cleanup = <-setup:
	case <-ctx.Done():
		return nil, false
	}
	defer func() {
		done := make(chan struct{})
		runOnOwner(ctx, func() {
			cleanup()
			u.m.tuiInst.Render()
			close(done)
		})
		select {
		case <-done:
		case <-ctx.Done():
		}
	}()

	if onHandle != nil {
		onHandle(overlay)
	}
	runOnOwner(ctx, func() { u.m.tuiInst.Render() })

	for !overlay.Done() {
		inputCh, changed := overlay.inputRoute()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, false
		case buf := <-inputCh:
			data := string(buf)
			// Theme replies and fullscreen viewport keys precede the component, as for every selector; both run on the owner loop.
			consumed := make(chan bool, 1)
			runOnOwner(ctx, func() { consumed <- u.m.consumeModalHostInput(data) })
			select {
			case handled := <-consumed:
				if handled {
					continue
				}
			case <-ctx.Done():
				return nil, false
			}
			dispatchModalInput(overlay, []string{data}, overlay.HandleInput, overlay.Done)
		case <-overlay.Closed():
			// The extension closed its own overlay. Without this the loop stays
			// parked on inputCh and the caller only unblocks when the user
			// presses an unrelated key.
		}
	}
	return overlay.Result(), true
}

// ─── Editor access ───────────────────────────────────────────────────────────

func (u *ExtUIContext) PasteToEditor(text string) {
	if u.m.editor != nil {
		// Simulate bracketed paste per upstream
		u.m.editor.HandleInput("\x1b[200~" + text + "\x1b[201~")
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
	}
}

func (u *ExtUIContext) SetEditorText(text string) {
	if u.m.editor != nil {
		u.m.editor.SetText(text)
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
	}
}

// GetEditorText reads owner-published expanded text after binding, including paste contents. Before binding the caller owns the editor directly.
func (u *ExtUIContext) GetEditorText() string {
	if snapshot := u.m.editorSnapshot.Load(); snapshot != nil {
		return *snapshot
	}
	if u.m.editor != nil {
		return u.m.editor.GetExpandedText()
	}
	return ""
}

// ─── Terminal input ──────────────────────────────────────────────────────────

func (u *ExtUIContext) OnTerminalInput(handler extension.TerminalInputHandler) func() {
	if u.m == nil {
		return func() {}
	}
	return u.m.addTerminalInputHandler(handler)
}

// OnRemoteTerminalInput registers a subprocess extension's listener. The
// input loop asks it off the loop and applies its verdict in input order.
func (u *ExtUIContext) OnRemoteTerminalInput(extensionName string, handler extension.RemoteTerminalInputHandler) func() {
	if u.m == nil {
		return func() {}
	}
	return u.m.addRemoteTerminalInputHandler(extensionName, handler)
}

// ─── Autocomplete ────────────────────────────────────────────────────────────

// SetEditorComponent installs the component factory builds in place of the editor, or with nil restores the editor
// (interactive-mode.ts setCustomEditorComponent). The factory is called on the owner loop with the TUI, the editor theme and the keybindings.
func (u *ExtUIContext) SetEditorComponent(factory extension.EditorFactory) {
	// interactive-mode.ts:2876 records the factory first, so getEditorComponent answers the new factory at once; the owner loop installs it.
	u.m.extComponentMu.Lock()
	u.m.editorFactory = factory
	u.m.extComponentMu.Unlock()
	u.m.runOnMain(u.m.backgroundCtx, func() { u.m.setCustomEditorComponent(factory) })
}

// GetEditorComponent is the factory the last SetEditorComponent installed (interactive-mode.ts:2664 `this.editorComponentFactory`).
func (u *ExtUIContext) GetEditorComponent() extension.EditorFactory {
	u.m.extComponentMu.Lock()
	defer u.m.extComponentMu.Unlock()
	return u.m.editorFactory
}

// ─── Theme ───────────────────────────────────────────────────────────────────

func (u *ExtUIContext) Theme() extension.Theme { return ActiveExtensionTheme() }

// ActiveExtensionTheme is the active theme as extensions see it
// (ctx.ui.theme): upstream hands every mode's UI context the global theme.
func ActiveExtensionTheme() extension.Theme {
	theme := tui.ActiveTheme()
	if theme == nil {
		return nil
	}
	return ExtensionThemePalette(theme)
}

// ExtensionThemePalette is the palette extensions receive for theme (ctx.ui.theme): its resolved escape sequences, and the host-resolved appearance and concrete colors.
func ExtensionThemePalette(theme *tui.Theme) extension.Theme {
	foregrounds, backgrounds := theme.ANSIPalette()
	palette := map[string]any{
		"name":        theme.Name,
		"foregrounds": foregrounds,
		"backgrounds": backgrounds,
		// modifiers is whether theme.bold and the other chalk styles draw,
		// as upstream's chalk decides from its own stdout.
		"modifiers": chalkModifiersEnabled(),
		// mode is upstream Theme.getColorMode(): the mode the theme's own escape sequences are built for, which style draws a concrete color in.
		"mode": string(theme.GetColorMode()),
		// appearance and colors are upstream Theme.appearance and Theme.colors (theme.ts:311-336). The host resolves them: they depend on the terminal's reported colors, which the extension process cannot see.
		"appearance": string(theme.Appearance()),
		"colors":     extensionThemeColors(theme.Colors()),
	}
	// sourcePath is upstream Theme.sourcePath: the file a custom theme was
	// loaded from, absent for built-in themes.
	if reg := tui.ActiveThemeRegistry(); reg != nil {
		if path := reg.PathOf(theme.Name); path != "" {
			palette["sourcePath"] = path
		}
	}
	return palette
}

// extensionThemeColors is the wire form of Theme.colors: each token's pi-tui Color as {kind: "indexed", index}, {kind: "rgb", r, g, b} or {kind: "oklch", l, c, h}.
// A color with a non-finite channel is left out: Pi's rgbColor and oklchColor reject one (colors.ts:58-88), and JSON cannot carry it. A terminal reply can produce one (terminal-colors.ts:27-36 divides Infinity by Infinity for a very long channel).
func extensionThemeColors(values map[string]tui.Color) map[string]any {
	finite := func(channels ...float64) bool {
		for _, channel := range channels {
			if math.IsNaN(channel) || math.IsInf(channel, 0) {
				return false
			}
		}
		return true
	}
	colors := make(map[string]any, len(values))
	for token, color := range values {
		switch color := color.(type) {
		case tui.IndexedColor:
			colors[token] = map[string]any{"kind": "indexed", "index": color.Index}
		case tui.RgbColorValue:
			if finite(color.R, color.G, color.B) {
				colors[token] = map[string]any{"kind": "rgb", "r": color.R, "g": color.G, "b": color.B}
			}
		case tui.OklchColorValue:
			if finite(color.L, color.C, color.H) {
				colors[token] = map[string]any{"kind": "oklch", "l": color.L, "c": color.C, "h": color.H}
			}
		}
	}
	return colors
}

// GetAllThemes lists the themes the user can switch to, mirroring upstream's
// getAllThemes (getAvailableThemesWithPaths). Path is empty for built-in
// themes, matching upstream's `path: string | undefined`.
func (u *ExtUIContext) GetAllThemes() []extension.ThemeMeta {
	reg := tui.ActiveThemeRegistry()
	if reg == nil {
		return nil
	}
	names := reg.Names()
	metas := make([]extension.ThemeMeta, 0, len(names))
	for _, name := range names {
		metas = append(metas, extension.ThemeMeta{Name: name, Path: reg.PathOf(name)})
	}
	return metas
}

// GetTheme loads a theme's portable palette without selecting it. Missing names return absence, as Pi's getThemeByName does. The theme is in the terminal's color mode, as Pi's createTheme defaults it (theme.ts:599-600).
func (u *ExtUIContext) GetTheme(name string) (extension.Theme, error) {
	reg := tui.ActiveThemeRegistry()
	if reg == nil {
		return nil, nil
	}
	theme := reg.Get(name)
	if theme == nil {
		return nil, nil
	}
	return ExtensionThemePalette(theme.WithColorMode(tui.GetTerminalColorMode())), nil
}

// SetTheme disables automatic switching, applies a named theme, and persists a successful choice.
// An unknown name falls back to the system theme and returns failure without changing the stored selection (theme.ts setTheme).
func (u *ExtUIContext) SetTheme(selection extension.ThemeSelection) extension.SetThemeResult {
	if u.m == nil {
		return extension.SetThemeResult{Success: false, Error: "no interactive UI is active"}
	}
	var name string
	switch selection := selection.(type) {
	case extension.ThemeName:
		name = string(selection)
	case extension.ThemeInstance:
		// upstream: interactive-mode.ts:2670 `themeOrName instanceof Theme` -> themeController.setThemeInstance
		if selection.Theme == nil {
			return extension.SetThemeResult{Success: false, Error: "expected a theme"}
		}
		core := u.m.theme()
		core.renderer = func() tui.TUI { return ownerInvalidatingRenderer{TUI: u.m.tuiInst, m: u.m} }
		core.setThemeInstance(selection.Theme)
		return extension.SetThemeResult{Success: true}
	default:
		return extension.SetThemeResult{Success: false, Error: "expected theme name string"}
	}
	// upstream 0.99.1 interactive-mode.ts:2575-2586 extension setTheme: select by name, and persist only a theme that loaded.
	core := u.m.theme()
	// An extension calls off the owner loop, which alone may touch the renderer.
	core.renderer = func() tui.TUI { return ownerInvalidatingRenderer{TUI: u.m.tuiInst, m: u.m} }
	if err := core.setThemeName(name, false); err != nil {
		return extension.SetThemeResult{Success: false, Error: err.Error()}
	}
	if u.m.opts.SettingsManager != nil && u.m.opts.Settings.Theme != name {
		if err := u.m.opts.SettingsManager.UpdateGlobal(func(gs *Settings) {
			gs.Theme = name
		}); err != nil {
			return extension.SetThemeResult{Success: false, Error: err.Error()}
		}
		u.m.opts.Settings.Theme = name
	}
	return extension.SetThemeResult{Success: true}
}

// ─── Tools state ─────────────────────────────────────────────────────────────

func (u *ExtUIContext) GetToolsExpanded() bool {
	u.m.toolMu.Lock()
	defer u.m.toolMu.Unlock()
	return u.m.toolsExpanded
}

func (u *ExtUIContext) SetToolsExpanded(expanded bool) {
	apply := func() {
		u.m.setAllToolsExpanded(expanded)
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
	}
	if u.m.runCtx == nil {
		apply()
		return
	}
	u.m.runOnMain(u.m.runCtx, apply)
}

// ownerInvalidatingRenderer runs Invalidate on the owner loop and requests the render an extension's theme change needs.
type ownerInvalidatingRenderer struct {
	tui.TUI
	m *InteractiveMode
}

func (r ownerInvalidatingRenderer) Invalidate() {
	if r.TUI == nil {
		return
	}
	// The embedded renderer, not this wrapper: the wrapper's own Invalidate would recurse.
	base := r.TUI
	invalidate := func() { base.Invalidate(); base.RequestRender() }
	if r.m.runCtx == nil {
		invalidate()
		return
	}
	r.m.runOnMain(r.m.runCtx, invalidate)
}
