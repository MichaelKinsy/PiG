package subprocess

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderProxyComponent keeps subprocess-rendered lines in a host-side cache.
// pig divergence (D56): renderer inactivity cancels one generation off-loop,
// and renderRetry asks the extension again once it can answer.
// Render never performs IPC or JSON work; it returns the current generation and
// starts at most one owned background request loop when width/options change.
type renderProxyComponent struct {
	// changed is set when a response replaces the painted lines; a parent that caches this component reads it through IsDirty and NeedsRedraw.
	changed    atomic.Bool
	extName    string
	customType string
	method     string // "render_message" | "render_entry"
	payloadKey string // "message" | "entry"
	payload    any    // extension.CustomMessage | extension.CustomEntry
	conn       *Conn
	inactivity time.Duration
	invalidate func()
	fallback   []string

	// key is the host's name for the message or entry the component draws (renderKeys); "" when the renderer call is not for a displayed one.
	key string
	// takesMouse is the extension's report that the component it returned takes the mouse (RenderResult.Mouse).
	takesMouse atomic.Bool
	// regions are the rectangles of the Node component's MouseRegions (RenderResult.Regions), which HandleMouse hit-tests.
	regions atomic.Pointer[[]RenderMouseRegion]

	mu         sync.Mutex
	lines      []string
	linesWidth int // width the cached lines were rendered at
	width      int
	options    extension.MessageRenderOptions
	generation uint64
	requesting bool
	retry      renderRetry
	// rerender is whether the next request runs the renderer. Pi runs it when it builds the component (first draw, a new expansion or padding, an
	// invalidation) and draws the component it returned on every other frame (a resize, a click), so only those three set it; an answer to a request
	// that ran the renderer clears it.
	rerender bool
	// events are the mouse events the view took since the last request; the next request carries them, so the extension applies them before it draws.
	events []ViewEventPayload
	// view is the view of the renderer's results (D107).
	view rendererView
}

func newRenderProxyComponent(
	extName string,
	customType string,
	message extension.CustomMessage,
	options extension.MessageRenderOptions,
	conn *Conn,
	inactivity time.Duration,
	invalidate func(),
) *renderProxyComponent {
	return &renderProxyComponent{
		extName:    extName,
		customType: customType,
		method:     "render_message",
		payloadKey: "message",
		payload:    message,
		conn:       conn,
		inactivity: inactivity,
		invalidate: invalidate,
		fallback:   []string{fmt.Sprintf("[%s]", customType)},
		options:    options,
		key:        renderKeys.key(conn, options.Identity),
		rerender:   true,
	}
}

// newEntryRenderProxyComponent builds the host-side proxy for a subprocess
// custom-entry renderer. It shares renderProxyComponent's proven single-flight,
// generation-guarded, off-loop IPC core with the message renderer; only the RPC
// method and payload key differ. The host-owned transcript Spacer(1) upstream
// CustomEntryComponent adds is contributed by the caller (addCustomEntryToChat),
// so this proxy stays a pure content renderer identical to the message proxy.
func newEntryRenderProxyComponent(
	extName string,
	customType string,
	entry extension.CustomEntry,
	options extension.EntryRenderOptions,
	conn *Conn,
	inactivity time.Duration,
	invalidate func(),
) *renderProxyComponent {
	return &renderProxyComponent{
		extName:    extName,
		customType: customType,
		method:     "render_entry",
		payloadKey: "entry",
		payload:    entry,
		conn:       conn,
		inactivity: inactivity,
		invalidate: invalidate,
		fallback:   []string{fmt.Sprintf("[%s]", customType)},
		options:    extension.MessageRenderOptions{Expanded: options.Expanded, Identity: options.Identity},
		key:        renderKeys.key(conn, options.Identity),
		rerender:   true,
	}
}

