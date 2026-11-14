// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"sync/atomic"
	"time"
)

// FramePacing is the adaptive frame pacing policy: the one question Pi's scheduler asks, how long after a throttled frame began the next one may begin, answered from the last frame's cost instead of with Pi's constant. A renderer without a policy answers with the constant (MIN_RENDER_INTERVAL_MS); that is the default, and removing the policy restores it. Everything else is Pi's rule: a request marks one pending frame, drawn at once when the interval has passed since the last frame began and otherwise when it has; a forced request, a key press and RenderNow never ask.
//
// pig additive (D625): while the policy is active a throttled frame may begin 10 to 16 ms after the last one began, where Pi's always waits 16 ms; what a frame shows is the same.
type FramePacing struct {
	// terminalTrusted is whether the terminal the process runs in is trusted with synchronized updates (synchronizedUpdatesTrusted). The environment decides it, so it is read once.
	terminalTrusted bool
	// allowed is the owner's part of the answer: an agent run streams and no extension draws on the screen. The owner loop stores it before each throttled frame; the scheduler loads it under its lock.
	allowed *atomic.Bool
}

// frameIntervalFloor is the shortest interval the policy answers with: content that arrives faster is drawn once per floor.
// pig additive (D625): Pi's interval has no floor below its constant.
const frameIntervalFloor = 10 * time.Millisecond

// emptyFrameWait is the least time after a frame that sent the terminal nothing before the next frame may begin; it doubles with every further such frame in a row (frameIntervalLocked).
// pig additive (D625): Pi's interval does not depend on what a frame sent.
const emptyFrameWait = time.Millisecond

// frameCostFactor bounds drawing to a quarter of the loop: a frame that cost c is followed by one no sooner than 4c after it began, so from a cost of 4 ms on the interval is Pi's. A terminal that takes the bytes slowly blocks the frame's write, the blocked time is part of the cost, and the interval returns to Pi's.
// pig additive (D625): Pi's interval does not depend on what a frame cost.
const frameCostFactor = 4

// NewAdaptiveFramePacing returns the adaptive policy for the terminal the process runs in. allowed is the caller's gate: the policy answers with Pi's constant whenever it is false.
func NewAdaptiveFramePacing(allowed *atomic.Bool) *FramePacing {
	return &FramePacing{terminalTrusted: synchronizedUpdatesTrusted(), allowed: allowed}
}

// SetFramePacing installs the policy that answers the scheduler's interval; nil removes it, and the interval is Pi's constant again. Thread-safe.
func (t *tuiBase) SetFramePacing(pacing *FramePacing) {
	t.mu.Lock()
	t.pacing = pacing
	t.mu.Unlock()
}

// screenWriterTerminal is a terminal that says whether the screen writer (D624) is translating its frames now: it writes only the cells a frame changes, and no frame that changes none.
type screenWriterTerminal interface {
	ScreenWriterActive() bool
	// writesSent counts the writes the terminal sent on: a frame after which it is what it was before sent the terminal nothing. One atomic load.
	writesSent() uint64
}

// writesSent implements screenWriterTerminal.
func (t *ProcessTerminal) writesSent() uint64 { return t.sent.Load() }

// frameBeforeLocked is called when a frame begins, before lastRenderAt becomes its time: unless the frame before it sent the terminal nothing, that frame is the last one that showed something. mu must be held.
func (t *tuiBase) frameBeforeLocked() {
	if t.quietRun == 0 || !t.quietAt.Equal(t.lastRenderAt) {
		t.shownAt, t.shownCost, t.quietRun = t.lastRenderAt, t.lastFrameCost, 0
	}
}

// frameSentLocked is called when a measured frame has ended: sent is whether the terminal was sent anything during it. A frame that was is the last one that showed something as soon as the next frame begins (frameBeforeLocked). mu must be held.
func (t *tuiBase) frameSentLocked(sent bool) {
	if !sent {
		t.quietAt = t.lastRenderAt
		t.quietRun++
	}
}

// adaptiveFrameInterval is the adaptive policy's interval after a frame that cost cost and sent the terminal something.
func adaptiveFrameInterval(cost time.Duration) time.Duration {
	return min(minRenderInterval, max(frameIntervalFloor, frameCostFactor*cost))
}

// FramePacingActive reports whether the adaptive policy answers the scheduler's interval now: it is installed, the terminal is trusted with synchronized updates, the owner's gate is open and the screen writer is translating the terminal's frames. Thread-safe.
func (t *tuiBase) FramePacingActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.framePacingActiveLocked()
}

// framePacingActiveLocked is FramePacingActive with mu held. It takes no lock, starts nothing and allocates nothing.
func (t *tuiBase) framePacingActiveLocked() bool {
	pacing := t.pacing
	if pacing == nil || !pacing.terminalTrusted || !pacing.allowed.Load() {
		return false
	}
	terminal, ok := t.Terminal().(screenWriterTerminal)
	return ok && terminal.ScreenWriterActive()
}

// frameIntervalLocked is how long after a throttled frame began the next one may begin (tui.ts scheduleRender, MIN_RENDER_INTERVAL_MS). mu must be held. It takes no lock, starts nothing and allocates nothing.
func (t *tuiBase) frameIntervalLocked() time.Duration {
	if !t.framePacingActiveLocked() {
		return minRenderInterval
	}
	// pig additive (D625): with the policy installed, the terminal trusted with synchronized updates, the screen writer active and the owner's gate open, the interval follows the last frame's cost between the floor and Pi's constant.
	if t.quietRun == 0 || !t.quietAt.Equal(t.lastRenderAt) {
		return adaptiveFrameInterval(t.lastFrameCost)
	}
	// pig additive (D625): the last frame sent the terminal nothing (the screen writer found nothing changed), so it does not restart the interval. While the last frame that showed something is younger than Pi's constant, the next frame is due four times the empty frame's cost after it began, at least emptyFrameWait, twice that after the second empty frame in a row, four times after the third, and no later than Pi's constant after the frame that showed something: a change is shown no later than Pi's interval would show it. From then on the interval is Pi's constant after the last frame, until a frame shows something: a stream that changes nothing on the screen is drawn as often as Pi draws it, after at most three frames more.
	age := t.lastRenderAt.Sub(t.shownAt)
	if t.shownAt.IsZero() || age >= minRenderInterval {
		return minRenderInterval
	}
	wait := max(frameCostFactor*t.lastFrameCost, emptyFrameWait) << min(t.quietRun-1, 4)
	return max(min(wait, minRenderInterval-age), adaptiveFrameInterval(t.shownCost)-age)
}
