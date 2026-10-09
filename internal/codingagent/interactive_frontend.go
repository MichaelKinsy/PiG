package codingagent

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D91): a Piglet frontend member can draw the interactive
// mode in place of Pi's ANSI renderer. Pi has no such member; with none
// fused, nothing here runs and the ANSI path is unchanged.

// holdProgramStatusForFrontend runs before raw mode starts. A session that
// may draw the run is the terminal that shows Pi's program status (its
// renderer's Terminal reports to it), so raw mode sends no OSC 7501 support
// query and the process terminal reports nothing; openFrontend hands the
// status back to the process terminal if no session takes the run.
func (m *InteractiveMode) holdProgramStatusForFrontend() {
	if m.opts.Frontend != nil {
		tui.SetProgramStatusElsewhere(true)
	}
}

// openFrontend offers the run to the fused frontend after raw mode is on, as
// a terminal protocol handshake requires, and before the first paint. A
// frontend that takes the run replaces the renderer that has not painted yet.
func (m *InteractiveMode) openFrontend() {
	if m.opts.Frontend == nil {
		return
	}
	var out io.Writer = os.Stdout
	if m.rendererOut != nil {
		out = m.rendererOut
	}
	// The session writes from its own goroutines too, and PiG writes its
	// terminal queries while it draws: each Write reaches the terminal whole.
	out = &frontendTerminal{out: out}
	// The first fallback request names the reason; later ones, such as each
	// failed frame after it, change nothing.
	var leaving atomic.Bool
	leave := func(reason string) {
		if !leaving.Swap(true) {
			m.leaveFrontendAsync(reason)
		}
	}
	session, err := m.opts.Frontend.Open(frontend.Env{
		Out:      out,
		Columns:  m.tuiInst.Width(),
		Getenv:   os.Getenv,
		AppName:  AppName,
		Version:  m.opts.AppVersion,
		Fallback: leave,
		Edit: func(edit frontend.Edit) {
			m.postFrontendAction(func() { m.applyFrontendEdit(edit) })
		},
		Undo: func() { m.postFrontendAction(func() { m.editor.Undo() }) },
		Send: func(text string) {
			m.postFrontendAction(func() { m.sendFrontendPrompt(m.runCtx, text) })
		},
		Act: m.postFrontendAct,
		// pig additive (D91): a click on a node's Click area reaches the
		// component that drew it on the owner loop.
		Click: func(click frontend.Click) {
			m.postFrontendAction(func() { m.surface.Click(click) })
		},
	})
	if err != nil || session == nil {
		// No session draws: the process terminal shows the program status,
		// and queries the terminal for it now.
		tui.SetProgramStatusElsewhere(false)
	}
	if err != nil {
		m.frontendStartupWarning = fmt.Sprintf("Native rendering is unavailable: %v", err)
		return
	}
	if session == nil {
		return
	}
	onApplyError := func(err error) { leave(fmt.Sprintf("drawing failed: %v", err)) }
	var surface *tui.TuiSurface
	if m.rendererOut != nil {
		surface = tui.NewTuiSurfaceWithSize(session, m.tuiInst.Width(), m.tuiInst.Height(), onApplyError)
	} else {
		surface = tui.NewTuiSurface(session, onApplyError)
	}
	surface.SetHooks(m.frontendSurfaceHooks())
	// The theme reads the terminal's colors and light/dark reports as it does
	// under the ANSI renderer.
	surface.SetTerminalOut(out)
	m.rendererMu.Lock()
	defer m.rendererMu.Unlock()
	m.surface = surface
	// The surface replaces the renderer and keeps its debug key, as a renderer switch carries onDebug (interactive-mode.ts:900, :920).
	surface.SetOnDebug(m.tuiInst.OnDebug())
	m.tuiInst = surface
	// The fullscreen renderer built for this run never starts.
	m.altScreen = nil
	m.applyBaseRendererWiring()
	m.reportFrontend()
}

