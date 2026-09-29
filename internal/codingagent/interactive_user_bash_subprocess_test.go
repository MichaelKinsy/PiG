package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// Cancellation crosses the actual Node socket in both topologies, rather than
// only cancelling the Go reference callback's context.
func TestUserBashNodeOwner(t *testing.T) {
	for _, packed := range []bool{false, true} {
		name := "isolated"
		if packed {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "owner.mjs")
			if err := os.WriteFile(source, []byte(`import {writeFileSync} from "node:fs";
import {join} from "node:path";
writeFileSync(new URL("./owner.pid", import.meta.url), String(process.pid));
export default pi => pi.on("user_bash", async (event, ctx) => {
  const signal = ctx.signal;
  const cancelled = new Promise(resolve => signal.addEventListener("abort", resolve, {once:true}));
  writeFileSync(join(event.cwd, "started"), ctx.ui.getEditorText());
  await cancelled;
  writeFileSync(join(event.cwd, "cancelled"), String(signal.aborted));
  return {result:{output:"late result",exitCode:7,cancelled:false,truncated:false}};
});`), 0o600); err != nil {
				t.Fatal(err)
			}
			h := subprocess.NewHostWithConfigRoot(root, t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			cfg := subprocess.ExtConfig{Name: "owner", Source: source, Enabled: true}
			var exts []extension.Extension
			if packed {
				peer := filepath.Join(root, "peer.mjs")
				if err := os.WriteFile(peer, []byte(`import {writeFileSync} from "node:fs"; export default () => {writeFileSync(new URL("./peer.pid", import.meta.url), String(process.pid));};`), 0o600); err != nil {
					t.Fatal(err)
				}
				var errs []error
				exts, errs = h.LoadAll(t.Context(), []subprocess.ExtConfig{cfg, {Name: "peer", Source: peer, Enabled: true}})
				if len(errs) != 0 {
					t.Fatal(errs)
				}
				ownerPID, ownerErr := os.ReadFile(filepath.Join(root, "owner.pid"))
				peerPID, peerErr := os.ReadFile(filepath.Join(root, "peer.pid"))
				if ownerErr != nil || peerErr != nil || string(ownerPID) != string(peerPID) {
					t.Fatalf("members are not packed: owner=%s/%v peer=%s/%v", ownerPID, ownerErr, peerPID, peerErr)
				}
			} else {
				ext, err := h.Load(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				exts = []extension.Extension{*ext}
			}
			m := newRunOnMainProbe(t)
			m.pendingMessagesContainer = tui.NewContainer()
			m.newRunner = inproc.NewRunner(exts, root)
			m.layout = m.chatContainer
			m.wireInprocContextActions()
			m.editor.SetText("owner editor value")
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(&ExtUIContext{m: m})
			h.SetUIBridge(bridge)
			m.opts.CWD = root
			ctx, cancel := context.WithCancel(t.Context())
			m.runCtx, m.backgroundCtx = ctx, ctx
			m.abortCtx, m.abortFn = context.WithCancel(ctx)
			loopDone := make(chan struct{})
			go m.drainLoop(ctx, loopDone)
			defer func() { cancel(); h.Shutdown("test done"); <-loopDone; m.backgroundTasks.Wait() }()
			m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "must-not-run", false) })
			waitUserBashFile(t, filepath.Join(root, "started"), "owner editor value")
			// A key must still reach the owner while the real Node callback waits.
			typed := make(chan string, 1)
			m.runOnMain(ctx, func() { _ = m.handleKey(ctx, "x"); typed <- m.editor.Text(); m.abortFn() })
			select {
			case text := <-typed:
				if text != "owner editor valuex" {
					t.Fatalf("editor=%q", text)
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatal("Node hook blocked input owner")
			}
			waitUserBashFile(t, filepath.Join(root, "cancelled"), "true")
			m.backgroundTasks.Wait()
			if len(m.bashOrder) != 0 {
				t.Fatal("cancelled Node result created a block")
			}
		})
	}
}

func waitUserBashFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.NewTimer(testbudget.Wait(t))
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("%s: got %q, err=%v; want %q", path, data, err, want)
		case <-tick.C:
		}
	}
}
