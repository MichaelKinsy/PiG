package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi runs every extension in one event loop, so the loop drains only after a command awaiting ctx.newSession() has written its output, in whichever extension it runs. PiG runs an isolated Node extension in its own process: that process reports its window closed while the host builds the replacement, and the drain report of another process (a quit handler that never settles) has no ordering with the command's response. From the drain checkpoint the host must therefore count the command as able to answer.
func TestDrainAwaitsIsolatedNodeCommandThatWaitsForSessionChange(t *testing.T) {
	nodeCellRequireNode(t)
	entry := filepath.Join(t.TempDir(), "ns2.mjs")
	if err := os.WriteFile(entry, []byte(`export default function (pi) { pi.registerCommand("ns2", { description: "replace the session", handler: async (_args, ctx) => { await ctx.newSession(); } }); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	h.SetMode("rpc")
	t.Cleanup(func() { h.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	entered := make(chan struct{})
	release := make(chan struct{})
	bridge.SetHostAction("newSession", func(ctx context.Context, _ *extension.NewSessionOptions) (extension.CancelledResult, error) {
		close(entered)
		extension.CallInitiated(ctx)
		<-release
		return extension.CancelledResult{}, nil
	})
	h.SetUIBridge(bridge)
	var mu sync.Mutex
	var counts []int
	changed := make(chan struct{}, 8)
	h.SetCommandSuspendHandler(func(n int) {
		mu.Lock()
		counts = append(counts, n)
		mu.Unlock()
		changed <- struct{}{}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, []ExtConfig{{Name: "ns2", Source: entry, Enabled: true, Isolation: "isolated"}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("LoadAll = %d extensions, errors %v", len(loaded), errs)
	}
	command := loaded[0].Commands["ns2"]
	done := make(chan error, 1)
	go func() { done <- command.Handler(ctx, "") }()
	<-entered
	waitCount := func(want int) {
		t.Helper()
		for {
			mu.Lock()
			got := -1
			if len(counts) > 0 {
				got = counts[len(counts)-1]
			}
			mu.Unlock()
			if got == want {
				return
			}
			select {
			case <-changed:
			case <-ctx.Done():
				t.Fatalf("suspended count never reached %d: %v", want, counts)
			}
		}
	}
	h.EndInput()
	waitCount(1)
	h.markLoopDrained()
	waitCount(0)
	// The never-settling quit handler of another process keeps Pi's loop, and so the response, alive.
	h.setQuitHandlerSuspended()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("command: %v", err)
	}
}
