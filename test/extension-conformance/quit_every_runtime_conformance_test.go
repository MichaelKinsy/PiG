package extensionconformance

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const (
	// quitSilentNodeSource is a Node extension whose session_shutdown handler never settles and holds no timer and no I/O: the quit's dispose cannot return.
	quitSilentNodeSource = `export default function (pi) { pi.on("session_shutdown", () => new Promise(() => {})); }`
	// quitTimerNodeSource arms a 3 s timer in its session_shutdown handler and returns.
	quitTimerNodeSource = `export default function (pi) { pi.on("session_shutdown", () => { setTimeout(() => {}, 3000); }); }`
	// quitHeldWatch is how long a row watches a quit that must be held before it lets the holder go. A host that acts on one process's drained loop alone does so within milliseconds.
	quitHeldWatch = time.Second
)

// loadQuitNode loads source into host as a Node extension with the given isolation and returns its one session_shutdown handler.
func loadQuitNode(t *testing.T, host *subprocess.Host, name, source, isolation string) extension.HandlerFn {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".mjs")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: name, Source: path, Enabled: true, Isolation: isolation})
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	handlers := loaded.EventHandlers("session_shutdown")
	if len(handlers) != 1 {
		t.Fatalf("session_shutdown handlers of %s = %d, want 1", name, len(handlers))
	}
	return handlers[0]
}

// quitDrained installs the host's drain handler and returns a channel that closes when the host hands the drained loop to its owner, which is the owner's exit.
func quitDrained(host *subprocess.Host) <-chan struct{} {
	drained := make(chan struct{})
	var once sync.Once
	host.SetRuntimeDrainHandler(func() { once.Do(func() { close(drained) }) })
	return drained
}

// TestConformance_QuitWaitsForACommandInFlight pins what holds a quit whose dispose cannot return, for a command in flight in every SDK and in the isolated, packed
// and fused realizations. Pi 1.5.2 under tmux: a session_shutdown handler that never settles beside a command in flight on a TCP read: the process does not exit
// while the read waits (4 s watched), and exits 0 53 ms after the peer answers, with the command finished. Pi has one event loop, and the command's socket is a
// referenced handle of it.
//
// PiG runs the silent handler in a Node process of its own here. A Node extension's process reports its own drained loop, which the socket of its command keeps
// from draining; a Go, Rust, Python or Lisp runtime reports no loop, and the host counts the command it has not answered. Every row sees the same: the host does not
// hand the drained loop to its owner within the watch, and does once the command has returned.
func TestConformance_QuitWaitsForACommandInFlight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test done")
			})
			budget := testbudget.Wait(t)
			cmd, ok := findCommand(h.runner, "io_wait")
			if !ok {
				t.Fatal("io_wait command not registered")
			}
			silent := loadQuitNode(t, h.host, "quit-silent", quitSilentNodeSource, "isolated")
			drained := quitDrained(h.host)
			peer := newIOWaitPeer(t)
			done := make(chan error, 1)
			go func() { done <- cmd.Handler(context.WithoutCancel(t.Context()), peer.addr()) }()
			conn := peer.accept()
			h.host.EndInput()
			go func() { _, _ = silent(map[string]any{"type": "session_shutdown", "reason": "quit"}) }()
			select {
			case <-drained:
				t.Fatal("the host handed the drained loop to its owner while a command waited on its read: the quit was not held for the command in flight")
			case err := <-done:
				t.Fatalf("the command returned before the peer answered: %v", err)
			case <-time.After(quitHeldWatch):
			}
			if _, err := conn.Write([]byte("go\n")); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(budget):
				t.Fatal("the command did not return after the peer answered")
			}
			select {
			case <-drained:
			case <-time.After(budget):
				t.Fatal("the host never handed the drained loop to its owner after the command had finished: the quit was not evaluated again")
			}
		})
	}
}

// TestConformance_QuitLastsAsLongAsANodeTimer pins the length of the hold. Pi 1.5.2 under tmux: a session_shutdown handler that never settles beside an extension
// whose own session_shutdown handler armed a 3 s timer: the process exits 0 after 3.09 s: not at once, and not never. The packed row has both extensions in one Node
// process, whose one loop holds the timer as Pi's does; the isolated row has each in a process of its own, and the timer's process reports its drained loop when the
// timer ended.
func TestConformance_QuitLastsAsLongAsANodeTimer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node runtimes)")
	}
	for _, tc := range []struct{ name, isolation string }{{"isolated", "isolated"}, {"packed", "shared-ok"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := subprocess.NewHost(t.TempDir())
			host.SetMode("rpc")
			t.Cleanup(func() { host.Shutdown("test done") })
			drained := quitDrained(host)
			arm := loadQuitNode(t, host, "quit-timer", quitTimerNodeSource, tc.isolation)
			silent := loadQuitNode(t, host, "quit-silent", quitSilentNodeSource, tc.isolation)
			event := map[string]any{"type": "session_shutdown", "reason": "quit"}
			host.EndInput()
			// The timer is armed inside the first handler, so it ends no earlier than 3 s from here.
			before := time.Now()
			if _, err := arm(event); err != nil {
				t.Fatalf("the timer extension's session_shutdown: %v", err)
			}
			go func() { _, _ = silent(event) }()
			select {
			case <-drained:
			case <-time.After(testbudget.Wait(t) + 3*time.Second):
				t.Fatal("the host never handed the drained loop to its owner: the process whose timer ended did not report its drained loop")
			}
			if held := time.Since(before); held < 3*time.Second {
				t.Fatalf("the host handed the drained loop to its owner after %s while a 3 s timer was held, want no earlier than 3 s", held)
			}
		})
	}
}
