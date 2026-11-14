//go:build linux

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// quitHeldWatch is how long a row watches a quit that must be held before it lets the holder go.
const quitHeldWatch = time.Second

// TestInteractiveQuitWaitsForACommandInFlight: the user quits interactive mode (ctrl+d) while an extension command is in flight on a TCP read (the rpc-settle-wait
// fixtures: /hold dials a peer and returns when the peer answers) and another extension's session_shutdown handler never settles, so the quit's dispose cannot return.
//
// Pi 1.5.2, run under tmux with rpc-settle-wait.mjs and the same silent handler: the process does not exit while the command waits (4 s watched): its socket is a
// referenced handle of the one event loop. 53 ms after the peer answers, the command has finished (it writes its marker file) and the process exits 0, with no resume
// hint. Without the silent handler the dispose returns and Pi exits at once (96 ms) with the command unfinished.
//
// pig runs the silent handler in the Node runtime process and a Go, Rust, Python or Lisp command in a process of its own, which has no event loop that could report
// what it holds. The host sees the equivalent itself: a command that such a runtime has not answered. So the quit waits for it, and it is evaluated again when the
// command ends. pig exited 0 92 to 97 ms after the key with the command unfinished: it acted on the drained loop of the Node process alone.
//
// The Node row runs the command in the process of the silent handler (two factory files pack into one cell): that process's own loop holds the socket.
func TestInteractiveQuitWaitsForACommandInFlight(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	fixture := func(name string) func(*testing.T) string {
		return func(t *testing.T) string {
			t.Helper()
			path, err := filepath.Abs(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			return path
		}
	}
	rows := []struct {
		bridge    string
		extension func(*testing.T) string
	}{
		{"node-packed", fixture("rpc-settle-wait.mjs")},
		{"go", func(t *testing.T) string { return buildGoSDKFixture(t, "rpc-settle-wait-go") }},
		{"rust", buildRustSettleWait},
		{"python", fixture("rpc-settle-wait.py")},
		{"lisp", fixture("rpc-settle-wait.lisp")},
	}
	for _, tc := range rows {
		t.Run(tc.bridge, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			agent, project, sessions, done := filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "sessions"), filepath.Join(root, "command-done")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			seedFirstRunDone(t, agent)
			// No request is sent: the model only has to exist.
			models := `{"providers":{"localhold":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"dummy","models":[{"id":"hold-model"}]}}}`
			if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
				t.Fatal(err)
			}
			silent := filepath.Join(root, "shutdown-never.mjs")
			if err := os.WriteFile(silent, []byte(quitProbeShutdownNever), 0o600); err != nil {
				t.Fatal(err)
			}
			peer := newRPCSettlePeer(t)
			argv := []string{binary, "--no-extensions", "--model", "localhold/hold-model", "--session-dir", sessions, "-e", tc.extension(t), "-e", silent}
			env := []string{"HOME=" + root, "PIG_HOME=" + root, "PI_HOME=" + root, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1", "PI_OFFLINE=1",
				"RPC_SETTLE_ADDR=" + peer.addr(), "RPC_SETTLE_DONE=" + done}
			p := startQuitPane(t, project, filepath.Join(root, "stderr.log"), env, argv...)
			p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })
			p.text("/hold")
			p.await("the typed /hold", func() bool { return strings.Contains(p.screen(), "/hold") })
			p.keys("Enter")
			held := peer.accept()
			pig := p.pid()
			children := childProcesses(pig)
			if len(children) == 0 {
				t.Fatalf("pig %d has no extension process", pig)
			}
			p.keys("C-d")
			p.await("the stopped TUI", p.cooked)
			// The watch: the command's connection stays open and silent while pig holds the quit. A pig that acts on the Node process's drained loop alone exits about
			// 95 ms after the key and ends the extension processes, which closes the connection; an answer sent before that would let the command finish first and hide
			// the early exit. Pi was watched held for 4 s; the second here is ten times the early exit's delay.
			if err := held.SetReadDeadline(time.Now().Add(quitHeldWatch)); err != nil {
				t.Fatal(err)
			}
			if n, err := held.Read(make([]byte, 16)); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("the connection of the command in flight ended or spoke within %s of the quit (%d bytes, %v): pig did not hold the quit for it", quitHeldWatch, n, err)
			}
			if err := held.SetReadDeadline(time.Time{}); err != nil {
				t.Fatal(err)
			}
			if status, exited := p.status(); exited {
				t.Fatalf("pig exited (status %d) while the command in flight had not finished", status)
			}
			// The peer answers: the command finishes, nothing is in flight any more, and the pending dispose ends in the exit.
			_, answerErr := held.Write([]byte("go\n"))
			status := 0
			p.await("pig's exit after the command finished", func() bool {
				code, exited := p.status()
				status = code
				return exited
			})
			if status != 0 {
				t.Errorf("pig exited with status %d, want 0", status)
			}
			if _, err := os.Stat(done); err != nil {
				t.Errorf("pig exited before the command in flight had finished: its marker is missing (%v; the answer to its read: %v)", err, answerErr)
			}
			budget := testbudget.Wait(t)
			deadline := time.Now().Add(budget)
			for _, child := range children {
				for !errors.Is(syscall.Kill(child, 0), syscall.ESRCH) {
					if time.Now().After(deadline) {
						t.Errorf("the child process %d of pig is alive %s after pig exited", child, budget)
						_ = syscall.Kill(child, syscall.SIGKILL)
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			if strings.Contains(p.screen(), "To resume this session") {
				t.Errorf("pig printed the resume hint of a completed dispose, though the dispose never returned:\n%s", p.screen())
			}
		})
	}
}
