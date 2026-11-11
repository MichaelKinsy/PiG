package host

import "time"

// Ports packages/chord/src/next/host/backoff.ts. Retry backoff per identity (§6): after a transient failure an identity
// waits 1 s, doubling up to 60 s. The entries hold no object, so a failed object is collected after its last reference
// while its window still runs.

// firstDelay is the delay after the first transient failure.
const firstDelay = time.Second

// maxDelay is the longest delay.
const maxDelay = 60 * time.Second

// nextDelay is the delay after a failure: 1 s after the first (no previous delay), then the previous one doubled, up to
// 60 s. Identities, routines and job finishes back off alike. Pi's `previous: number | undefined` is previous 0 (a delay
// is never 0 once chosen).
func nextDelay(previous time.Duration) time.Duration {
	if previous == 0 {
		return firstDelay
	}
	return min(previous*2, maxDelay)
}

// backoffEntry is one identity's backoff: the delay of its last window, when that window ends (Date.now() time, ms), and
// the timer ending a window still running (nil when none runs).
type backoffEntry struct {
	delay time.Duration
	until int64
	timer Timer
}

// backoff is the host's backoff map. When a window ends, due(identity) runs: the host retries the identity's object, or
// deletes the entry when there is none. An entry is deleted on success, on a broken failure, and when its window has
// passed with nothing left to retry. It is used inside the host's sections.
type backoff struct {
	loop    eventLoop
	entries map[string]*backoffEntry
	due     func(identity string)
}

func newBackoff(loop eventLoop, due func(identity string)) *backoff {
	return &backoff{loop: loop, entries: map[string]*backoffEntry{}, due: due}
}

// failed records a transient failure: a window of the previous delay doubled (1 s at first), then due.
func (b *backoff) failed(identity string) {
	previous := b.entries[identity]
	var before time.Duration
	if previous != nil {
		if previous.timer != nil {
			previous.timer.Stop()
		}
		before = previous.delay
	}
	delay := nextDelay(before)
	entry := &backoffEntry{delay: delay, until: b.loop.Now() + delay.Milliseconds()}
	entry.timer = b.loop.AfterFunc(delay, func() {
		entry.timer = nil
		b.due(identity)
	})
	b.entries[identity] = entry
}

// waiting reports whether identity is inside a window: a new object for it waits for due before its first attempt.
func (b *backoff) waiting(identity string) bool {
	entry := b.entries[identity]
	return entry != nil && entry.timer != nil
}

// until is when the window of identity ends, while one runs: a failed reference's retryAt.
func (b *backoff) until(identity string) (int64, bool) {
	entry := b.entries[identity]
	if entry == nil || entry.timer == nil {
		return 0, false
	}
	return entry.until, true
}

// skip ends the window at once (its object is retried now: `ref.retry()`, a wake); the delay stays for the next failure.
func (b *backoff) skip(identity string) {
	entry := b.entries[identity]
	if entry == nil {
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.timer = nil
}

// ended: its object ended; an entry whose window has passed gets no outcome any more and goes.
func (b *backoff) ended(identity string) {
	if _, ok := b.entries[identity]; ok && !b.waiting(identity) {
		delete(b.entries, identity)
	}
}

// delete forgets identity (success, a broken failure, or a window that passed with nothing to retry).
func (b *backoff) delete(identity string) {
	if entry := b.entries[identity]; entry != nil && entry.timer != nil {
		entry.timer.Stop()
	}
	delete(b.entries, identity)
}

// clear forgets every identity (the host closes).
func (b *backoff) clear() {
	for identity := range b.entries {
		b.delete(identity)
	}
}
