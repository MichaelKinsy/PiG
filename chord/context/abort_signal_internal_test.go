package chordctx

import "testing"

// signalContext answers context.Cause with its signal's reason by taking the key Cause asks with from the standard library
// once, not from a name: if a Go release changes how Cause asks, this fails instead of the cause turning into Canceled.
func TestCancelCtxKeyIsFound(t *testing.T) {
	if cancelCtxKey == nil {
		t.Fatal("context.Cause no longer asks a context for its cancellation record through Value: signalContext cannot give its signal's cause")
	}
}
