package main

import (
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type configInputReader struct {
	*io.PipeReader
	closed chan struct{}
}

func (r *configInputReader) Close() error { err := r.PipeReader.Close(); close(r.closed); return err }

func TestConfigSelectorHoldsLateNegotiationBeforeFocusDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, w := io.Pipe()
		defer func() { _ = r.Close(); _ = w.Close() }()
		previous := normalizeConfigInputSequence
		defer func() { normalizeConfigInputSequence = previous }()
		kitty := tui.IsKittyProtocolActive()
		defer tui.SetKittyProtocolActive(kitty)
		tui.SetKittyProtocolActive(false)
		seen := make(chan string, 8)
		normalizeConfigInputSequence = func(s string) string { seen <- s; return s }
		selector := tui.NewConfigSelector(nil, 0)
		ui := tui.NewWithOutput(io.Discard, 80, 24)
		ui.Add(selector)
		done := make(chan error, 1)
		go func() { done <- driveConfigSelector(ui, selector, r) }()
		if _, err := w.Write([]byte("a\x1b[")); err != nil {
			t.Fatal(err)
		}
		if got := <-seen; got != "a" {
			t.Fatalf("first event=%q", got)
		}
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		select {
		case got := <-seen:
			t.Errorf("prefix dispatched at framing deadline: %q", got)
		default:
		}
		if _, err := w.Write([]byte("?7u")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()
		select {
		case got := <-seen:
			t.Errorf("negotiation leaked to config: %q", got)
		default:
		}
		if !tui.IsKittyProtocolActive() {
			t.Error("late response did not activate Kitty")
		}
		if _, err := w.Write([]byte("\x03")); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestConfigEscapeCancelsAndJoinsPendingRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, w := io.Pipe()
		defer func() { _ = r.Close(); _ = w.Close() }()
		source := &configInputReader{PipeReader: r, closed: make(chan struct{})}
		selector := tui.NewConfigSelector(nil, 0)
		ui := tui.NewWithOutput(io.Discard, 80, 24)
		ui.Add(selector)
		done := make(chan error, 1)
		go func() { done <- driveConfigSelector(ui, selector, source) }()
		if _, err := w.Write([]byte("\x1b")); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		select {
		case <-source.closed:
		default:
			t.Error("config returned while its next raw Read remained blocked")
		}
	})
}
