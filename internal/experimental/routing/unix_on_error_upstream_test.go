package routing

import (
	"errors"
	"testing"
)

// Pi server/src/transports/unix/listener.ts UnixListenerOptions.onError receives listener errors; a handler that throws is contained and never fails the listener.
// Pi: packages/server/src/transports/unix/listener.ts:22 (onError)
// packages/server/src/types.ts:11 and connection.ts:16: onError(error) receives the listener errors.
func TestUnixListenerReportsErrorsToOnErrorAndContainsAThrowingHandler(t *testing.T) {
	var got []error
	listener := &UnixListener{options: UnixListenerOptions{OnError: func(err error) { got = append(got, err) }}}
	want := errors.New("accept failed")
	listener.reportError(want)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("OnError got %v, want [%v]", got, want)
	}

	throwing := &UnixListener{options: UnixListenerOptions{OnError: func(error) { panic("handler failure") }}}
	throwing.reportError(want) // must not panic

	(&UnixListener{}).reportError(want) // no handler: no effect
}
