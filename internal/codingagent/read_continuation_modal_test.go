package codingagent

import (
	"context"
	"io"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/tui"
)

// The main loop handles a terminal read whole: after a chunk that announces more of its read, it waits for the rest.
// A modal that arms off the loop meanwhile takes the rest, and the router drops the read's end, so the arming itself
// must end the wait, including the first modal of the run. Otherwise the loop waits for input that went to the modal.
func TestReadContinuationEndsWhenAModalTakesTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readCh, errCh := make(chan inputChunk), make(chan error, 1)
		done := make(chan error, 1)
		go func() { done <- m.dispatchTerminalRead(ctx, inputChunk{more: true}, &readCh, errCh) }()
		synctest.Wait()
		_, release := m.acquireModalInputChannel()
		defer release()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("the main loop still waits for a read a modal took over")
		}
	})
}
