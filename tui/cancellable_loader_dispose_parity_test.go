package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCancellableLoaderDisposeParity emits the observation test/parity/scenarios/tui-components/27-cancellable-loader-dispose.toml compares with Pi
// 1.1.0's CancellableLoader.dispose() (test/parity/testdata/cancellable-loader-dispose-pi.mjs): the animation runs, dispose freezes it, and neither the abort
// state nor onAbort changes.
func TestCancellableLoaderDisposeParity(t *testing.T) {
	loader := NewCancellableLoader(nil, nil, nil, "working", nil)
	aborts := 0
	loader.OnAbort = func() { aborts++ }
	// Pi's constructor starts the animation (setIndicator calls start); Pig's frames are driven by the host's Tick, or by Start, so the probe starts it.
	loader.Start()
	render := func() string { return strings.Join(loader.Render(40), "\n") }
	first := render()
	time.Sleep(250 * time.Millisecond)
	animated := render() != first
	loader.Dispose()
	stopped := render()
	time.Sleep(250 * time.Millisecond)
	frozen := render() == stopped
	loader.Dispose()
	fmt.Printf("cancellable-loader-observation:%s\n", (&observation{}).add("animated", animated).add("frozenAfterDispose", frozen).add("aborted", loader.Aborted()).add("onAbortCalls", aborts))
}
