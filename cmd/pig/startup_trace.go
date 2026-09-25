// Startup timing instrumentation for pig.
//
// Activated by setting PIG_STARTUP_TRACE=1. Prints structured timing
// to stderr showing wall-clock elapsed from process start for each
// startup. Uses Go's monotonic clock via time.Since.
//
// Usage:
//
//	PIG_STARTUP_TRACE=1 pig --model github-copilot/gpt-5-mini
//
// Output:
//
//	[startup] flags-parsed          0ms
//	[startup] services-init         2ms
//	[startup] model-resolved        3ms
//	[startup] extensions-loaded    25ms
//	[startup] session-created      28ms
//	[startup] interactive-ready   130ms
//
// This is the official profiling mechanism. Do NOT add ad-hoc
// fmt.Fprintf timing to main.go or interactive.go: use this.
package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// startupTrace records from process start.
// Zero-cost when disabled (all methods are no-ops).
type startupTrace struct {
	enabled bool
	t0      time.Time
	mu      sync.Mutex
}

var trace = startupTrace{
	enabled: os.Getenv("PIG_STARTUP_TRACE") != "",
	t0:      time.Now(), // captures process init time
}

func installStartupTrace(s *startupTrace, install func(func(string))) {
	install(s.MarkFunc())
}

// MarkFunc returns the trace callback for components that add their own labels.
// It returns nil while tracing is disabled so those components can skip label
// construction as well as timestamp and output work.
func (s *startupTrace) MarkFunc() func(string) {
	if !s.enabled {
		return nil
	}
	return s.Mark
}

// Mark records a named checkpoint. No-op when tracing is disabled.
func (s *startupTrace) Mark(label string) {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[startup] %-28s %dms\n", label, time.Since(s.t0).Milliseconds())
}

// traceSDKLockWaits marks each startup wait for another process's staged-SDK
// transaction lock, so a slow startup names the holder it is waiting behind.
func traceSDKLockWaits() {
	if !trace.enabled {
		return
	}
	pigsdklock.SetWaitObserver(func(string, bool) func() {
		trace.Mark("sdk-lock-wait-start")
		return func() { trace.Mark("sdk-lock-wait-done") }
	})
}
