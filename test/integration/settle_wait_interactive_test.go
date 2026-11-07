//go:build integration

package integration

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// settleWaitPeer is the TCP peer of the coding/cli/testdata/rpc-settle-wait extensions: a handler or command of theirs dials it, announces itself with a "waiting" line and returns when the peer writes a line back.
type settleWaitPeer struct {
	t  *testing.T
	ln net.Listener
}

func newSettleWaitPeer(t *testing.T) *settleWaitPeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return &settleWaitPeer{t: t, ln: ln}
}

// accept returns the connection of the next waiting handler once it announced itself, so the handler is known to be inside its wait.
func (p *settleWaitPeer) accept() net.Conn {
	p.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	_ = p.ln.(*net.TCPListener).SetDeadline(deadline)
	conn, err := p.ln.Accept()
	if err != nil {
		p.t.Fatalf("no handler dialed the peer: %v", err)
	}
	p.t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(deadline)
	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "waiting\n" {
		p.t.Fatalf("handler announced %q, %v; want \"waiting\"", line, err)
	}
	return conn
}

// settleWaitExtensions returns the -e argument of each realization of coding/cli/testdata/rpc-settle-wait: the Node, Python and Lisp modules, and the Go and Rust executables built from their sources.
func settleWaitExtensions(t *testing.T) map[string]string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	testdata := filepath.Join(root, "coding", "cli", "testdata")
	for _, tool := range []string{"go", "cargo", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required for the SDK realizations: %v", tool, err)
		}
	}
	if _, err := exec.LookPath(toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)); err != nil {
		t.Fatalf("sbcl is required for the Lisp SDK realization: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	goBin := filepath.Join(t.TempDir(), "rpc-settle-wait-go")
	build := exec.CommandContext(ctx, "go", "build", "-o", goBin, ".")
	build.Dir = filepath.Join(testdata, "rpc-settle-wait-go")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rpc-settle-wait-go: %v\n%s", err, out)
	}
	target := t.TempDir()
	cargo := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
	cargo.Dir = filepath.Join(testdata, "rpc-settle-wait-rs")
	cargo.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	if out, err := cargo.CombinedOutput(); err != nil {
		t.Fatalf("build rpc-settle-wait-rs: %v\n%s", err, out)
	}
	return map[string]string{
		"node":   filepath.Join(testdata, "rpc-settle-wait.mjs"),
		"go":     goBin,
		"rust":   filepath.Join(target, "release", "rpc-settle-wait-rs"),
		"python": filepath.Join(testdata, "rpc-settle-wait.py"),
		"lisp":   filepath.Join(testdata, "rpc-settle-wait.lisp"),
	}
}

// Pi's interactive mode awaits an agent_settled handler and a command in its run's own continuation (agent-session.ts:1094-1105, interactive-mode.ts onSubmit), while its UI keeps reading keys and painting: text typed meanwhile appears in the editor. The handler and the command wait on a plain TCP read in every SDK, which the host can tell from a handler that computes only by its age. Pig's UI must not wait for either.
func TestInteractiveEditorStaysLiveWhileSettleHandlerOrCommandWaits(t *testing.T) {
	extensions := settleWaitExtensions(t)
	for _, trigger := range []struct{ name, input string }{{"settled", "hello"}, {"command", "/hold"}} {
		configs := []systemConfig{}
		for _, sdk := range []string{"node", "go", "rust", "python", "lisp"} {
			cfg := deterministicGopiConfig(t)
			cfg.name = "pig-" + sdk
			cfg.args = append(cfg.args, "-e", extensions[sdk])
			configs = append(configs, cfg)
		}
		pi := deterministicUpstreamConfig(t)
		pi.name = "pi"
		pi.args = append(pi.args, "-e", extensions["node"])
		for _, cfg := range append([]systemConfig{pi}, configs...) {
			t.Run(trigger.name+"/"+cfg.name, func(t *testing.T) {
				peer := newSettleWaitPeer(t)
				cfg.env = append(cfg.env, "RPC_SETTLE_ADDR="+peer.ln.Addr().String())
				ready := interactiveReadyMarker
				if cfg.name == "pi" {
					ready = "Press ctrl+o to show full startup help"
				}
				session, cleanup := launchSystemWithReady(t, cfg, 60*time.Second, ready)
				defer cleanup()
				time.Sleep(800 * time.Millisecond)
				if err := tmuxCommand("send-keys", "-t", session, "-l", trigger.input).Run(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(300 * time.Millisecond)
				if err := tmuxCommand("send-keys", "-t", session, "Enter").Run(); err != nil {
					t.Fatal(err)
				}
				conn := peer.accept()
				if err := tmuxCommand("send-keys", "-t", session, "-l", "zzqx").Run(); err != nil {
					t.Fatal(err)
				}
				if pane, ok := waitForPaneContains(session, 10*time.Second, "zzqx"); !ok {
					t.Fatalf("%s: the editor did not show text typed while the %s waits:\n%s", cfg.name, trigger.name, pane)
				}
				if _, err := conn.Write([]byte("go\n")); err != nil {
					t.Fatal(err)
				}
				quietExit(t, session)
			})
		}
	}
}
