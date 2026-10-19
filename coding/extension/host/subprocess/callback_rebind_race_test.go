package subprocess

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// The wiring layer binds the host's crash and geometry callbacks again on every reload while the host's connection, recovery and handshake goroutines read them. A rebind must not race a read, and a read sees a whole binding: some handler, or the one registered before it.
func TestHostCallbacksRebindWhileHostGoroutinesRead(t *testing.T) {
	h := NewHost(t.TempDir())
	transport := &TransportError{Extension: "failed", Operation: "read", Err: io.EOF}
	h.SetCrashHandler(func(string, time.Duration, bool, string) {})
	h.SetWidthFunc(func() int { return 80 })
	h.SetHeightFunc(func() int { return 24 })

	const rounds = 500
	var readers sync.WaitGroup
	readers.Go(func() {
		for range rounds {
			// invocationError reads the crash handler as the host's command and event paths do.
			if _, owned := errors.AsType[*invocation.LifecycleError](h.invocationError(transport)); !owned {
				t.Error("a transport failure lost its lifecycle owner while the crash handler was rebound")
				return
			}
		}
	})
	readers.Go(func() {
		for range rounds {
			// readyGeometry reads both geometry callbacks as a handshake does.
			if width, height := h.readyGeometry(); width < 80 || width > 84 || height < 24 || height > 28 {
				t.Errorf("ready geometry %dx%d is no binding's size", width, height)
				return
			}
		}
	})
	for i := range rounds {
		h.SetCrashHandler(func(string, time.Duration, bool, string) {})
		h.SetWidthFunc(func() int { return 80 + i%5 })
		h.SetHeightFunc(func() int { return 24 + i%5 })
	}
	readers.Wait()
}