func (c *renderProxyComponent) Render(width int) []string {
	c.mu.Lock()
	start := false
	if width > 0 && width != c.width {
		c.width = width
		c.generation++
		if !c.requesting && c.conn != nil {
			c.requesting = true
			start = true
		}
	}
	// pig divergence (D56): the first frame after an abandoned render's retry came due asks the extension again.
	if c.retry.takeDue() && !start && !c.requesting && c.width > 0 && c.conn != nil {
		c.requesting = true
		start = true
	}
	lines := c.lines
	if len(lines) == 0 {
		lines = c.fallback
	} else {
		// A response for an earlier width is never painted (whatever its rows)
		// while the request at the current width is in flight.
		lines = widthx.FrameAt(lines, c.linesWidth, c.width)
		if live, ok := c.view.live(c.width); ok && c.linesWidth == c.width {
			lines = live
		}
	}
	out := append([]string(nil), lines...)
	c.mu.Unlock()

	if start {
		go c.requestLoop()
	}
	return out
}

// IsDirty reports whether a response replaced the painted lines since the last NeedsRedraw, so a caching parent re-renders the proxy.
func (c *renderProxyComponent) IsDirty() bool { return c.changed.Load() }

// NeedsRedraw consumes the flag IsDirty reads.
func (c *renderProxyComponent) NeedsRedraw() bool { return c.changed.Swap(false) }

