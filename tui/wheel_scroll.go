package tui

// Ports packages/tui/src/wheel-scroll.ts

import (
	"math"
	"os"
	"runtime"
	"sync"
	"time"
)

// WheelScrollLines is the lines moved per mouse-wheel event, or Auto to accelerate fast wheel spins (upstream `number | "auto"`). The zero value moves one line.
type WheelScrollLines struct {
	Auto  bool
	Lines float64
}

const (
	// Several events closer than this belong to one physical notch (Ghostty emits them ~4 ms apart)
	// or come from a high-resolution source. They move one line each and do not accelerate.
	wheelBurstGapMs = 5
	// A pause longer than this ends a scroll gesture.
	wheelGestureGapMs = 200
	// Average event gap that maps to one line per event. Faster events scale up proportionally.
	wheelReferenceGapMs = 100
	// upstream: packages/tui/src/wheel-scroll.ts:MAX_AUTO_LINES
	wheelMaxAutoLines = 6
)

// terminalAcceleratesWheel reports whether wheel deltas are already accelerated. Local macOS terminals receive wheel and trackpad deltas that the OS has already accelerated, and they emit one event per line. Other platforms, and SSH sessions where the client platform is unknown, usually send one event per wheel notch.
func terminalAcceleratesWheel() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if _, set := os.LookupEnv(name); set {
			return false
		}
	}
	return true
}

// WheelScrollAccelerator converts wheel events into line counts.
//
// In Auto mode on terminals that do not accelerate wheel input, the count follows event velocity: an isolated notch moves one line, while a fast spin moves up to six lines per event. For example, notches 100 ms apart move 1 line each, 50 ms apart move 2, and 20 ms apart move 5. Settings changes may come from a different goroutine than wheel input, so its state is locked.
type WheelScrollAccelerator struct {
	mu         sync.Mutex
	lines      WheelScrollLines
	accelerate bool
	lastTime   float64
	// lastDirection is 0 until the first event.
	lastDirection int
	averageGap    float64
	hasAverageGap bool
	carry         float64
}

// NewWheelScrollAccelerator mirrors the constructor. Upstream defaults `accelerate` to `!terminalAcceleratesWheel()`; omit the argument for that default.
func NewWheelScrollAccelerator(lines WheelScrollLines, accelerate ...bool) *WheelScrollAccelerator {
	a := &WheelScrollAccelerator{lines: lines, accelerate: !terminalAcceleratesWheel()}
	if len(accelerate) > 0 {
		a.accelerate = accelerate[0]
	}
	a.resetLocked()
	return a
}

// SetLines replaces the line setting and resets the gesture state.
func (a *WheelScrollAccelerator) SetLines(lines WheelScrollLines) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lines = lines
	a.resetLocked()
}

// Next returns the positive line count for a wheel event in direction (-1 or 1) at time now, in milliseconds.
func (a *WheelScrollAccelerator) Next(direction int, now float64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.lines.Auto {
		if math.IsNaN(a.lines.Lines) || math.IsInf(a.lines.Lines, 0) {
			return 1
		}
		return int(min(math.Max(1, math.Floor(a.lines.Lines)), math.MaxInt32))
	}
	if !a.accelerate {
		return 1
	}

	gap := now - a.lastTime
	sameGesture := direction == a.lastDirection && gap <= wheelGestureGapMs
	a.lastTime = now
	a.lastDirection = direction
	if !sameGesture {
		a.hasAverageGap = false
		a.carry = 0
		return 1
	}
	if gap < wheelBurstGapMs {
		return 1
	}

	if a.hasAverageGap {
		a.averageGap = (a.averageGap + gap) / 2
	} else {
		a.averageGap, a.hasAverageGap = gap, true
	}
	lines := math.Min(wheelMaxAutoLines, math.Max(1, wheelReferenceGapMs/a.averageGap)) + a.carry
	whole := math.Floor(lines)
	a.carry = lines - whole
	return int(whole)
}

func (a *WheelScrollAccelerator) resetLocked() {
	a.lastTime = math.Inf(-1)
	a.lastDirection = 0
	a.hasAverageGap = false
	a.averageGap = 0
	a.carry = 0
}

var wheelClockStart = time.Now()

// wheelClock is the monotonic millisecond clock the alt-screen renderer times wheel events with, like `performance.now()`. Tests replace it.
var wheelClock = func() float64 { return float64(time.Since(wheelClockStart)) / float64(time.Millisecond) }
