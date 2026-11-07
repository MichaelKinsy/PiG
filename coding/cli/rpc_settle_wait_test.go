package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// rpcSettlePeer is the TCP peer of the testdata/rpc-settle-wait extensions: a handler or command of theirs dials it, announces itself with a "waiting" line and returns when the peer writes a line back.
type rpcSettlePeer struct {
	t  *testing.T
	ln net.Listener
}

func newRPCSettlePeer(t *testing.T) *rpcSettlePeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return &rpcSettlePeer{t: t, ln: ln}
}

func (p *rpcSettlePeer) addr() string { return p.ln.Addr().String() }

// accept returns the connection of the next waiting handler once it announced itself, so the handler is known to be inside its wait.
func (p *rpcSettlePeer) accept() net.Conn {
	p.t.Helper()
	deadline := time.Now().Add(testbudget.Wait(p.t))
	if tcp, ok := p.ln.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(deadline)
	}
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

// rpcSettleImplementation starts testdata/rpc-settle-wait in one SDK realization, or Pi itself, with the peer at addr.
type rpcSettleImplementation struct {
	name  string
	start func(t *testing.T, env ...string) *rpcProcess
}

func startRPCSettleWaitPig(t *testing.T, extension string, extraEnv []string) *rpcProcess {
	t.Helper()
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"}
	env = append(env, extraEnv...)
	return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", extension)
}

