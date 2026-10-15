package tui

import "testing"

// cancellable-loader.ts handleInput: the configured tui.select.cancel binding aborts the signal and calls onAbort on every press
// (abort() is idempotent, onAbort is not guarded); any other key does nothing, including the default Escape once cancel is remapped.
func TestCancellableLoaderCancelFollowsTheBindingAndCallsOnAbortEachPress(t *testing.T) {
	withBindings(t, map[string][]string{KBSelectCancel: {"ctrl+x"}})
	loader := NewCancellableLoader(nil, nil, nil, "loading", nil)
	aborts := 0
	loader.OnAbort = func() { aborts++ }
	loader.HandleInput("\x1b")
	if aborts != 0 || loader.Aborted() {
		t.Fatalf("default Escape cancelled a loader whose cancel binding is ctrl+x: aborts %d aborted %v", aborts, loader.Aborted())
	}
	loader.HandleInput("\x18")
	loader.HandleInput("\x18")
	if aborts != 2 || !loader.Aborted() || loader.Signal().Err() == nil {
		t.Fatalf("two cancel presses: aborts %d aborted %v (onAbort runs on each press)", aborts, loader.Aborted())
	}
}

func TestCancellableLoaderCancelWithoutOnAbortOnlyAborts(t *testing.T) {
	loader := NewCancellableLoader(nil, nil, nil, "loading", nil)
	loader.HandleInput("\x1b")
	if !loader.Aborted() {
		t.Fatal("cancel without an onAbort callback did not abort the signal")
	}
}
