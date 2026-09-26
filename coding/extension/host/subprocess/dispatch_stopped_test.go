package subprocess

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A handler call that fails because the host stopped the extension (its
// owner's context ended, or a shutdown began) is marked as stopped by the
// host, so the runner does not report it; any other failure is the
// handler's own.
func TestDispatchErrorMarksCallsTheHostStopped(t *testing.T) {
	failure := errors.New("event session_start: context canceled")
	live := &managedExt{host: &Host{}, parentCtx: context.Background()}
	if err := live.dispatchError(failure); errors.Is(err, extension.ErrHandlerStopped) || !errors.Is(err, failure) {
		t.Fatalf("running extension: dispatchError = %v, want the failure unchanged", err)
	}
	owner, cancel := context.WithCancel(context.Background())
	cancel()
	for name, me := range map[string]*managedExt{
		"owner cancelled":    {host: &Host{}, parentCtx: owner},
		"extension stopping": func() *managedExt { me := &managedExt{host: &Host{}}; me.shuttingDown.Store(true); return me }(),
		"host stopping":      func() *managedExt { h := &Host{}; h.shuttingDown.Store(true); return &managedExt{host: h} }(),
	} {
		if err := me.dispatchError(failure); !errors.Is(err, extension.ErrHandlerStopped) || !errors.Is(err, failure) {
			t.Errorf("%s: dispatchError = %v, want the failure marked as stopped by the host", name, err)
		}
	}
}
