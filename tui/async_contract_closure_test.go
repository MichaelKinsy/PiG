package tui

import (
	"sync"
	"testing"
	"time"
)

// cancellable-loader.ts handleInput: abortController.abort() runs before onAbort?.(), so a callback that reads the signal sees it already aborted.
func TestCancellableLoaderAbortsTheSignalBeforeOnAbortRuns(t *testing.T) {
	loader := NewCancellableLoader(nil, nil, nil, "loading", nil)
	var abortedInCallback, signalErrInCallback bool
	loader.OnAbort = func() {
		abortedInCallback = loader.Aborted()
		signalErrInCallback = loader.Signal().Err() != nil
	}
	loader.HandleInput("\x1b")
	if !abortedInCallback || !signalErrInCallback {
		t.Fatalf("onAbort saw aborted=%v signal.Err set=%v; abort() runs first", abortedInCallback, signalErrInCallback)
	}
	select {
	case <-loader.Signal().Done():
	default:
		t.Fatal("the signal's Done channel is not closed after the cancel key")
	}
}

// tui-alt-screen.ts copyTextToClipboard awaits copySelection: the host event loop keeps running while the callback is in flight, the flash reports
// the settled result only after it, and a callback that fails is reported as the message of its failure.
func TestAltScreenCopySelectionCallbackRunsWithoutTheScreenLockAndFlashesAfterItSettles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		want    string
		wantRet bool
	}{
		{"success", nil, "Copied!", true},
		{"failure", errCopyDenied, "denied by host", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			letGo := func() { releaseOnce.Do(func() { close(release) }) }
			defer letGo()
			copyOnSelect := false
			h := newAltHarness(t, 80, 4, TuiAltScreenOptions{CopyOnSelect: &copyOnSelect, CopySelection: func(string) error {
				close(entered)
				<-release
				return tc.err
			}})
			h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
			h.start()
			h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M", "\x1b[<0;4;2m")
			result := make(chan bool, 1)
			go func() { result <- h.tui.CopyActiveSelectionToClipboard() }()
			<-entered

			// While the callback is in flight the screen is still served: this call takes the screen lock.
			served := make(chan struct{})
			go func() { h.tui.GetScreenLines(); close(served) }()
			select {
			case <-served:
			case <-time.After(10 * time.Second):
				t.Fatal("the screen lock is held while the copy callback is in flight")
			}
			select {
			case <-result:
				t.Fatal("CopyActiveSelectionToClipboard returned before the callback settled")
			default:
			}
			h.render()
			if h.viewportHas(tc.want) {
				t.Fatalf("%q flashed before the callback settled: %q", tc.want, h.viewport())
			}

			letGo()
			if got := <-result; got != tc.wantRet {
				t.Fatalf("result = %v, want %v", got, tc.wantRet)
			}
			h.render()
			if !h.viewportHas(tc.want) {
				t.Fatalf("flash rows = %q, want %q", h.viewport(), tc.want)
			}
		})
	}
}

type copyDenied struct{}

func (copyDenied) Error() string { return "denied by host" }

var errCopyDenied error = copyDenied{}