func startRPCSettleWaitPi(t *testing.T, extraEnv ...string) *rpcProcess {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("Pi version = %q, %v", pkg.Version, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent")}
	env = append(env, extraEnv...)
	args := []string{filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1",
		"-e", filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts"), "-e", filepath.Join(root, "coding", "cli", "testdata", "rpc-settle-wait.mjs")}
	return startJSONLProcessAt(t, t.TempDir(), env, node, args...)
}

func rpcSettleImplementations(t *testing.T) []rpcSettleImplementation {
	t.Helper()
	node, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	python, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.py"))
	if err != nil {
		t.Fatal(err)
	}
	lisp, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	return []rpcSettleImplementation{
		{"pi", startRPCSettleWaitPi},
		{"pig-node", func(t *testing.T, env ...string) *rpcProcess { return startRPCSettleWaitPig(t, node, env) }},
		{"pig-go", func(t *testing.T, env ...string) *rpcProcess {
			return startRPCSettleWaitPig(t, buildGoSDKFixture(t, "rpc-settle-wait-go"), env)
		}},
		{"pig-rust", func(t *testing.T, env ...string) *rpcProcess {
			return startRPCSettleWaitPig(t, buildRustSettleWait(t), env)
		}},
		{"pig-python", func(t *testing.T, env ...string) *rpcProcess { return startRPCSettleWaitPig(t, python, env) }},
		{"pig-lisp", func(t *testing.T, env ...string) *rpcProcess { return startRPCSettleWaitPig(t, lisp, env) }},
	}
}

// buildRustSettleWait builds testdata/rpc-settle-wait-rs and returns the executable.
func buildRustSettleWait(t *testing.T) string {
	t.Helper()
	target := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	cargo := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
	cargo.Dir = filepath.Join("testdata", "rpc-settle-wait-rs")
	cargo.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	if out, err := cargo.CombinedOutput(); err != nil {
		t.Fatalf("build rpc-settle-wait-rs: %v\n%s", err, out)
	}
	return filepath.Join(target, "release", testExecutable("rpc-settle-wait-rs"))
}

// An agent_settled handler that waits on a socket is awaiting I/O, so Pi's event loop reads stdin meanwhile: a get_state sent on seeing agent_end is answered before agent_settled, which follows only when the handler returns (agent-session.ts:1094-1105 awaits the handler before it emits agent_settled; rpc-mode.ts:808-810 starts every stdin line without waiting for the one before). Every SDK's handler waits on the same read, and the host cannot tell what the read is.
func TestRPCCommandDuringSettleHandlerNetworkWaitAnswersAcrossSDKs(t *testing.T) {
	for _, impl := range rpcSettleImplementations(t) {
		t.Run(impl.name, func(t *testing.T) {
			peer := newRPCSettlePeer(t)
			p := impl.start(t, "RPC_SETTLE_ADDR="+peer.addr())
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
			conn := peer.accept()
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var seen []string
			p.await("the get_state response while agent_settled waits", func(r rpcRecord) bool {
				seen = append(seen, fmt.Sprint(r["type"]))
				return r["id"] == "s"
			})
			if want := []string{"response"}; !slices.Equal(seen, want) {
				t.Fatalf("records after agent_end = %v, want %v\n%s", seen, want, p.stderr.String())
			}
			if _, err := conn.Write([]byte("go\n")); err != nil {
				t.Fatal(err)
			}
			p.await("agent_settled after the handler returned", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
			p.closeAndWait("after agent_settled")
		})
	}
}

// A command that waits on a socket is awaiting I/O too: Pi reads the next stdin line meanwhile, so get_state is answered before the command's own response, which follows when the command returns (rpc-mode.ts:394-413, 808-810).
func TestRPCCommandDuringCommandNetworkWaitAnswersAcrossSDKs(t *testing.T) {
	for _, impl := range rpcSettleImplementations(t) {
		t.Run(impl.name, func(t *testing.T) {
			peer := newRPCSettlePeer(t)
			p := impl.start(t, "RPC_SETTLE_ADDR="+peer.addr())
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "h", "type": "prompt", "message": "/hold"})
			conn := peer.accept()
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var seen []string
			p.await("the get_state response while the command waits", func(r rpcRecord) bool {
				seen = append(seen, fmt.Sprint(r["type"]))
				return r["id"] == "s"
			})
			if want := []string{"response"}; !slices.Equal(seen, want) {
				t.Fatalf("records before the command returned = %v, want %v\n%s", seen, want, p.stderr.String())
			}
			if _, err := conn.Write([]byte("go\n")); err != nil {
				t.Fatal(err)
			}
			p.await("the command's response", func(r rpcRecord) bool { return r["id"] == "h" })
			p.closeAndWait("after the command returned")
		})
	}
}

// An agent_settled handler that returns at once is Pi's microtask-only tail: no stdin line is read before agent_settled is written, so a get_state sent on seeing agent_end is answered after it (agent-session.ts:1094-1105, rpc-mode.ts:808-810). A runtime that cannot tell a wait from computation must not take such a handler for a wait.
func TestRPCCommandAfterAgentEndAnswersAfterFastSettleHandlerAcrossSDKs(t *testing.T) {
	for _, impl := range rpcSettleImplementations(t) {
		t.Run(impl.name, func(t *testing.T) {
			p := impl.start(t)
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var seen []string
			p.await("the get_state response", func(r rpcRecord) bool {
				seen = append(seen, fmt.Sprint(r["type"]))
				return r["id"] == "s"
			})
			if want := []string{"agent_settled", "response"}; !slices.Equal(seen, want) {
				t.Fatalf("records after agent_end = %v, want %v\n%s", seen, want, p.stderr.String())
			}
			p.closeAndWait("after the get_state response")
		})
	}
}

// An input handler that waits on a socket is awaiting I/O: Pi's prompt() awaits the handler inside its own continuation while its RPC loop reads the next stdin line (agent-session.ts prompt(), rpc-mode.ts:808-810), so get_state is answered before the run starts, and the run follows when the handler returns. In Pig, rpcAwaitHandler admits the next line only once the handler suspended or finished, so a handler of a runtime that cannot report its wait needs the quantum for it. Pig's Node runtime does not report a wait for an input handler in RPC mode (D86), so its leg runs only under Pi and the Go, Rust, Python and Lisp SDKs here.
func TestRPCCommandDuringInputHandlerNetworkWaitAnswersAcrossSDKs(t *testing.T) {
	for _, impl := range rpcSettleImplementations(t) {
		if impl.name == "pig-node" {
			continue
		}
		t.Run(impl.name, func(t *testing.T) {
			peer := newRPCSettlePeer(t)
			p := impl.start(t, "RPC_SETTLE_INPUT_ADDR="+peer.addr())
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			conn := peer.accept()
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var seen []string
			p.await("the get_state response while the input handler waits", func(r rpcRecord) bool {
				seen = append(seen, fmt.Sprint(r["type"]))
				return r["id"] == "s"
			})
			if slices.Contains(seen, "agent_start") || slices.Contains(seen, "agent_end") {
				t.Fatalf("records before the input handler returned = %v, want no run\n%s", seen, p.stderr.String())
			}
			if _, err := conn.Write([]byte("go\n")); err != nil {
				t.Fatal(err)
			}
			p.await("agent_settled after the input handler returned", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
			p.closeAndWait("after agent_settled")
		})
	}
}
