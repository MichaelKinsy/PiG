package subprocess

import (
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// PushProxy implements the Component interface (Render + Invalidate) backed by
// a cached []string that the extension pushes asynchronously. Render() NEVER
// touches the socket: it reads from the cache in O(1). Width-change events
// flow back to the extension via a notification so it can re-render.
//
// Performance contract: Render() returns in <1μs (slice copy from cache).
// All socket I/O happens on the connection's read goroutine.
//
// pig-specific: no upstream equivalent.
type PushProxy struct {
	mu    sync.RWMutex
	lines []string
	// linesWidth is the width the extension rendered lines at (0 = unknown).
	linesWidth int

	// width is tracked separately with atomic to avoid write-under-RLock.
	width atomic.Int32

	// invalidate is called when new lines are pushed, signaling the TUI to
	// schedule a re-render on the next tick.
	invalidate func()

	// onWidthChange is called when the host notifies the proxy of a terminal
	// width change. The proxy forwards this to the extension so it can re-render.
	onWidthChange func(width int)
}

// NewPushProxy creates a proxy component. The invalidate callback should trigger
// a TUI render cycle. The onWidthChange callback should notify the extension
// of the new terminal width.
func NewPushProxy(invalidate func(), onWidthChange func(width int)) *PushProxy {
	return &PushProxy{
		invalidate:    invalidate,
		onWidthChange: onWidthChange,
	}
}

// Render returns the cached lines. This NEVER blocks on I/O: guaranteed <1μs.
// Satisfies the tui.Component interface.
//
// Returns a defensive copy so callers cannot corrupt the cache.
func (p *PushProxy) Render(width int) []string {
	// Track width changes atomically (no lock needed).
	oldWidth := int(p.width.Swap(int32(width)))
	if width > 0 && oldWidth != width && p.onWidthChange != nil {
		go p.onWidthChange(width)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.lines) == 0 {
		return nil
	}
	out := make([]string, len(p.lines))
	copy(out, p.lines)
	// A frame rendered for another width is never painted (whatever its rows);
	// the extension re-renders on the host's width_change notification.
	return widthx.FrameAt(out, p.linesWidth, width)
}

// Invalidate marks the component for re-render. Called by the TUI framework.
// For PushProxy this is a no-op: re-renders are triggered by UpdateLines.
func (p *PushProxy) Invalidate() {
	// No-op: the push model means we update when the extension pushes,
	// not when the TUI framework asks us to.
}

// UpdateLines replaces the cached lines and triggers a TUI re-render.
// Called from the connection's read goroutine when a widget_push arrives.
// Thread-safe: can be called from any goroutine.
func (p *PushProxy) UpdateLines(lines []string) { p.UpdateLinesAt(lines, 0) }

// UpdateLinesAt replaces the cached lines with a frame rendered at width
// (0 when the extension did not report it) and triggers a TUI re-render.
func (p *PushProxy) UpdateLinesAt(lines []string, width int) {
	p.mu.Lock()
	p.lines = lines
	p.linesWidth = width
	p.mu.Unlock()

	if p.invalidate != nil {
		p.invalidate()
	}
}

// Clear removes all cached lines.
func (p *PushProxy) Clear() {
	p.mu.Lock()
	p.lines = nil
	p.mu.Unlock()

	if p.invalidate != nil {
		p.invalidate()
	}
}

// Lines returns the current cached lines (for testing/inspection).
func (p *PushProxy) Lines() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.lines))
	copy(out, p.lines)
	return out
}
