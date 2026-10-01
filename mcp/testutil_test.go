package mcp_test

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

// jsonEqual reports whether got marshals to the same JSON value as want, the
// way vitest's toEqual compares plain objects.
func jsonEqual(t *testing.T, got any, want string) {
	t.Helper()
	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var g, w any
	if err := json.Unmarshal(gotBytes, &g); err != nil {
		t.Fatalf("unmarshal got %s: %v", gotBytes, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("unmarshal want %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s\nwant %s", gotBytes, want)
	}
}

// fakeClock is the virtual clock that stands in for vitest's fake timers.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Duration
	fn      func()
	stopped bool
	fired   bool
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: c.now + d, fn: f}
	c.timers = append(c.timers, timer)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		wasActive := !timer.stopped && !timer.fired
		timer.stopped = true
		return wasActive
	}
}

// Sleep blocks the calling goroutine until the virtual time has advanced by d.
func (c *fakeClock) Sleep(d time.Duration) {
	done := make(chan struct{})
	c.AfterFunc(d, func() { close(done) })
	<-done
}

// Advance moves the virtual time forward and runs the timers that come due,
// earliest first.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now + d
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, timer := range c.timers {
			if timer.stopped || timer.fired || timer.at > target {
				continue
			}
			if next == nil || timer.at < next.at {
				next = timer
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.fired = true
		c.now = next.at
		c.mu.Unlock()
		next.fn()
	}
}

// pending is the number of timers that have neither fired nor been stopped.
func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fired {
			n++
		}
	}
	return n
}