// reportFrontend tells the extensions whether a frontend session draws, so
// Node derives views and the SDKs send frontend-only annotations only then
// (D107).
func (m *InteractiveMode) reportFrontend() {
	if bridge := m.opts.SubprocessUIBridge; bridge != nil {
		bridge.SetFrontend(m.surface != nil)
	}
}

// frontendTerminal is the terminal while a frontend session draws: the
// session's writes and PiG's own, one Write at a time. A write larger than
// the terminal's output buffer can otherwise take turns with another one in
// the kernel, and a query inside a frame would break both.
type frontendTerminal struct {
	mu  sync.Mutex
	out io.Writer
}

func (t *frontendTerminal) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.Write(data)
}

// frontendSurfaceHooks supply the session state a frontend node carries
// beyond the component tree. They run where the surface renders, on the
// owner loop that also handles agent events.
func (m *InteractiveMode) frontendSurfaceHooks() tui.SurfaceHooks {
	return tui.SurfaceHooks{
		Streaming: func() *tui.AssistantMessageComponent {
			if !m.hasActiveAgentTurn() {
				return nil
			}
			return m.evCurrentBlock
		},
		ToolDiff:       toolResultDiff,
		Editor:         func() *tui.Editor { return m.editor },
		EditorSendable: m.frontendEditorSendable,
		Working:        m.frontendWorking,
		Footer: func(c tui.Component) (frontend.Footer, bool) {
			if m.statusLine == nil || c != tui.Component(m.statusLine) {
				return frontend.Footer{}, false
			}
			return m.statusLine.frontendFooter()
		},
		// Path is what GetSessionFile reports, without its allocation: ""
		// for an in-memory session.
		SessionFile: func() string {
			if session := m.currentSession(); session != nil {
				return session.Path()
			}
			return ""
		},
		Mark: m.frontendMark.read,
		// pig additive (D91): a click on an extension's view reaches its
		// component only in the mode where Pi's terminal reports the mouse.
		Mouse: func() bool { return m.opts.TuiMode == "fullscreen" },
	}
}

// frontendMarkCache keeps PiG's mark, the pig head of the active sprite, with
// the sprite and the color mode it was built from, so a frame rebuilds it
// only after one of them changed.
type frontendMarkCache struct {
	variant piglogin.Variant
	mode    tui.TerminalColorMode
	mark    frontend.Mark
	built   bool
}

// read returns the mark of the sprite the startup header draws now, in the
// active theme's color mode.
func (c *frontendMarkCache) read() frontend.Mark {
	variant, mode := piglogin.Active(), tui.ActiveTheme().GetColorMode()
	if !c.built || mode != c.mode || !piglogin.SameHead(variant, c.variant) {
		c.variant, c.mode, c.mark, c.built = variant, mode, headMark(variant, mode), true
	}
	return c.mark
}

// headMark is the variant's pig head as HeadLines draws it in mode: its
// opaque pixels, each in the color its half block takes.
func headMark(variant piglogin.Variant, mode tui.TerminalColorMode) frontend.Mark {
	head := piglogin.HeadPixels(variant)
	pixels := make([]frontend.MarkPixel, len(head))
	for i, pixel := range head {
		pixels[i] = frontend.MarkPixel{X: pixel.X, Y: pixel.Y, Color: markPixelColor(pixel.Color, mode)}
	}
	return frontend.Mark{Width: piglogin.HeadWidth, Height: piglogin.HeadHeight, Pixels: pixels}
}

// markPixelColor is the color a terminal shows for value drawn in mode: the
// color itself in truecolor, and in 256-color mode the xterm color of the
// palette index the SGR sequence that HeadLines writes selects.
func markPixelColor(value color.RGBA, mode tui.TerminalColorMode) string {
	c, err := tui.NewRgbColor(float64(value.R), float64(value.G), float64(value.B))
	if err != nil {
		panic(err) // the channels are bytes, inside the 0..255 range NewRgbColor accepts
	}
	if mode == tui.TerminalColorModeTrueColor {
		return tui.ColorToHex(c)
	}
	sgr := tui.ForegroundAnsi(c, mode)
	index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(sgr, "\x1b[38;5;"), "m"))
	if err != nil {
		panic(fmt.Sprintf("256-color sequence %q", sgr))
	}
	return tui.ColorToHex(tui.IndexedColor{Index: index})
}

