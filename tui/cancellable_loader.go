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

// NewCancellableLoader creates a cancellable loader.
func NewCancellableLoader(spinnerColor, messageColor, message string, frames []string) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &CancellableLoader{
		Loader: *NewStyledLoader(spinnerColor, messageColor, message, frames),
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

// Dispose stops loader animation ownership. Loader animation is host-driven in
// Pig, so there is no local timer to stop and cancellation state is unchanged.
func (*CancellableLoader) Dispose() {}
