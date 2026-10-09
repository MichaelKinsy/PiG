package codingagent

// pi: packages/coding-agent/src/modes/interactive/tui-renderer.ts

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/test/suite/regressions/5724-sigterm-signal-exit.test.ts:52.
// The process-level signal guard covers listener retention; this pins the original deferred-disposal order at the terminal owner.
func TestSignalShutdownDefersTerminalCleanupUntilDisposalCompletesUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order []string
		mode := upstreamShutdownMode(t, &order, false)
		started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		mode.newRunner = inproc.NewRunner([]extension.Extension{{Path: "cleanup", Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(...any) (any, error) {
				order = append(order, "dispose")
				close(started)
				<-release
				return nil, nil
			}},
		}}}, t.TempDir())
		go func() {
			mode.ShutdownFromSignal()
			done <- mode.finishInteractiveShutdown()
		}()
		<-started
		synctest.Wait()
		if !slices.Equal(order, []string{"dispose"}) {
			t.Errorf("pending cleanup order=%v", order)
		}
		unblock()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(order, []string{"dispose", "drainInput", "stop"}) {
			t.Fatalf("finished cleanup order=%v", order)
		}
	})
}