func (c *renderProxyComponent) Invalidate() {
	c.mu.Lock()
	c.rerender = true
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

// SetExpanded updates the renderer options and refreshes the cached generation.
func (c *renderProxyComponent) SetExpanded(expanded bool) {
	c.mu.Lock()
	if c.options.Expanded == expanded {
		c.mu.Unlock()
		return
	}
	c.options.Expanded = expanded
	c.rerender = true
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

// SetOutputPad refreshes custom-message rendering without changing entry options.
func (c *renderProxyComponent) SetOutputPad(padding int) {
	c.mu.Lock()
	if c.method != "render_message" || c.options.OutputPad == padding {
		c.mu.Unlock()
		return
	}
	c.options.OutputPad = padding
	c.rerender = true
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

func (c *renderProxyComponent) requestLoop() {
	for {
		c.mu.Lock()
		generation := c.generation
		width := c.width
		options := c.options
		rerender := c.rerender
		events := c.events
		c.events = nil
		c.mu.Unlock()

		lines, ok, stall := c.request(width, options, rerender, events)

		c.mu.Lock()
		current := generation == c.generation
		if current && ok {
			// The answer ran the renderer when it was asked to, and the next frame draws the component it returned.
			c.rerender = false
			c.lines = append([]string(nil), lines...)
			c.linesWidth = width
			c.changed.Store(true)
		}
		if current {
			c.requesting = false
			var epoch uint64
			var backoff time.Duration
			var settled <-chan struct{}
			if stall != nil {
				epoch, backoff, settled = c.retry.abandoned(c.inactivity, stall.Settled)
			} else {
				c.retry.answered()
			}
			c.mu.Unlock()
			if ok && c.invalidate != nil {
				c.invalidate()
			}
			if stall != nil {
				c.conn.afterRetryBackoff(settled, backoff, func() { c.retryDue(epoch) }, nil)
			}
			return
		}
		c.mu.Unlock()
	}
}

// retryDue makes the retry of abandonment epoch due and asks for the frame
// that sends it. The proxy reports itself changed, so a parent that caches
// its lines renders it again.
func (c *renderProxyComponent) retryDue(epoch uint64) {
	c.mu.Lock()
	due := c.retry.comeDue(epoch)
	if due {
		c.changed.Store(true)
	}
	c.mu.Unlock()
	if due && c.invalidate != nil {
		c.invalidate()
	}
}

// request runs one render in the extension. stall is set when the renderer
// inactivity boundary abandoned the request.
func (c *renderProxyComponent) request(width int, options extension.MessageRenderOptions, rerender bool, events []ViewEventPayload) (lines []string, ok bool, stall *HandlerStalledError) {
	ctx := context.Background()
	var wireOptions any = options
	if c.method == "render_entry" {
		wireOptions = extension.EntryRenderOptions{Expanded: options.Expanded}
	}
	fields := map[string]any{
		c.payloadKey: c.payload,
		"options":    wireOptions,
		"width":      width,
	}
	if c.key != "" {
		fields["key"] = c.key
	}
	if rerender {
		fields["rerender"] = true
	}
	if len(events) > 0 {
		fields["events"] = events
	}
	args, err := json.Marshal(fields)
	if err != nil {
		return nil, false, nil
	}
	resp, err := c.conn.requestWithInactivity(ctx, &Envelope{
		Type: MsgRequest,
		Request: &RequestPayload{
			Method: c.method,
			Tool:   c.customType,
			Args:   args,
		},
	}, c.inactivity, c.method+" "+c.customType)
	if abandoned, isStall := errors.AsType[*HandlerStalledError](err); isStall {
		return nil, false, abandoned
	}
	if err != nil || resp == nil || resp.Response == nil || resp.Response.Error != nil {
		return nil, false, nil
	}
	var result RenderResult
	if err := json.Unmarshal(resp.Response.Result, &result); err != nil {
		return nil, false, nil
	}
	c.takesMouse.Store(result.Mouse)
	c.regions.Store(&result.Regions)
	lines, ok = c.view.apply(c.conn, c.invalidate, c.key, c.enqueueEvent, result, width)
	return lines, ok, nil
}

// enqueueEvent queues a mouse event for the extension and asks for the request that carries it. It performs no IPC and takes no view lock, so a view
// delivers its events from the TUI loop.
func (c *renderProxyComponent) enqueueEvent(ev ViewEventPayload) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

// HandleMouse gives a fullscreen mouse event to the component the extension drew, as Pi's Container gives it to the component the renderer returned
// (a MouseRegion in the posthorse and built-in cards). Nothing runs in the extension while the terminal waits: a kit view's mouse regions answer
// from their description, and the extension hears of the events they took in the next request, which draws the card again. A component the extension
// keeps (Node's) takes the click only, answered as handled at once; its press, drag, wheel and move stay with the host's selection and scrolling,
// which Pi's component would have left unhandled in the same places.
func (c *renderProxyComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	if c.key == "" || c.conn == nil {
		return nil
	}
	result := c.view.handleMouse(*remoteMouse(event))
	if !result.Handled && !result.Capture && !result.Focus {
		if event.Type != tui.MouseClick {
			return nil
		}
		var regions []RenderMouseRegion
		if p := c.regions.Load(); p != nil {
			regions = *p
		}
		if len(regions) > 0 {
			// The component's MouseRegions answer where Pi's hit test finds them; a click outside them is left unhandled, as in Pi.
			if !slices.ContainsFunc(regions, func(r RenderMouseRegion) bool {
				return event.X >= r.X && event.X < r.X+r.Width && event.Y >= r.Y && event.Y < r.Y+r.Height
			}) {
				return nil
			}
		} else if !c.takesMouse.Load() {
			// pig divergence (D277): a component that handles the mouse itself answers for itself, which the host cannot ask before it answers the terminal, so it gets the clicks inside it and no other event.
			return nil
		}
		c.enqueueEvent(ViewEventPayload{Key: c.key, Type: ViewEventMouse, ViewEventExtras: ViewEventExtras{Mouse: remoteMouse(event)}})
		result = extension.ViewMouseResult{Handled: true}
	}
	dispatched := &tui.TuiMouseDispatchResult{
		TuiMouseEventResult: tui.TuiMouseEventResult{Handled: true, Capture: result.Capture, Focus: result.Focus, Render: result.Render},
		Target: tui.TuiMouseDispatchTarget{
			Component: c, OriginX: event.ScreenX - event.X, OriginY: event.ScreenY - event.Y,
			Width: event.Width, Height: event.Height,
		},
	}
	if result.Focus {
		dispatched.FocusTarget = c
	}
	return dispatched
}

// FrontendView reports the structure of the renderer's result to a D91
// frontend (D107) while the result shown is the one laid out at width.
func (c *renderProxyComponent) FrontendView(width int) *frontend.View {
	c.mu.Lock()
	current := len(c.lines) > 0 && c.linesWidth == width
	c.mu.Unlock()
	if !current {
		return nil
	}
	return c.view.frontendView(width)
}

// renderRetryMaxBackoff caps the wait between the requests a render proxy
// sends an extension whose renders the inactivity boundary keeps abandoning.
// pig divergence (D56): Pi renders synchronously and never retries; the cap bounds how seldom an abandoned render asks again.
const renderRetryMaxBackoff = time.Minute

// renderRetry is a render proxy's plan after the renderer inactivity boundary
// abandoned its request (D56). Pi renders synchronously, so its renderer's
// output always reaches the screen; a proxy whose request was abandoned keeps
// its last frame or fallback and asks again on the first frame after a
// backoff. The backoff starts at the inactivity boundary and doubles with
// each consecutive abandonment up to renderRetryMaxBackoff, so an extension
// that never answers, or always answers too late, gets one request per
// backoff, never one per frame. Only for the first abandonment in a row does
// the retry also come due as soon as the abandoned request settles (the
// extension answered it late, or the connection ended), so a renderer that
// was busy for a moment renders again without waiting out the backoff; later
// abandonments in a row wait out the full backoff. Its methods run under the
// proxy's lock.
type renderRetry struct {
	// pending: the newest inputs' request was abandoned and nothing answered them since.
	pending bool
	// due: the next frame asks again.
	due bool
	// streak counts consecutive abandonments.
	streak int
	// epoch names the abandonment a waiting afterRetryBackoff belongs to.
	epoch uint64
}

// abandoned records that the boundary abandoned the newest inputs' request,
// whose Settled channel is settled. It returns the abandonment's epoch, its
// backoff, and the channel that may end the backoff early: settled after the
// first abandonment in a row, nil after later ones.
func (r *renderRetry) abandoned(inactivity time.Duration, settled <-chan struct{}) (uint64, time.Duration, <-chan struct{}) {
	r.pending, r.due = true, false
	r.streak++
	r.epoch++
	backoff := inactivity
	for i := 1; i < r.streak && backoff < renderRetryMaxBackoff; i++ {
		// pig divergence (D56): the backoff doubles with each consecutive abandonment.
		backoff *= 2
	}
	if r.streak > 1 {
		settled = nil
	}
	return r.epoch, min(backoff, renderRetryMaxBackoff), settled
}

// answered ends the retries: the extension answered the newest inputs, or
// their request failed for a reason the boundary did not cause.
func (r *renderRetry) answered() {
	r.pending, r.due, r.streak = false, false, 0
	r.epoch++
}

// comeDue makes the retry of abandonment epoch due. It reports false when a
// later abandonment or an answer superseded that abandonment.
func (r *renderRetry) comeDue(epoch uint64) bool {
	if !r.pending || epoch != r.epoch {
		return false
	}
	r.due = true
	return true
}

// takeDue reports and clears a retry that came due.
func (r *renderRetry) takeDue() bool {
	due := r.due
	r.due = false
	return due
}

// afterRetryBackoff waits on a connection task until the abandoned request settles or the backoff ends, then runs due. Close joins the task. It ends
// without due when the connection closes first: nothing on a closed connection can answer. closed, if set, runs then instead, and at once when the
// connection no longer admits tasks.
func (c *Conn) afterRetryBackoff(settled <-chan struct{}, backoff time.Duration, due, closed func()) {
	if c.startProducerTask(func() {
		timer := c.clock.NewTimer(backoff)
		defer timer.Stop()
		select {
		case <-settled:
		case <-timer.C():
		case <-c.done:
			if closed != nil {
				closed()
			}
			return
		}
		due()
	}) {
		return
	}
	if closed != nil {
		closed()
	}
}
