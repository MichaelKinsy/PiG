//go:build windows

package tui

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The reader waits on the console handle and a cancel event together, so Stop never leaves a reader blocked in a read that belongs to the next terminal.
func TestWindowsInputWaiterTimesOutSignalsAndCancels(t *testing.T) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(event), "event") // owns and closes the handle
	defer func() { _ = file.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiter, err := newTerminalInputWaiter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.close()

	if ready, err := waiter.wait(file, 20); err != nil || ready {
		t.Fatalf("idle wait = %v, %v; want a timeout", ready, err)
	}
	if err := windows.SetEvent(event); err != nil {
		t.Fatal(err)
	}
	if ready, err := waiter.wait(file, 1000); err != nil || !ready {
		t.Fatalf("signalled wait = %v, %v; want ready", ready, err)
	}
	if err := windows.ResetEvent(event); err != nil {
		t.Fatal(err)
	}

	var returned atomic.Bool
	done := make(chan struct{})
	go func() {
		ready, err := waiter.wait(file, -1)
		returned.Store(true)
		if ready || err != nil {
			t.Errorf("cancelled wait = %v, %v", ready, err)
		}
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if returned.Load() {
		t.Fatal("an infinite wait returned before cancellation")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not wake the wait")
	}
	runtime.KeepAlive(file)
}

// Windows has no SIGWINCH: the watcher polls the console size, kicks once at start, and stops with its context.
func TestWindowsResizeWatcherKicksOnceAndStops(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	terminal := NewProcessTerminal(r, w)
	ctx, cancel := context.WithCancel(t.Context())
	var resizes atomic.Int32
	stop := terminal.startResizeWatcher(ctx, func() { resizes.Add(1) })
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
	for resizes.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no startup resize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(3 * resizePollInterval)
	if got := resizes.Load(); got != 1 {
		t.Fatalf("resize callbacks = %d with a constant size, want only the startup kick", got)
	}
	cancel()
}

// enableVTProcessing leaves a redirected stdout alone and returns a restore that does nothing.
func TestWindowsEnableVTProcessingIgnoresRedirectedOutput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	NewProcessTerminal(r, w).enableVTProcessing()()
	(&ProcessTerminal{}).enableVTProcessing()()
}

// The native Shift key state comes from GetKeyState; the Windows console sends a plain Return for Shift+Enter, so Return is rewritten when Shift is held.
func TestNativeShiftEnterNormalizationOnWindows(t *testing.T) {
	for _, tc := range []struct {
		sequence string
		shift    bool
		want     string
	}{
		{"\r", true, "\x1b[13;2u"},
		{"\r", false, "\r"},
		{"a", true, "a"},
		{"\x1b[13;2u", true, "\x1b[13;2u"},
	} {
		got := normalizeProcessInputSequenceFor(tc.sequence, "windows", "", func(ModifierKey) bool { return tc.shift })
		if got != tc.want {
			t.Errorf("%q shift=%v = %q, want %q", tc.sequence, tc.shift, got, tc.want)
		}
	}
	for _, modifier := range []ModifierKey{ModifierShift, ModifierCommand, ModifierControl, ModifierOption} {
		_ = IsNativeModifierPressed(modifier)
	}
	if got := NormalizeProcessInputSequence("a"); got != "a" {
		t.Errorf("NormalizeProcessInputSequence(a) = %q", got)
	}
}
