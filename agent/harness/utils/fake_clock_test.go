package utils

import "sort"

// fakeClock runs timers synchronously when advanced, like vitest fake timers.
type fakeClock struct {
	nowMs  float64
	timers []*fakeTimer
	seq    int
}

type fakeTimer struct {
	at      float64
	seq     int
	fn      func()
	stopped bool
}

func (clock *fakeClock) now() float64 { return clock.nowMs }

func (clock *fakeClock) afterFunc(delayMs float64, fn func()) func() {
	clock.seq++
	timer := &fakeTimer{at: clock.nowMs + max(delayMs, 0), seq: clock.seq, fn: fn}
	clock.timers = append(clock.timers, timer)
	return func() { timer.stopped = true }
}

func (clock *fakeClock) advance(ms float64) {
	target := clock.nowMs + ms
	for {
		due := clock.nextDue(target)
		if due == nil {
			break
		}
		clock.nowMs = due.at
		due.stopped = true
		due.fn()
	}
	clock.nowMs = target
}

func (clock *fakeClock) nextDue(target float64) *fakeTimer {
	var live []*fakeTimer
	for _, timer := range clock.timers {
		if !timer.stopped && timer.at <= target {
			live = append(live, timer)
		}
	}
	if len(live) == 0 {
		return nil
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].at != live[j].at {
			return live[i].at < live[j].at
		}
		return live[i].seq < live[j].seq
	})
	return live[0]
}
