// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"sync/atomic"
	"time"
)

// FramePacing is the adaptive frame pacing policy: the one question Pi's scheduler asks, how long after a throttled frame began the next one may begin, answered from the last frame's cost instead of with Pi's constant. A renderer without a policy answers with the constant (MIN_RENDER_INTERVAL_MS); that is the default, and removing the policy restores it. Everything else is Pi's rule: a request marks one pending frame, drawn at once when the interval has passed since the last frame began and otherwise when it has; a forced request, a key press and RenderNow never ask.
//
// pig divergence (D625): while the policy is active a throttled frame may begin 10 to 16 ms after the last one began, where Pi's always waits 16 ms; what a frame shows is the same.
type FramePacing struct {
	// terminalTrusted is whether the terminal the process runs in is trusted with synchronized updates (synchronizedUpdatesTrusted). The environment decides it, so it is read once.
	terminalTrusted bool
	// allowed is the owner's part of the answer: an agent run streams and no extension draws on the screen. The owner loop stores it before each throttled frame; the scheduler loads it under its lock.
	allowed *atomic.Bool
}

// frameIntervalFloor is the shortest interval the policy answers with: content that arrives faster is drawn once per floor.
// pig divergence (D625): Pi's interval has no floor below its constant.
const frameIntervalFloor = 10 * time.Millisecond

// frameCostFactor bounds drawing to a quarter of the loop: a frame that cost c is followed by one no sooner than 4c after it began, so from a cost of 4 ms on the interval is Pi's. A terminal that takes the bytes slowly blocks the frame's write, the blocked time is part of the cost, and the interval returns to Pi's.
// pig divergence (D625): Pi's interval does not depend on what a frame cost.
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
}

// frameIntervalLocked is how long after a throttled frame began the next one may begin (tui.ts scheduleRender, MIN_RENDER_INTERVAL_MS). mu must be held. It takes no lock, starts nothing and allocates nothing.
func (t *tuiBase) frameIntervalLocked() time.Duration {
	pacing := t.pacing
	if pacing == nil || !pacing.terminalTrusted || !pacing.allowed.Load() {
		return minRenderInterval
	}
	if terminal, ok := t.Terminal().(screenWriterTerminal); !ok || !terminal.ScreenWriterActive() {
		return minRenderInterval
	}
	// pig divergence (D625): with the policy installed, the terminal trusted with synchronized updates, the screen writer active and the owner's gate open, the interval follows the last frame's cost between the floor and Pi's constant.
	return min(minRenderInterval, max(frameIntervalFloor, frameCostFactor*t.lastFrameCost))
}
