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

// unansweredSelectUI answers no dialog: stdin has ended, so nothing can.
type unansweredSelectUI struct {
	extension.UIContext
	asked chan struct{}
}

func (u *unansweredSelectUI) Select(ctx context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	close(u.asked)
	<-ctx.Done()
	return "", ctx.Err()
}

// drainIsolatedFixture runs two real isolated Node runtimes. The first has a quit session_shutdown handler that never settles. The second has the command under test. stdin ends while the command runs, then the quit handler starts, as an RPC shutdown does.
type drainIsolatedFixture struct {
	t       *testing.T
	host    *Host
	command extension.RegisteredCommand
	quit    *extension.Extension
	ctx     context.Context

	mu      sync.Mutex
	counts  []int
	changed chan struct{}
	drained chan struct{}
	done    chan error
}

func startDrainIsolated(t *testing.T, commandSource string, configure func(h *Host)) *drainIsolatedFixture {
	t.Helper()
	nodeCellRequireNode(t)
	dir := t.TempDir()
	quit := filepath.Join(dir, "quit.mjs")
	if err := os.WriteFile(quit, []byte(`export default function (pi) { pi.on("session_shutdown", async () => { await new Promise(() => {}); }); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "command.mjs")
	if err := os.WriteFile(command, []byte(commandSource), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &drainIsolatedFixture{t: t, changed: make(chan struct{}, 32), drained: make(chan struct{}), done: make(chan error, 1)}
	f.host = NewHost(t.TempDir())
	f.host.SetMode("rpc")
	t.Cleanup(func() { f.host.Shutdown("test done") })
	configure(f.host)
	f.host.SetCommandSuspendHandler(func(n int) {
		f.mu.Lock()
		f.counts = append(f.counts, n)
		f.mu.Unlock()
		f.changed <- struct{}{}
	})
	f.host.SetRuntimeDrainHandler(func() { close(f.drained) })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	f.ctx = ctx
	loaded, errs := f.host.LoadAll(ctx, []ExtConfig{
		{Name: "quit", Source: quit, Enabled: true, Isolation: "isolated"},
		{Name: "command", Source: command, Enabled: true, Isolation: "isolated"},
	})
	if len(errs) != 0 || len(loaded) != 2 {
		t.Fatalf("LoadAll = %d extensions, errors %v", len(loaded), errs)
	}
	for i := range loaded {
		ext := loaded[i]
		switch ext.Name {
		case "command":
			f.command = ext.Commands["cmd"]
		case "quit":
			f.quit = &loaded[i]
		}
	}
	if f.command.Handler == nil || f.quit == nil {
		t.Fatal("the fixture extensions registered no cmd command or quit extension")
	}
	return f
}

// last returns the newest suspended count.
func (f *drainIsolatedFixture) last() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.counts) == 0 {
		return -1
	}
	return f.counts[len(f.counts)-1]
}

func (f *drainIsolatedFixture) waitCount(want int) {
	f.t.Helper()
	for f.last() != want {
		select {
		case <-f.changed:
		case <-f.ctx.Done():
			f.mu.Lock()
			defer f.mu.Unlock()
			f.t.Fatalf("suspended count never reached %d: %v", want, f.counts)
		}
	}
}

// endInputAndQuit ends input, then starts the quit handler that never settles, and returns once the host applied the runtime's loop drain.
func (f *drainIsolatedFixture) endInputAndQuit() {
	f.t.Helper()
	f.host.EndInput()
	handlers := f.quit.EventHandlers("session_shutdown")
	if len(handlers) != 1 {
		f.t.Fatalf("session_shutdown handlers = %d, want 1", len(handlers))
	}
	go func() { _, _ = handlers[0](map[string]any{"type": "session_shutdown", "reason": "quit"}) }()
	select {
	case <-f.drained:
	case <-f.ctx.Done():
		f.t.Fatal("the quit handler's runtime never reported its drain")
	}
}

// Pi runs every extension in one event loop, so a quit handler that never settles ends the process only when the loop drains, after every command's output. PiG runs each isolated Node extension in a process of its own, so the host counts a command of another process able to answer until that process reports that nothing keeps its own loop alive. A command that awaits a session change keeps the loop alive; one that awaits a Promise nothing settles, or a dialog that stdin can no longer answer, does not.
func TestDrainSpansIsolatedNodeProcesses(t *testing.T) {
	t.Parallel()
	t.Run("session change keeps the loop alive", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		f := startDrainIsolated(t, `export default function (pi) { pi.registerCommand("cmd", { description: "replace the session", handler: async (_args, ctx) => { await ctx.newSession(); } }); }`, func(h *Host) {
			bridge := NewUIBridge(func() {})
			bridge.SetHostAction("newSession", func(ctx context.Context, _ *extension.NewSessionOptions) (extension.CancelledResult, error) {
				close(entered)
				extension.CallInitiated(ctx)
				<-release
				return extension.CancelledResult{}, nil
			})
			h.SetUIBridge(bridge)
		})
		go func() { f.done <- f.command.Handler(f.ctx, "") }()
		<-entered
		// The command's window closed on the pending session change. Its runtime reports it suspended once input ended.
		f.host.EndInput()
		f.waitCount(1)
		f.endInputAndQuit()
		f.waitCount(0)
		close(release)
		if err := <-f.done; err != nil {
			t.Fatalf("command: %v", err)
		}
	})

	t.Run("a Promise nothing settles", func(t *testing.T) {
		f := startDrainIsolated(t, `export default function (pi) { pi.registerCommand("cmd", { description: "await nothing", handler: async () => { await new Promise(() => {}); } }); }`, func(*Host) {})
		go func() { f.done <- f.command.Handler(f.ctx, "") }()
		f.endInputAndQuit()
		// Nothing keeps the command's process alive, so it reports its own drain and the host stops waiting for the command.
		f.waitCount(1)
	})

	t.Run("a dialog stdin cannot answer", func(t *testing.T) {
		asked := make(chan struct{})
		f := startDrainIsolated(t, `export default function (pi) { pi.registerCommand("cmd", { description: "await a select", handler: async (_args, ctx) => { await ctx.ui.select("Ask", ["yes"]); } }); }`, func(h *Host) {
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(&unansweredSelectUI{UIContext: extension.NoopUIContext, asked: asked})
			h.SetUIBridge(bridge)
		})
		go func() { f.done <- f.command.Handler(f.ctx, "") }()
		<-asked
		f.endInputAndQuit()
		f.waitCount(1)
	})
}
