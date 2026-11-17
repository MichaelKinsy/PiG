package chordctx_test

import (
	"context"
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// A WithoutAbortSignal context keeps nothing of its parent's cancellation (Pi's withoutAbortSignal gives the context no abort
// signal at all, context/index.ts:118-121), so the abort of a parent behind it is not an abort of anything derived from it, and
// AbortOrder, which is the place of the abort that ended a context, does not walk through it.
func TestAbortOrderStopsAtADetachedAncestor(t *testing.T) {
	parent, cancelParent := chordctx.WithCancel(chordctx.Background())
	detached := chordctx.WithoutAbortSignal(parent)
	child, cancelChild := chordctx.WithCancel(detached)
	cancelParent(context.Canceled)
	if got := chordctx.AbortOrder(detached); got != 0 {
		t.Errorf("a context detached from an aborted parent has abort order %d, want 0: it is not aborted", got)
	}
	if got := chordctx.AbortOrder(child); got != 0 {
		t.Errorf("a child of that context has abort order %d before it is cancelled, want 0", got)
	}
	cancelChild(context.Canceled)
	if own, parents := chordctx.AbortOrder(child), chordctx.AbortOrder(parent); own == 0 || own <= parents {
		t.Errorf("abort order of the child cancelled after its detached ancestor's parent = %d, parent's %d; want its own, later place", own, parents)
	}
}

// The reason of a context derived from two sources is the one of the source that aborted first (AbortSignal.any), and a source
// that stands behind a detached ancestor is aborted by its own cancel alone: the abort of the ancestor's parent is not before
// it. A listener added after the aborts hears the first source of the composite that aborted, which is `second` here, not the
// source whose detached ancestor's parent was cancelled first.
func TestLateListenerHearsTheFirstSourceThatAbortedNotADetachedAncestorsParent(t *testing.T) {
	x, cancelX := chordctx.WithCancel(chordctx.Background())
	behindDetached, cancelBehindDetached := chordctx.WithCancel(chordctx.WithoutAbortSignal(x))
	other, cancelOther := chordctx.WithCancel(chordctx.Background())
	composite := chordctx.WithAbortSignal(behindDetached, other)
	errOther, errBehind := errors.New("the independent source"), errors.New("the source behind the detached context")
	cancelX(errors.New("the parent behind the detached context"))
	cancelOther(errOther)
	cancelBehindDetached(errBehind)
	var heard []error
	chordctx.OnAbort(composite, func(cause error) { heard = append(heard, cause) })
	if len(heard) != 1 || heard[0] != errOther { //nolint:errorlint // the abort reason is passed by identity: a wrapping error would be a different reason
		t.Errorf("a listener added after the aborts heard %v, want once, %v: the first source of the composite that aborted", heard, errOther)
	}
}
