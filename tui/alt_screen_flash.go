package tui

import (
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// altScreenFlashDefaultDurationMS mirrors upstream DEFAULT_DURATION_MS. Go has no
// default parameters, so the caller (the alt-screen renderer) supplies it
// explicitly; the constant carries the value.
const altScreenFlashDefaultDurationMS = 1000

type flashEntry struct {
	id      int
	message string
	stop    func() bool
}

// AltScreenFlashContainer holds transient reverse-video messages composited by
// the alternate-screen renderer. Ports pi-tui's AltScreenFlashContainer.
//
// Forced Go mechanic: upstream's single-loop setTimeout(...).unref() removal is
// an owned time.AfterFunc guarded by a mutex, because each timer fires on its own
// goroutine and races Render/Flash/Dispose. The per-entry id is the identity
// guard: a fired-but-already-removed callback finds its id gone and no-ops. The
// requestRender callback runs after the lock is released so slow render work
// never holds the mutex. Go timers never keep the process alive, so unref() has
// no Go counterpart; Dispose stops every timer to avoid a goroutine leak.
type AltScreenFlashContainer struct {
	invalidatable
	mu            sync.Mutex
	entries       []flashEntry
	nextID        int
	requestRender func()
	// afterFunc schedules a removal and returns its cancel; time.AfterFunc in production, a manual clock in the Pi comparison test.
	afterFunc func(d time.Duration, f func()) (stop func() bool)
}

// NewAltScreenFlashContainer constructs a flash container that calls
// requestRender whenever the visible set changes.
func NewAltScreenFlashContainer(requestRender func()) *AltScreenFlashContainer {
	return &AltScreenFlashContainer{requestRender: requestRender, afterFunc: func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }}
}

// Flash shows message in reverse video for durationMs, then removes it. The
// caller supplies altScreenFlashDefaultDurationMS for the optional upstream
// default; an explicit zero or negative duration is clamped to zero like
// Math.max(0, durationMs).
func (c *AltScreenFlashContainer) Flash(message string, durationMs int) {
	durationMs = max(0, durationMs)
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	stop := c.afterFunc(time.Duration(durationMs)*time.Millisecond, func() {
		c.mu.Lock()
		removed := false
		for i, entry := range c.entries {
			if entry.id == id {
				c.entries = append(c.entries[:i], c.entries[i+1:]...)
				removed = true
				break
			}
		}
		c.mu.Unlock()
		if removed {
			c.requestRender()
		}
	})
	c.entries = append(c.entries, flashEntry{id: id, message: message, stop: stop})
	c.mu.Unlock()
	c.requestRender()
}

// Dispose stops all pending timers and clears the visible set.
func (c *AltScreenFlashContainer) Dispose() {
	c.mu.Lock()
	for _, entry := range c.entries {
		entry.stop()
	}
	c.entries = nil
	c.mu.Unlock()
}

// Render returns one reverse-video line per active flash, each padded-spaced and
// truncated to width. Mirrors upstream render.
func (c *AltScreenFlashContainer) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := make([]string, len(c.entries))
	for i, entry := range c.entries {
		message := widthx.TruncateToWidth(" "+entry.message+" ", width, "", false)
		lines[i] = "\x1b[7m" + message + "\x1b[27m"
	}
	return lines
}
