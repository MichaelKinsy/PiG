// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

// ProgressBytesPerSecond is the pace each progress commit buys: a pause proportional to what it wrote.
const ProgressBytesPerSecond = 100 * 1024

// progress is Progress of output.ts: adaptive progress commits. The first change after an idle period commits at once; each
// commit then delays the next by at least minIntervalMs and by its written size at 100 KiB/s. At most one commit is in
// flight; changes made meanwhile coalesce into the next one.
type progress struct {
	h             *Host
	write         func(then func(bytes int, err error))
	onError       func(error)
	minIntervalMs float64

	waiters  []func(error)
	cancel   func()
	inFlight bool
	nextAt   float64
	dirty    bool
	stopped  bool
	onIdle   func()
}

func newProgress(h *Host, write func(then func(int, error)), onError func(error), minIntervalMs float64) *progress {
	return &progress{h: h, write: write, onError: onError, minIntervalMs: minIntervalMs}
}

// mark schedules a commit.
func (p *progress) mark() {
	p.dirty = true
	p.schedule()
}

// markAndWait schedules a commit; then runs with the commit that includes this change.
func (p *progress) markAndWait(then func(error)) {
	p.waiters = append(p.waiters, then)
	p.mark()
}

// idle reports whether stop calls back at once: no progress commit is in flight.
func (p *progress) idle() bool { return !p.inFlight }

// stop stops committing and waits for the commit in flight; then receives the waiters the final commit must settle.
func (p *progress) stop(then func(waiters []func(error))) {
	p.stopped = true
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	take := func() { w := p.waiters; p.waiters = nil; then(w) }
	if p.inFlight {
		p.onIdle = take
		return
	}
	take()
}

func (p *progress) schedule() {
	if p.stopped || p.cancel != nil || p.inFlight {
		return
	}
	wait := p.nextAt - p.h.Now()
	if wait <= 0 {
		p.flush()
		return
	}
	p.cancel = p.h.After(wait, func() {
		p.cancel = nil
		p.flush()
	})
}

func (p *progress) flush() {
	if p.stopped || !p.dirty {
		return
	}
	p.dirty = false
	waiters := p.waiters
	p.waiters = nil
	started := p.h.Now()
	p.inFlight = true
	p.write(func(bytes int, err error) {
		if err == nil {
			p.nextAt = started + max(p.minIntervalMs, float64(bytes)*1000/ProgressBytesPerSecond)
			for _, w := range waiters {
				w(nil)
			}
		} else {
			p.nextAt = started + p.minIntervalMs
			for _, w := range waiters {
				w(err)
			}
			p.onError(err)
		}
		p.inFlight = false
		if p.dirty {
			p.schedule()
		}
		if p.onIdle != nil {
			idle := p.onIdle
			p.onIdle = nil
			idle()
		}
	})
}