// frontendWorking reports the active status indicator, which the terminal
// renderer draws in the editor's top border or above the editor. Frames are
// an extension's only when it set its own with setWorkingIndicator.
func (m *InteractiveMode) frontendWorking() (frontend.Working, bool) {
	indicator := m.activeStatusIndicator
	if indicator == nil || indicator.Loader == nil {
		return frontend.Working{}, false
	}
	working := frontend.Working{
		Kind:     frontend.WorkingKind(indicator.Kind),
		Message:  indicator.Message,
		Interval: m.statusFrameInterval(),
	}
	if indicator.IndicatorVerbatim {
		working.Frames = indicator.Frames
	}
	return working, true
}

// frontendFooter reports the footer's data, and false while an extension's
// footer replaces it.
func (s *FooterComponent) frontendFooter() (frontend.Footer, bool) {
	totals, routed, snap := s.footerSources()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.suppressedByExtFooter {
		return frontend.Footer{}, false
	}
	return footerNode(s.footerDataLocked(totals, routed, snap)), true
}

// footerNode is the data renderFooter draws, without the layout: the provider
// only while more than one is available, and the thinking level only for a
// model that reasons.
func footerNode(d footerData) frontend.Footer {
	contextWindow, pct := footerContextUsage(d)
	model, provider, thinkingLevel := footerModel(d)
	if d.providerCount <= 1 {
		provider = ""
	}
	footer := frontend.Footer{
		Cwd:         footerCwd(d.cwd),
		GitBranch:   d.gitBranch,
		SessionName: d.sessionName,
		UsageTotals: frontend.UsageTotals{
			Input:      d.usage.input,
			Output:     d.usage.output,
			CacheRead:  d.usage.cacheRead,
			CacheWrite: d.usage.cacheWrite,
			Cost:       d.usage.cost,
		},
		LatestCacheHitRate: d.usage.latestCacheHitRate,
		UsingSubscription:  d.usingSubscription,
		ContextUsage: frontend.ContextUsage{
			Tokens:        d.contextTokens,
			Percent:       pct,
			Unknown:       d.contextUnknown,
			ContextWindow: contextWindow,
		},
		AutoCompact:       d.autoCompactEnabled,
		Experimental:      experimentalFeaturesEnabled(),
		Model:             model,
		Provider:          provider,
		ThinkingLevel:     thinkingLevel,
		ExtensionStatuses: footerStatusesWithText(d.extensionStatuses),
	}
	if d.routed != nil && d.routed.Model != nil {
		footer.Routed = frontend.RoutedModel{Model: d.routed.Model.ID, ThinkingLevel: string(d.routed.ThinkingLevel)}
	}
	return footer
}

// frontendEditorSendable reports whether a send submits now: the input loop
// runs, no exit is requested, and the editor has the keys, which a selector,
// an overlay, an extension dialog and the external editor take. Pi accepts
// prompts while the agent streams (steering) and while it compacts (queued),
// so neither clears it.
func (m *InteractiveMode) frontendEditorSendable() bool {
	return m.frontendInputRunning && !m.requestExit.Load() && !m.tuiTornDown &&
		m.extensionDialog == nil && !m.externalEditorActive &&
		m.tuiInst.GetFocusedComponent() == tui.Component(m.editor)
}

// postFrontendAction queues a session's editor action for the owner loop
// without blocking. frontendInput drains the queue once the session has
// handled an input sequence, so an action from HandleInput runs before the
// next key. An action runs only while the session draws, and repaints.
func (m *InteractiveMode) postFrontendAction(action func()) {
	m.frontendActions.post(m, func() {
		if m.surface == nil || m.tuiTornDown {
			return
		}
		action()
		m.tuiInst.RequestRender()
	})
}

// applyFrontendEdit applies a native edit computed against the editor's
// current text; one computed before keys still in flight changed it is stale
// and ignored.
func (m *InteractiveMode) applyFrontendEdit(edit frontend.Edit) {
	if edit.Len != jsstring.Length(m.editor.Text()) {
		return
	}
	m.editor.ApplyEdit(edit.From, edit.To, edit.Text, edit.Cursor)
}

