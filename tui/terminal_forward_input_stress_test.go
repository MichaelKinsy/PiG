//go:build unix

package tui

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestForwardInputDoesNotDropReadBytesOnCancel stresses the exact handoff
// tested by TestStoppedTerminalReaderDoesNotEatNextKeystroke, but with a
// keystroke burst racing the cancellation instead of one keystroke followed
// by a clean stop. forwardInput's contract (see Stop's doc comment: "without
// draining unread input... lets a subsequent terminal owner receive bytes
// typed during focus handoff") promises that every byte is either delivered
// through onInput or left unread for the next reader. A byte forwardInput
// already consumed from stdin via readInput must never be silently discarded:
// that is data loss a later reader cannot recover, unlike an unread byte
// still sitting in the pipe.
func TestForwardInputDoesNotDropReadBytesOnCancel(t *testing.T) {
	const iterations = 500
	payload := []byte("0123456789/settings")

	for iter := range iterations {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}

		terminal := NewProcessTerminalWithOutput(r, nil, &strings.Builder{})
		ctx, cancel := context.WithCancel(context.Background())

		var mu sync.Mutex
		var delivered []byte
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			terminal.forwardInput(ctx, func(data []byte) {
				mu.Lock()
				delivered = append(delivered, data...)
				mu.Unlock()
			}, nil)
		}()

		rng := rand.New(rand.NewSource(int64(iter)))
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for _, b := range payload {
				if _, werr := w.Write([]byte{b}); werr != nil {
					return
				}
				if rng.Intn(3) == 0 {
					time.Sleep(time.Duration(rng.Intn(200)) * time.Microsecond)
				}
			}
		}()
		<-writerDone

		// Cancel at a randomized point so the race lands at a different spot
		// in the reader's progress across iterations.
		time.Sleep(time.Duration(rng.Intn(300)) * time.Microsecond)
		cancel()
		<-readerDone

		_ = w.Close()
		rest, _ := io.ReadAll(r)
		_ = r.Close()

		mu.Lock()
		total := append(append([]byte(nil), delivered...), rest...)
		mu.Unlock()

		if !bytes.Equal(total, payload) {
			t.Fatalf("iteration %d: delivered+unread = %q, want %q (forwardInput dropped a byte it already read from stdin)", iter, total, payload)
		}
	}
}
