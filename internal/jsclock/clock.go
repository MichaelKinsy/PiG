// Package jsclock is the process clock of JavaScript's Date.now() and performance.now(), with a replaceable source.
// Upstream tests replace both with vi.spyOn; Go keeps the replacement here so telemetry adapters read the same clock.
package jsclock

import (
	"sync"
	"sync/atomic"
	"time"
)

var (
	mu      sync.RWMutex
	start   = time.Now()
	dateNow func() float64
	perfNow func() float64
	// generation counts the replacements and restorations of Date.now(): a consumer that anchors one clock to the other re-anchors
	// when it changes (a test that moves Date.now() starts a new process as far as the anchor is concerned).
	generation atomic.Uint64
)

// Generation is how many times Date.now() was replaced or restored. It never decreases.
func Generation() uint64 { return generation.Load() }

// DateNow is Date.now(): whole milliseconds since the Unix epoch.
func DateNow() float64 {
	mu.RLock()
	source := dateNow
	mu.RUnlock()
	if source != nil {
		return source()
	}
	return float64(time.Now().UnixMilli())
}

// DateTime is DateNow as a time.
func DateTime() time.Time {
	//portlint:allow numbers DateNow is whole milliseconds well inside int64 (Date.now(); a mock sets finite values)
	return time.UnixMilli(int64(DateNow()))
}

// PerformanceNow is performance.now(): fractional milliseconds on a monotonic clock since process start.
func PerformanceNow() float64 {
	mu.RLock()
	source := perfNow
	mu.RUnlock()
	if source != nil {
		return source()
	}
	return float64(time.Since(start)) / float64(time.Millisecond)
}

// Mock is a replacement for one clock, set like vi.spyOn(...).mockReturnValue.
type Mock struct {
	mu    sync.Mutex
	value float64
}

// Set is mockReturnValue.
func (mock *Mock) Set(value float64) {
	mock.mu.Lock()
	mock.value = value
	mock.mu.Unlock()
}

func (mock *Mock) get() float64 {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	return mock.value
}

// MockDateNow replaces Date.now() with a constant that returns initial until Set. The returned restore reinstalls the
// real clock, as vi.restoreAllMocks does.
func MockDateNow(initial float64) (*Mock, func()) {
	mock := &Mock{value: initial}
	mu.Lock()
	dateNow = mock.get
	mu.Unlock()
	generation.Add(1)
	return mock, func() {
		mu.Lock()
		dateNow = nil
		mu.Unlock()
		generation.Add(1)
	}
}

// MockPerformanceNow replaces performance.now() like MockDateNow.
func MockPerformanceNow(initial float64) (*Mock, func()) {
	mock := &Mock{value: initial}
	mu.Lock()
	perfNow = mock.get
	mu.Unlock()
	return mock, func() {
		mu.Lock()
		perfNow = nil
		mu.Unlock()
	}
}

// TimeOrigin is performance.timeOrigin: the wall-clock time of PerformanceNow's zero, in fractional milliseconds since the
// Unix epoch.
func TimeOrigin() float64 { return float64(start.UnixNano()) / float64(time.Millisecond) }
