package tui

import "testing"

// Pi bordered-loader.ts:61-67: dispose() calls the wrapped loader's dispose() when it has one (a CancellableLoader) and otherwise its stop() (a plain Loader),
// so both kinds end their animation.
func TestBorderedLoaderDisposeStopsTheAnimationOfEitherLoaderKind(t *testing.T) {
	for _, cancellable := range []bool{false, true} {
		bl := NewBorderedLoader(nil, ActiveTheme(), "working", BorderedLoaderOptions{Cancellable: &cancellable})
		bl.loader.Start()
		running := func() bool {
			bl.loader.mu.Lock()
			defer bl.loader.mu.Unlock()
			return bl.loader.stopCh != nil
		}
		if !running() {
			t.Fatalf("cancellable=%t: Start did not begin the animation", cancellable)
		}
		bl.Dispose()
		if running() {
			t.Fatalf("cancellable=%t: Dispose left the animation running", cancellable)
		}
	}
}
