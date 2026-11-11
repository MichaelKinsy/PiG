package tui

import chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"

// Ports apps/p2/src/tui (the plugins' calls through useOptimistic): the kit's OptimisticAdd returns its call's result
// (Pi's add returns the promise's value); the p2 plugins' calls answer with nothing but an error, so their result type
// is struct{} and these wrappers fix it.

// OptimisticAdd is the kit's add for a call that answers with an error only.
type OptimisticAdd[A any] = chordtui.OptimisticAdd[A, struct{}]

// Optimistic is chordtui.Optimistic for calls that answer with an error only.
func Optimistic[T, A any](s *chordtui.TuiScope, read func() T, reduce func(current T, action A) T) (shown func() T, add OptimisticAdd[A], pending func() bool) {
	return chordtui.Optimistic[T, A, struct{}](s, read, reduce)
}

// OptimisticLast is chordtui.OptimisticLast for calls that answer with an error only.
func OptimisticLast[T any](s *chordtui.TuiScope, read func() T) (func() T, OptimisticAdd[T], func() bool) {
	return chordtui.OptimisticLast[T, struct{}](s, read)
}

// AddCall runs call under add and returns its error.
func AddCall[A any](add OptimisticAdd[A], action A, call func() error) error {
	_, err := add(action, func() (struct{}, error) { return struct{}{}, call() })
	return err
}
