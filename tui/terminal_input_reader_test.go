package tui

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestForwardTerminalNormalizesNativeShiftEnterAfterFraming(t *testing.T) {
	withTerminalInput(t, "TestForwardTerminalNormalizesNativeShiftEnterAfterFraming", 0, func(t *testing.T, in interactiveTestInput) {
		previous := normalizeTerminalInput
		defer func() { normalizeTerminalInput = previous }()
		var calls []string
		normalizeTerminalInput = func(sequence string) string {
			calls = append(calls, sequence)
			return NormalizeNativeShiftEnterInput(sequence, true, true)
		}
		terminal := NewProcessTerminalWithOutput(in.file, nil, ioDiscard{})
		in.ownQueryReply(terminal)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		inputs := make(chan string, 3)
		go func() {
			defer close(done)
			terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, nil)
		}()
		defer func() { cancel(); in.release(); <-done }()
		in.send(t, "ab\r")
		for _, want := range []string{"a", "b", "\x1b[13;2u"} {
			select {
			case got := <-inputs:
				if got != want {
					t.Fatalf("input=%q, want %q", got, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing normalized input")
			}
		}
		cancel()
		<-done
		if !slices.Equal(calls, []string{"a", "b", "\r"}) {
			t.Errorf("normalizer calls=%q", calls)
		}
	})
}

func TestForwardTerminalFramesAndHoldsNegotiationPrefix(t *testing.T) {
	withTerminalInput(t, "TestForwardTerminalFramesAndHoldsNegotiationPrefix", 0, func(t *testing.T, in interactiveTestInput) {
		preserveKeyboardProtocolState(t)
		terminal := NewProcessTerminalWithOutput(in.file, nil, ioDiscard{})
		in.ownQueryReply(terminal)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		type event struct {
			data string
			at   time.Time
		}
		inputs := make(chan event, 2)
		errors := make(chan error, 1)
		go func() {
			defer close(done)
			terminal.forwardInput(ctx, func(data []byte) { inputs <- event{string(data), time.Now()} }, func(err error) { errors <- err })
		}()
		defer func() { cancel(); in.release(); <-done }()
		start := time.Now()
		in.send(t, "a\x1b[")
		for i, want := range []string{"a", "\x1b["} {
			select {
			case input := <-inputs:
				if input.data != want {
					t.Fatalf("event%d=%q, want %q", i, input.data, want)
				}
				// Exact50+150ms deadlines are tested with synctest; this real reader guard detects premature dispatch without relying on scheduler precision.
				if i == 1 && input.at.Sub(start) < 150*time.Millisecond {
					t.Fatalf("prefix dispatched after %v, before its negotiation hold", input.at.Sub(start))
				}
			case err := <-errors:
				t.Fatal(err)
			case <-time.After(5 * time.Second):
				t.Fatalf("missing event %q", want)
			}
		}
	})
}

func TestForwardTerminalNegotiationRespectsBatchOrder(t *testing.T) {
	// Pi forwards the ordinary data event before consuming the following response, even when both arrive in one stdin chunk.
	withTerminalInput(t, "TestForwardTerminalNegotiationRespectsBatchOrder", 0, func(t *testing.T, in interactiveTestInput) {
		preserveKeyboardProtocolState(t)
		terminal := NewProcessTerminalWithOutput(in.file, nil, ioDiscard{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		activeAtInput := make(chan bool, 1)
		go func() {
			defer close(done)
			terminal.forwardInput(ctx, func(data []byte) {
				if string(data) == "a" {
					activeAtInput <- terminal.KittyProtocolActive()
				}
			}, nil)
		}()
		defer func() { cancel(); in.release(); <-done }()
		in.send(t, "a\x1b[?7u")
		select {
		case active := <-activeAtInput:
			if active {
				t.Fatal("later negotiation took effect before earlier input callback")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing ordinary input callback")
		}
	})
}
