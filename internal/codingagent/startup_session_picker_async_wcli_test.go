package codingagent

// pi: packages/coding-agent/src/cli/session-picker.ts

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi session-picker.ts:25-36,54 resolves the first select or cancel only after
// ui.stop(); the loaders it handed the selector take (onProgress, signal) and
// Go joins them before returning: a loader still blocked when Escape cancels
// the picker sees its context cancelled, and has returned by the time
// selectStartupSession does.
func TestSelectStartupSessionCancelsAndJoinsBlockedLoaders(t *testing.T) {
	var loaderStarted = make(chan struct{})
	observed, release := make(chan struct{}, 2), make(chan struct{})
	var cancelled, returned atomic.Bool
	blocked := func(options SessionListOptions) ([]SessionInfo, error) {
		select {
		case loaderStarted <- struct{}{}:
		default:
		}
		<-options.Context.Done()
		cancelled.Store(true)
		select {
		case observed <- struct{}{}:
		default:
		}
		<-release
		returned.Store(true)
		return nil, options.Context.Err()
	}
	terminal := &fakeStartupTerminal{}
	type result struct {
		path     string
		selected bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		path, selected, err := selectStartupSession(blocked, blocked, StartupUIOptions{Settings: Settings{Theme: "dark"}},
			tui.NewWithOutput(io.Discard, 120, 30), terminal)
		done <- result{path, selected, err}
	}()
	select {
	case <-loaderStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the picker never started its loader")
	}
	terminal.send(t, "\x1b")
	select {
	case <-observed:
	case <-time.After(10 * time.Second):
		t.Fatal("Escape did not cancel the blocked loader")
	}
	// The loader holds its return until released: the picker must still be waiting for it.
	select {
	case <-done:
		t.Fatal("the picker returned before its cancelled loader finished")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case r := <-done:
		if r.err != nil && !errors.Is(r.err, io.EOF) {
			t.Fatal(r.err)
		}
		if r.selected || r.path != "" {
			t.Fatalf("picked (%q, %v) after cancel, want (\"\", false)", r.path, r.selected)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the picker did not finish after Escape")
	}
	if !cancelled.Load() || !returned.Load() {
		t.Fatalf("loader cancelled=%v returned=%v when the picker returned; the loader must be cancelled and joined", cancelled.Load(), returned.Load())
	}
}