// sendFrontendPrompt submits text as Enter submits the editor's text, without
// clearing the draft first.
func (m *InteractiveMode) sendFrontendPrompt(ctx context.Context, text string) {
	if text = widthx.JSTrim(text); text != "" {
		m.submit(ctx, text)
	}
}

// frontendActFeed performs a session's selector actions in the order they
// were posted, one at a time, on one background task.
type frontendActFeed struct {
	mu      sync.Mutex
	actions []frontend.Action
	running bool
}

// postFrontendAct queues a selector action without blocking.
func (m *InteractiveMode) postFrontendAct(action frontend.Action) {
	if m.tuiStopped.Load() {
		return
	}
	feed := &m.frontendActs
	feed.mu.Lock()
	feed.actions = append(feed.actions, action)
	start := !feed.running
	feed.running = true
	feed.mu.Unlock()
	if start {
		m.backgroundTasks.Go(m.feedFrontendActs)
	}
}

// feedFrontendActs performs the queued actions as keys. Each step's keys are
// computed on the owner loop against the selector as it is then, and routed
// like terminal input to whatever has the keys, the selector's own loop or
// the main loop, before the next step is computed. A step therefore sees
// the effect of the keys before it, and an action stops once its selector
// closes.
func (m *InteractiveMode) feedFrontendActs() {
	ctx := m.backgroundCtx
	feed := &m.frontendActs
	for {
		feed.mu.Lock()
		if len(feed.actions) == 0 || ctx == nil || ctx.Err() != nil {
			feed.actions, feed.running = nil, false
			feed.mu.Unlock()
			return
		}
		action := feed.actions[0]
		feed.actions = feed.actions[1:]
		feed.mu.Unlock()
		native := tui.NewNativeAction(action)
		for {
			keys, readCh := m.frontendActKeys(ctx, native)
			if len(keys) == 0 {
				break
			}
			for _, key := range keys {
				ticket := m.routeInputSequence(ctx, []byte(key), nil, readCh)
				if ticket != nil {
					select {
					case <-ticket.done:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}
}

// frontendActKeys computes an action's next keys on the owner loop, with the
// main loop's input channel, while a session draws and input runs.
func (m *InteractiveMode) frontendActKeys(ctx context.Context, native *tui.NativeAction) ([]string, chan<- inputChunk) {
	type step struct {
		keys   []string
		readCh chan<- inputChunk
	}
	result := make(chan step, 1)
	err := m.postToMain(ctx, func() {
		if m.surface == nil || m.tuiTornDown || m.inputReadCh == nil || !m.frontendInputRunning {
			result <- step{}
			return
		}
		result <- step{keys: m.surface.NativeKeys(native), readCh: m.inputReadCh}
	})
	if err != nil {
		return nil, nil
	}
	select {
	case s := <-result:
		return s.keys, s.readCh
	case <-ctx.Done():
		return nil, nil
	}
}

// toolResultDiff returns the unified patch a successful result's details
// carry: Pi's edit details, live or reloaded from the session as a map,
// and any tool's details with the same "patch" member.
func toolResultDiff(_ string, arguments map[string]any, result any) *frontend.Diff {
	r, ok := result.(agent.AgentToolResult)
	if !ok || r.IsError {
		return nil
	}
	var patch string
	switch details := detailsObject(r.Details).(type) {
	case *tools.EditToolDetails:
		if details != nil {
			patch = details.Patch
		}
	case map[string]any:
		patch, _ = details["patch"].(string)
	}
	if patch == "" {
		return nil
	}
	path, _ := arguments["path"].(string)
	if path == "" {
		path, _ = arguments["file_path"].(string)
	}
	return &frontend.Diff{Path: path, Text: patch}
}

// leaveFrontendAsync schedules leaveFrontend on the owner loop. It never
// blocks the caller, which may be the owner loop itself, and the background
// task ends when the run does.
func (m *InteractiveMode) leaveFrontendAsync(reason string) {
	ctx := m.backgroundCtx
	m.backgroundTasks.Go(func() {
		_ = m.postToMain(ctx, func() { m.leaveFrontend(reason) })
	})
}

// leaveFrontend closes the frontend session and continues the run with the
// configured ANSI renderer, repainting the whole component tree. Open
// overlays belong to the outgoing renderer and are closed.
func (m *InteractiveMode) leaveFrontend(reason string) {
	if m.surface == nil || m.tuiTornDown {
		return
	}
	m.rendererMu.Lock()
	surface := m.surface
	surface.Stop()
	closeErr := surface.Session().Close()
	for surface.HasOverlay() {
		surface.HideOverlay()
	}
	m.surface = nil
	m.teardownCurrentTui()
	m.createInteractiveTui(m.runCtx)
	m.rewireRenderer()
	m.mountInteractiveTui(true)
	m.tuiInst.Invalidate()
	m.tuiInst.Render()
	m.rendererMu.Unlock()
	m.reportFrontend()
	// The process terminal shows the program status again: it queries the
	// terminal for it, and is given the current status.
	tui.SetProgramStatusElsewhere(false)
	m.programStatusReporter().Resend()
	message := "Native rendering stopped: " + reason
	if closeErr != nil {
		message += fmt.Sprintf(" (closing it failed: %v)", closeErr)
	}
	m.showWarning(message)
}

// frontendInputReady tells the session that PiG's input loop is about to
// run, after startup has painted the first frame, and repaints the editor as
// sendable.
func (m *InteractiveMode) frontendInputReady() {
	if m.surface != nil {
		m.frontendInputRunning = true
		m.surface.Session().InputReady()
		m.tuiInst.RequestRender()
	}
}

// frontendInput offers terminal input to the frontend session and runs the
// editor actions it queued. It records a sequence the session took in
// frontendTookInput, so the input loop does not paint for it.
func (m *InteractiveMode) frontendInput(data string) bool {
	if m.surface == nil || !m.surface.HandleFrontendInput(data) {
		return false
	}
	m.frontendTookInput = true
	m.frontendActions.drain()
	return true
}

// closeFrontend stops drawing and closes the session while PiG still reads
// input, so the session's last replies are drained instead of reaching the
// shell. The error is reported after cooked mode returns.
func (m *InteractiveMode) closeFrontend() {
	if m.surface == nil {
		return
	}
	m.surface.Stop()
	m.frontendCloseErr = m.surface.Session().Close()
}

// applyBaseRendererWiring applies the wiring Run gives its renderer before
// the component tree exists.
func (m *InteractiveMode) applyBaseRendererWiring() {
	m.tuiInst.SetShowHardwareCursor(m.opts.Settings.GetShowHardwareCursor())
	m.tuiInst.SetClearOnShrink(m.opts.Settings.GetClearOnShrink())
	m.installRenderDispatcher()
	m.tuiInst.SetOverlayCommandDispatcher(func(command func()) {
		m.runOnMain(m.runCtx, command)
	})
}

// rewireRenderer applies the run's renderer-level wiring to a renderer that
// replaces another one mid-run, sourced from settings and host state so the
// current configuration carries across the swap.
func (m *InteractiveMode) rewireRenderer() {
	m.applyBaseRendererWiring()
	m.tuiInst.SetFocus(m.editor)
	if m.opts.SubprocessHost != nil {
		m.tuiInst.SetOnWidthChange(func(width int) { m.opts.SubprocessHost.NotifyWidth(width) })
	}
	// Registered unconditionally: the dialog chat cap depends on height even
	// when no subprocess extensions are loaded.
	m.tuiInst.SetOnHeightChange(m.onTerminalHeightChange)
}

// footerStatusesWithText is the footer's extension statuses a frontend lists: those that have text, nil when none do.
func footerStatusesWithText(statuses map[string]string) []string {
	texts := slices.DeleteFunc(footerExtensionStatuses(statuses), func(text string) bool { return text == "" })
	if len(texts) == 0 {
		return nil
	}
	return texts
}
