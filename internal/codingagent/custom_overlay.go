package codingagent

import (
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// customOverlay is the Go-side counterpart to a remote (subprocess) custom
// overlay. The component caches lines pushed by the remote producer,
// forwards every input chunk to the registered handler, and exposes a
// terminal Done flag so the input loop in
// [ExtUIContext.RunRemoteOverlay] can exit cleanly.
//
// Render copies cached rows on the owning TUI loop and performs no subprocess I/O. UpdateLines accepts concurrent producer updates.
//
// pig-specific: no upstream equivalent. Upstream's CustomComponent is
// constructed in-process by the extension; the subprocess shim instead
// caches lines and forwards input.
type customOverlay struct {
	mu    sync.RWMutex
	lines []string
	width int
	// terminalWidth runs only during Render on the owning TUI loop. The
	// component's render width may be smaller than this terminal geometry key.
	terminalWidth func() int
	done          bool
	result        any
	onInput       func(data string)
	onChange      func()

	// closedCh is closed exactly once by Close so a waiter blocked on input can
	// observe an extension-initiated close instead of sitting on its input
	// channel until the next keystroke happens to arrive.
	closedCh  chan struct{}
	closeOnce sync.Once
}

func newCustomOverlay(onChange func()) *customOverlay {
	return &customOverlay{onChange: onChange, closedCh: make(chan struct{})}
}

// Render returns cached lines only for their terminal geometry. It performs no socket I/O.
// Satisfies tui.Component.
func (c *customOverlay) Render(width int) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.terminalWidth != nil {
		width = c.terminalWidth()
	}
	if len(c.lines) == 0 || (c.width > 0 && c.width != width) {
		return nil
	}
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

func (c *customOverlay) Invalidate() {}

// HandleInput forwards the input chunk to the registered handler.
// Satisfies tui.InputHandler.
func (c *customOverlay) HandleInput(data string) {
	c.mu.RLock()
	fn := c.onInput
	c.mu.RUnlock()
	if fn != nil {
		fn(data)
	}
}

// SetOnInput registers the input-forwarding handler. Replaces any
// previous handler.
func (c *customOverlay) SetOnInput(fn func(data string)) {
	c.mu.Lock()
	c.onInput = fn
	c.mu.Unlock()
}

// UpdateLines replaces the cached lines and triggers a re-render.
func (c *customOverlay) UpdateLines(lines []string) {
	c.UpdateLinesAt(lines, 0)
}

// UpdateLinesAt retains the terminal width with the snapshot so a resize cannot paint stale rows.
func (c *customOverlay) UpdateLinesAt(lines []string, width int) {
	c.mu.Lock()
	if c.width == width && slices.Equal(c.lines, lines) {
		c.mu.Unlock()
		return
	}
	if len(lines) == 0 {
		c.lines = nil
	} else {
		c.lines = append([]string(nil), lines...)
	}
	c.width = width
	cb := c.onChange
	c.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Close marks the overlay as done with the given result and triggers
// a re-render so the surrounding input loop can exit.
func (c *customOverlay) Close(result any) {
	c.mu.Lock()
	c.done = true
	c.result = result
	cb := c.onChange
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.closedCh) })
	if cb != nil {
		cb()
	}
}

// Closed reports the close as it happens. Done() only answers when asked, so a
// caller parked on input needs this to wake.
func (c *customOverlay) Closed() <-chan struct{} { return c.closedCh }

// Done reports whether the overlay has been closed.
func (c *customOverlay) Done() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.done
}

// Result returns the close value. Defined when Done() is true.
func (c *customOverlay) Result() any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.result
}

// Compile-time component check.
var _ tui.Component = (*customOverlay)(nil)

// remoteOverlayTUIOptions maps a remote ui.custom() overlay request onto TUI
// overlay options. Upstream showOverlay mounts the component with no frame and
// resolves width/maxHeight/anchor/margin from overlayOptions (defaulting to
// min(80, available) centred), so that is the path unless the caller used
// pig's legacy modal fields (title/widthFraction/heightFraction) without
// upstream overlayOptions.
func remoteOverlayTUIOptions(opts extension.RemoteOverlayOptions) tui.OverlayOptions {
	legacyModal := opts.Title != "" || opts.WidthFraction > 0 || opts.HeightFraction > 0
	if opts.Layout == nil && legacyModal {
		wf := opts.WidthFraction
		if wf <= 0 {
			wf = 0.75
		}
		hf := opts.HeightFraction
		if hf <= 0 {
			hf = 0.7
		}
		return tui.OverlayOptions{Title: opts.Title, WidthFraction: wf, HeightFraction: hf}
	}
	if opts.Layout == nil {
		return tui.OverlaySpec{}.Options()
	}
	l := opts.Layout
	spec := tui.OverlaySpec{
		Width:        overlayValue(l.Width),
		MinWidth:     l.MinWidth,
		MaxHeight:    overlayValue(l.MaxHeight),
		Anchor:       l.Anchor,
		OffsetX:      l.OffsetX,
		OffsetY:      l.OffsetY,
		Row:          overlayValue(l.Row),
		Col:          overlayValue(l.Col),
		NonCapturing: l.NonCapturing,
	}
	if m := l.Margin; m != nil {
		if m.All != nil {
			all := *m.All
			spec.MarginAll = &all
		} else {
			spec.Margin = &tui.OverlayMarginSpec{Top: m.Top, Right: m.Right, Bottom: m.Bottom, Left: m.Left}
		}
	}
	return spec.Options()
}

func overlayValue(v *extension.OverlaySizeValue) *tui.OverlayValue {
	if v == nil {
		return nil
	}
	return &tui.OverlayValue{Value: v.Value, Percent: v.Percent, Invalid: v.Invalid}
}
