package utils

import (
	"sync"
	"time"
)

// publisherClock supplies wall time and one-shot timers in milliseconds.
type publisherClock interface {
	now() float64
	afterFunc(delayMs float64, fn func()) (stop func())
}

type systemClock struct{}

func (systemClock) now() float64 { return float64(time.Now().UnixMilli()) }

func (systemClock) afterFunc(delayMs float64, fn func()) func() {
	timer := time.AfterFunc(time.Duration(delayMs*float64(time.Millisecond)), fn)
	return func() { timer.Stop() }
}

// AdaptivePublisherOptions configure an AdaptivePublisher.
type AdaptivePublisherOptions[TValue, TUpdate any] struct {
	Snapshot func() TValue
	// Update derives the publication from the last published value (nil
	// before the first publication); ok false publishes nothing but still
	// advances the baseline.
	Update  func(previous *TValue, current TValue) (update TUpdate, ok bool)
	Measure func(update TUpdate) int
	Publish func(update TUpdate) error
	// OnError receives publication errors raised from the trailing timer.
	OnError func(err error)
	// MinIntervalMs defaults to 100; TargetBytesPerSecond to 100 KiB.
	MinIntervalMs        *float64
	TargetBytesPerSecond *float64
}

// AdaptivePublisher publishes the latest state without queuing intermediate
// mutations. The first dirty state after idle is immediate. Each publication
// buys a delay proportional to its encoded size, with a minimum interval that
// also bounds event count. A single trailing timer guarantees eventual
// publication. Deliveries are serialized in publication order and run outside
// the state lock: a forced flush waits for an in-flight delivery, while an
// unforced flush that finds one in flight (including a reentrant call from the
// consumer) defers to the trailing timer. A consumer must not force a flush
// from inside its own delivery.
type AdaptivePublisher[TValue, TUpdate any] struct {
	mu                   sync.Mutex
	options              AdaptivePublisherOptions[TValue, TUpdate]
	minIntervalMs        float64
	targetBytesPerSecond float64
	clock                publisherClock
	deliverMu            sync.Mutex
	published            *TValue
	dirty                bool
	nextEmitAt           float64
	stopTimer            func()
	timerGeneration      int
	disposed             bool
}

// NewAdaptivePublisher constructs a publisher with the wall clock.
func NewAdaptivePublisher[TValue, TUpdate any](options AdaptivePublisherOptions[TValue, TUpdate]) *AdaptivePublisher[TValue, TUpdate] {
	return newAdaptivePublisher(options, systemClock{})
}

func newAdaptivePublisher[TValue, TUpdate any](options AdaptivePublisherOptions[TValue, TUpdate], clock publisherClock) *AdaptivePublisher[TValue, TUpdate] {
	publisher := &AdaptivePublisher[TValue, TUpdate]{options: options, clock: clock, minIntervalMs: 100, targetBytesPerSecond: 100 * 1024}
	if options.MinIntervalMs != nil {
		publisher.minIntervalMs = *options.MinIntervalMs
	}
	if options.TargetBytesPerSecond != nil {
		publisher.targetBytesPerSecond = *options.TargetBytesPerSecond
	}
	return publisher
}

// MarkDirty records a state change and publishes immediately when the
// interval bought by the previous publication has elapsed.
func (publisher *AdaptivePublisher[TValue, TUpdate]) MarkDirty() error {
	publisher.mu.Lock()
	if publisher.disposed {
		publisher.mu.Unlock()
		return nil
	}
	publisher.dirty = true
	wait := publisher.nextEmitAt - publisher.clock.now()
	if wait > 0 {
		publisher.armTimerLocked(wait)
		publisher.mu.Unlock()
		return nil
	}
	publisher.mu.Unlock()
	return publisher.Flush(false)
}

// Flush publishes dirty state now when force is set or the publication
// interval has elapsed; otherwise it arms the trailing timer.
func (publisher *AdaptivePublisher[TValue, TUpdate]) Flush(force bool) error {
	if force {
		publisher.deliverMu.Lock()
	} else if !publisher.deliverMu.TryLock() {
		publisher.deferWhileDelivering()
		return nil
	}
	defer publisher.deliverMu.Unlock()
	publisher.mu.Lock()
	update, publish := publisher.prepareLocked(force)
	publisher.mu.Unlock()
	if !publish {
		return nil
	}
	return publisher.options.Publish(update)
}

func (publisher *AdaptivePublisher[TValue, TUpdate]) deferWhileDelivering() {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if !publisher.disposed && publisher.dirty {
		publisher.armTimerLocked(max(publisher.nextEmitAt-publisher.clock.now(), 1))
	}
}

func (publisher *AdaptivePublisher[TValue, TUpdate]) prepareLocked(force bool) (TUpdate, bool) {
	var none TUpdate
	if publisher.disposed || !publisher.dirty {
		return none, false
	}
	now := publisher.clock.now()
	if !force && now < publisher.nextEmitAt {
		publisher.armTimerLocked(publisher.nextEmitAt - now)
		return none, false
	}
	publisher.clearTimerLocked()
	current := publisher.options.Snapshot()
	update, ok := publisher.options.Update(publisher.published, current)
	publisher.published = &current
	publisher.dirty = false
	if !ok {
		return none, false
	}
	encodedBytes := float64(publisher.options.Measure(update))
	publisher.nextEmitAt = now + max(publisher.minIntervalMs, encodedBytes*1000/publisher.targetBytesPerSecond)
	// Commit before delivery. A consumer may apply the update and then fail or
	// reenter the producer; retaining the old baseline would duplicate that
	// delta.
	return update, true
}

// Dispose cancels the trailing timer and ignores later changes.
func (publisher *AdaptivePublisher[TValue, TUpdate]) Dispose() {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.clearTimerLocked()
	publisher.disposed = true
}

func (publisher *AdaptivePublisher[TValue, TUpdate]) clearTimerLocked() {
	if publisher.stopTimer != nil {
		publisher.stopTimer()
	}
	publisher.stopTimer = nil
	publisher.timerGeneration++
}

func (publisher *AdaptivePublisher[TValue, TUpdate]) armTimerLocked(wait float64) {
	if publisher.stopTimer != nil {
		return
	}
	publisher.timerGeneration++
	generation := publisher.timerGeneration
	publisher.stopTimer = publisher.clock.afterFunc(wait, func() {
		publisher.mu.Lock()
		if generation != publisher.timerGeneration {
			publisher.mu.Unlock()
			return
		}
		publisher.stopTimer = nil
		publisher.mu.Unlock()
		if err := publisher.Flush(false); err != nil && publisher.options.OnError != nil {
			publisher.options.OnError(err)
		}
	})
}
