//go:build unix

package codingagent

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi 0.87.1 interactive-mode.ts:4146-4157 awaits disposal without a deadline.
func TestHUPAwaitsCleanupAndRequestsOwnerTeardown(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "slow-cleanup", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) {
			time.Sleep(1500 * time.Millisecond)
			order = append(order, "dispose")
			return nil, nil
		}},
	}}}, t.TempDir())
	m.ShutdownFromSignal()
	if !m.requestExit.Load() || !slices.Equal(order, []string{"dispose"}) {
		t.Fatalf("handoff before cleanup: exit=%v order=%v", m.requestExit.Load(), order)
	}
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"dispose", "drainInput", "stop"}; !slices.Equal(order, want) {
		t.Fatalf("order=%v; want %v", order, want)
	}
}
