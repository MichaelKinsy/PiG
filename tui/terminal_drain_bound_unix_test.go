//go:build unix

package tui

import (
	"os"
	"sync"
	"testing"
	"time"
)

// terminal.ts drainInput: the loop ends at maxMs even when input never goes idle, and at idleMs after the last byte when it does; the call
// returns to its caller (it is awaited) rather than running on in the background.
func TestProcessTerminalDrainInputEndsAtTheMaximumWhileInputKeepsArriving(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	chunk := make([]byte, 4096)
	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	defer func() { close(stop); _ = w.Close(); writer.Wait() }()

	term := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
	const maxWait = 150 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- term.DrainInput(maxWait, 50*time.Millisecond) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DrainInput = %v", err)
		}
		if elapsed := time.Since(start); elapsed < maxWait {
			t.Fatalf("drain ended after %v with input still arriving; it runs to maxMs %v", elapsed, maxWait)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DrainInput never returned while input kept arriving: the maximum does not bound it")
	}
}

func TestProcessTerminalDrainInputEndsOnceInputIsIdle(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	term := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- term.DrainInput(30*time.Second, 20*time.Millisecond) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DrainInput = %v", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("idle input drained in %v", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DrainInput ignored the idle window and waited for the maximum")
	}
}
