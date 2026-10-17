package tui

// cancellable_loader.go: escape-cancellable loader.
//
// Ports upstream packages/tui/src/components/cancellable-loader.ts.

import "context"

// CancellableLoader extends Loader with Esc-key cancellation.
type CancellableLoader struct {
	Loader
	cancel  context.CancelFunc
	ctx     context.Context
	OnAbort func()
}

// NewCancellableLoader is `new CancellableLoader(ui, spinnerColorFn, messageColorFn, message, indicator)` (components/cancellable-loader.ts, which inherits Loader's constructor).
func NewCancellableLoader(ui TUI, spinnerColorFn, messageColorFn func(string) string, message string, indicator *LoaderIndicatorOptions) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &CancellableLoader{
		Loader: *NewLoader(ui, spinnerColorFn, messageColorFn, message, indicator),
		cancel: cancel,
		ctx:    ctx,
	}
}

// Context returns the cancellation context.
func (cl *CancellableLoader) Context() context.Context { return cl.ctx }

// Aborted returns true if cancelled.
func (cl *CancellableLoader) Aborted() bool { return cl.ctx.Err() != nil }

// HandleInput cancels on the registry-bound tui.select.cancel key.
func (cl *CancellableLoader) HandleInput(data string) {
	kb := GetTUIKeybindings()
	if !kb.Matches(data, KBSelectCancel) {
		return
	}
	cl.cancel()
	if cl.OnAbort != nil {
		cl.OnAbort()
	}
}

// Signal returns the underlying cancellation context.
func (cl *CancellableLoader) Signal() context.Context { return cl.ctx }

// Dispose is upstream dispose() (cancellable-loader.ts:37): it stops the animation timer. The abort state and OnAbort are untouched.
func (cl *CancellableLoader) Dispose() { cl.Stop() }
