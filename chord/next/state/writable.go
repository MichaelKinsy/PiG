package state

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
)

// Ports packages/chord/src/next/state/writable.ts (Writable, WritableOwner, WritableSource). Writables (slice 8 §2, design
// §17): self.writable(initial), an in-process value its attempt sets, identity compared; a contribution, a derive source
// and an `on` source, never a state member.

// Writable is an in-process value that changes when its owner sets it; Set of the same value notifies nobody.
type Writable[T any] interface {
	chord.NativeWatchable[T]
	// Set sets the value: a new value (by identity, see Identical) is a new frame for its watches and marks the derives and
	// registry tables reading it, in the batch running now or in one of its own. It returns released once its attempt
	// ended, and invalid/reentrant inside a derive compute or a compose.
	Set(value T) error
}

// WritableOwner is who owns a writable: the context its own batches run in, and the check that its attempt still runs.
type WritableOwner interface {
	// Context is the context a batch of the writable's own runs in.
	Context() context.Context
	// AssertUsable returns released once the attempt that made it was sealed.
	AssertUsable() error
}

// SectionOwner is a WritableOwner whose host runs its code in sections of one loop (Pi's single event loop). Set called from
// outside a section (a native's opener, which the host runs off the loop) enters one, as Pi's continuation after an
// `await` is on the loop; inside a section it nests.
type SectionOwner interface {
	WritableOwner
	// Do runs work as one section of the owner's loop.
	Do(work func())
	// OnLoop reports whether the caller is running a section of the owner's loop already: Set then needs no new one.
	OnLoop() bool
}

// WritableSource is a writable: its value, set by its owner's code, delivered through the host's batch. It is a Writable;
// NewWritableSource makes it.
type WritableSource[T any] struct {
	FollowedSource[T]
	value T
	// owner is the running attempt, nil once it ended and ended holds its error (the Go form of Pi's Ownership union).
	owner WritableOwner
	ended error
	// do is the owner's loop, fixed at construction (nil: the owner has none, and the caller is in a section).
	do     func(work func())
	onLoop func() bool
}

// NewWritableSource returns a writable holding initial, owned by owner, on batch.
func NewWritableSource[T any](initial T, owner WritableOwner, batch *Batch) *WritableSource[T] {
	w := &WritableSource[T]{value: initial, owner: owner}
	if sections, ok := owner.(SectionOwner); ok {
		w.do, w.onLoop = sections.Do, sections.OnLoop
	}
	w.Init(batch, w)
	return w
}

// Value is the value; it stays readable after the attempt ended.
func (w *WritableSource[T]) Value() T { return w.value }

// Set sets the value, as Writable.Set says. After the attempt ended it returns the error the attempt ended with, also for
// the value it has.
func (w *WritableSource[T]) Set(value T) error {
	if w.do != nil && !w.onLoop() {
		return w.setInSection(value)
	}
	return w.set(value)
}

// setInSection enters a section of the owner's loop for the Set (apart from Set, so the closure is made only here).
func (w *WritableSource[T]) setInSection(value T) (err error) {
	w.do(func() { err = w.set(value) })
	return err
}

func (w *WritableSource[T]) set(value T) error {
	if w.ended != nil {
		return w.ended
	}
	if err := w.owner.AssertUsable(); err != nil {
		return err
	}
	// The cost guard: setting the value it has costs one comparison past the seal check.
	if Identical(value, w.value) {
		return nil
	}
	batch := w.batch
	if err := batch.AssertNotComputing("writable.set"); err != nil {
		return err
	}
	return batch.Within(func() error {
		w.value = value
		w.Changed()
		return nil
	}, w.owner.Context())
}

// Detach is its attempt ended with err: its watches end ended, Set returns err, its value stays readable; it keeps neither
// its attempt nor the host.
func (w *WritableSource[T]) Detach(err error) {
	w.ended = err
	w.owner = nil
	w.End()
}
