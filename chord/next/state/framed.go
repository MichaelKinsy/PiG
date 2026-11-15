package state

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/state/framed.ts (FramedOwner, FramedSource). Framed sources: a JSON source whose owner sets
// its value whole (a live query's rows, a list's pages joined), one frame per batch as the diff from the value its
// watches had, however often the batch set it (a list sets once per page a commit touched); it may fail and recover, as a
// derive does (design §11).

// FramedOwner is who a framed source belongs to: named in errors, and told its failures after its first value (once
// per episode).
type FramedOwner interface {
	// Label names the owner in errors.
	Label() string
	// Report tells the owner a failure of the source.
	Report(err error)
}

// framedState is where a framed source stands: before its first value, with a value, failed (its last good value kept
// for a watch opened meanwhile), or detached (its owner ended).
type framedState uint8

const (
	framedLoading framedState = iota
	framedHasValue
	framedFailed
	framedDetached
)

// FramedSource is a source its embedder feeds with Set and FailWith, inside a batch, as often as it likes: the batch
// delivers one frame, of the state it ended in. A failure makes it a failed member (as a throwing derive): its watches get
// one failure call, its owner one report per failure episode; the next value replaces the value whole (`[["r", value]]`).
// It is embedded by value and initialized with Init.
type FramedSource struct {
	Source
	owner FramedOwner
	state framedState
	// value is its value (framedHasValue), or the last good value of a failure (framedFailed, hasLast).
	value   delta.JsonValue
	hasLast bool
	// err is its failure (framedFailed) or its owner's end (framedDetached).
	err error
	// framed is the value its watches have, kept from its first change in a batch to that batch's frame: framedNone is not
	// listed in the batch; framedNoValue is Pi's NO_VALUE, what the watches of a failed source had (no value, so its next one
	// replaces the value whole).
	framed    framedKind
	framedVal delta.JsonValue
	// failing is set from a failure until the next value: a failure episode is reported once.
	failing bool
}

// Init makes the source of batch, owned by owner.
func (f *FramedSource) Init(batch *Batch, owner FramedOwner) {
	f.Source.Init(batch, f)
	f.owner = owner
}

// Ready reports whether it has a value.
func (f *FramedSource) Ready() bool { return f.state == framedHasValue }

// Failure is its failure while it is failed; with a value, before its first one and once detached it is nil (Pi's
// `undefined`; a failure is never nil here: a Go error is a value).
func (f *FramedSource) Failure() error {
	if f.state != framedFailed {
		return nil
	}
	return f.err
}

// Detached reports whether its owner ended.
func (f *FramedSource) Detached() bool { return f.state == framedDetached }

// Value is its value; before its first one a not_ready error, while failed its failure, once detached its owner's end.
func (f *FramedSource) Value() (delta.JsonValue, error) {
	switch f.state {
	case framedHasValue:
		return f.value, nil
	case framedLoading:
		return nil, data.NewChordError(data.ChordErrorInit{
			Code:    data.CodeNotReady,
			Message: f.owner.Label() + ": read before its first value",
		}, nil)
	default:
		return nil, f.err
	}
}

// Start is its value, or while failed its failure and last good value (a detached one is a failure with none).
func (f *FramedSource) Start() WatchStart {
	if f.state == framedFailed {
		return WatchStart{Kind: StartFailed, Error: f.err, HasLast: f.hasLast, Last: f.value}
	}
	value, err := f.Value()
	if err != nil {
		return WatchStart{Kind: StartFailed, Error: err}
	}
	return WatchStart{Kind: StartValue, Value: value}
}

// Quiet leaves its next failure unreported: whoever awaits its first value reports that failure itself.
func (f *FramedSource) Quiet() { f.failing = true }

// Set takes value, inside a batch: listed for the batch's frame when watched, its dependents marked.
func (f *FramedSource) Set(value delta.JsonValue) {
	if f.state == framedDetached {
		return
	}
	f.failing = false
	f.keep()
	f.state, f.value, f.hasLast, f.err = framedHasValue, value, false, nil
	f.markDependents()
}

// FailWith fails with err, inside a batch, keeping its last good value for a watch opened meanwhile.
func (f *FramedSource) FailWith(err error) {
	before, last, hasLast := f.state, f.value, f.hasLast
	if before == framedDetached {
		return
	}
	if !f.failing {
		f.owner.Report(HandedOut(err))
	}
	f.failing = true
	f.keep()
	f.state, f.err = framedFailed, err
	switch before {
	case framedHasValue:
		f.value, f.hasLast = last, true
	case framedFailed:
		f.value, f.hasLast = last, hasLast
	default:
		f.value, f.hasLast = nil, false
	}
	f.markDependents()
}

// Deliver delivers the batch's one frame, of the state the batch left: the diff from the value its watches had before the
// batch, or one failure call when they had a value.
func (f *FramedSource) Deliver(ctx context.Context) error {
	beforeKind, before := f.framed, f.framedVal
	f.framed, f.framedVal = framedNone, nil
	switch f.state {
	case framedHasValue:
		// Listed by a change, so before was kept (a detach in the batch leaves no value).
		after := f.value
		ops := []delta.Op{{"r", after}}
		if beforeKind != framedNoValue {
			ops = delta.DiffRevisions(before, after)
		}
		if len(ops) > 0 {
			f.Publish(after, ops, false, ctx)
		}
	case framedFailed:
		// Still failed since the last call: nothing new for the watches.
		if beforeKind != framedNoValue {
			f.FailWatches(f.err, ctx)
		}
	}
	return nil
}

// End is its owner ended: reads return err, its watches end ended.
func (f *FramedSource) End(err error) {
	f.state, f.err, f.value, f.hasLast = framedDetached, err, nil, false
	f.framed, f.framedVal = framedNone, nil
	f.EndWatches()
}

// keep is the first half of Pi's `#change(next)`, before the state becomes `next` (Go has no state value to pass: the caller
// assigns it between keep and markDependents). The first change of a batch lists the source for that batch's frame
// (batch.changed, once: a source listed twice delivers twice, two frames of one member in one commit, which a link refuses)
// and keeps what its watches have, which the frame diffs from; a later one lists nothing. Pi: `this.batch.changed(this)`
// is the Source's impl here, as in Source.Changed.
func (f *FramedSource) keep() {
	if f.Watched() && f.framed == framedNone {
		if f.state == framedHasValue {
			f.framed, f.framedVal = framedValue, f.value
		} else {
			f.framed, f.framedVal = framedNoValue, nil
		}
		f.batch.Changed(f.impl)
	}
}

// markDependents is the second half of `#change`: every change marks its dependents, one may have read the value since the
// last.
func (f *FramedSource) markDependents() {
	for _, dependent := range f.dependents.Snapshot() {
		dependent.Invalidate()
	}
}
