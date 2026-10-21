// Package jsclock is the process clock of JavaScript's Date.now() and performance.now(), with a replaceable source.
// Upstream tests replace both with vi.spyOn; Go keeps the replacement here so telemetry adapters read the same clock.
package jsclock

import (
	"sync"
	"time"
)

var (
	mu      sync.RWMutex
	start   = time.Now()
	dateNow func() float64
	perfNow func() float64
)

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
	return mock, func() {
		mu.Lock()
		dateNow = nil
		mu.Unlock()
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
