package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	return startRPCSettleWaitPigWith(t, extraEnv, "-e", extension)
}

// startRPCSettleWaitPigPacked loads an empty sibling extension ahead of the fixture: both run in one Node process (D20).
func startRPCSettleWaitPigPacked(t *testing.T, extension string, extraEnv []string) *rpcProcess {
	t.Helper()
	sibling := filepath.Join(t.TempDir(), "sibling.mjs")
	if err := os.WriteFile(sibling, []byte("export default function () {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return startRPCSettleWaitPigWith(t, extraEnv, "-e", sibling, "-e", extension)
}

func startRPCSettleWaitPigWith(t *testing.T, extraEnv []string, extensions ...string) *rpcProcess {
	t.Helper()
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"}
	env = append(env, extraEnv...)
	args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1"}
	return startRPCProcessAt(t, t.TempDir(), env, append(args, extensions...)...)
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
		{"pig-node-packed", func(t *testing.T, env ...string) *rpcProcess { return startRPCSettleWaitPigPacked(t, node, env) }},
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

// buildRustSettleWait builds testdata/rpc-settle-wait-rs once per test process and returns the executable.
func buildRustSettleWait(t *testing.T) string {
	t.Helper()
	return buildRustSDKFixture(t, "rpc-settle-wait-rs")
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

// An input handler that waits on a socket is awaiting I/O: Pi's prompt() awaits the handler inside its own continuation while its RPC loop reads the next stdin line (agent-session.ts prompt(), rpc-mode.ts:808-810), so get_state is answered before the run starts, and the run follows when the handler returns. In Pig, rpcAwaitHandler admits the next line only once the handler suspended or finished: a Node runtime reports the end of the handler's event-loop window (D86), a handler of another runtime needs the quantum for it. Every realization answers alike, Node isolated and packed with a sibling extension in its process included.
func TestRPCCommandDuringInputHandlerNetworkWaitAnswersAcrossSDKs(t *testing.T) {
	for _, impl := range rpcSettleImplementations(t) {
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

// rpcSettleNodeImplementations are Pi and Pig's Node runtime, alone in its process and packed with a sibling extension (D20). The runtime reports the end of a Node handler's event-loop window (D86), so these rows answer alike.
func rpcSettleNodeImplementations(t *testing.T) []rpcSettleImplementation {
	t.Helper()
	var impls []rpcSettleImplementation
	for _, impl := range rpcSettleImplementations(t) {
		if impl.name == "pi" || strings.HasPrefix(impl.name, "pig-node") {
			impls = append(impls, impl)
		}
	}
	return impls
}

// rpcLabel names a record for an order assertion: a response by its id, anything else by its type.
func rpcLabel(r rpcRecord) string {
	if id, ok := r["id"].(string); ok && r["type"] == "response" {
		return "response " + id
	}
	return fmt.Sprint(r["type"])
}

// startWaitingNodeInput starts impl with an input handler that waits on the peer, sends the prompt p and returns once the handler is inside its wait.
func startWaitingNodeInput(t *testing.T, impl rpcSettleImplementation, env ...string) (*rpcProcess, net.Conn) {
	t.Helper()
	peer := newRPCSettlePeer(t)
	p := impl.start(t, append([]string{"RPC_SETTLE_INPUT_ADDR=" + peer.addr()}, env...)...)
	awaitReady(p)
	p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
	return p, peer.accept()
}

// A before_agent_start handler that waits on a socket is awaiting I/O inside prompt() too (agent-session.ts _startRun awaits emitBeforeAgentStart): Pi's RPC loop reads the next stdin line meanwhile, so get_state is answered before the run starts and before the prompt's response.
func TestRPCCommandDuringBeforeAgentStartHandlerNetworkWaitAnswersAcrossSDKs(t *testing.T) {
	for _, impl := range rpcSettleImplementations(t) {
		t.Run(impl.name, func(t *testing.T) {
			peer := newRPCSettlePeer(t)
			p := impl.start(t, "RPC_SETTLE_START_ADDR="+peer.addr())
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			conn := peer.accept()
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var seen []string
			p.await("the get_state response while the before_agent_start handler waits", func(r rpcRecord) bool {
				seen = append(seen, rpcLabel(r))
				return r["id"] == "s"
			})
			if want := []string{"response s"}; !slices.Equal(seen, want) {
				t.Fatalf("records before the handler returned = %v, want %v\n%s", seen, want, p.stderr.String())
			}
			if _, err := conn.Write([]byte("go\n")); err != nil {
				t.Fatal(err)
			}
			p.await("agent_settled after the handler returned", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
			p.closeAndWait("after agent_settled")
		})
	}
}

// A handler that chains microtasks and never yields to the event loop waits for nothing: Pi's loop reads no stdin line during it, so a get_state the client sends once the handler runs is answered after the prompt's response (the handler's continuation is synchronous to the loop turn). Pig's runtime reports a wait only when the handler's event-loop window closes unanswered, so a handler that only runs microtasks holds stdin as in Pi. One Node process, so the host cannot tell a spinning handler from a waiting one except by that report.
// What it detects (mutations run on the tip): with rpcAwaitHandler admitting the next line at once (5 runs), the input cells are red every time and the command cells only by race, because a Node command is also held by the loop's timer-callback hold (rpcLoopChain.arm: host.loop.arm in serveRPCMode waits for the runtime to acknowledge, which a runtime that chains microtasks cannot); with that hold removed too (10 runs) all four Node cells are red every time. So an input cell proves rpcAwaitHandler's hold and a command cell proves that either hold exists, not each alone. A runtime that reports a microtask-only handler as waiting is caught only by race (such a runtime cannot flush the report while it spins), so this test cannot prove the report's absence.
func TestRPCNodeHandlerThatOnlyRunsMicrotasksHoldsStdin(t *testing.T) {
	for _, kind := range []struct{ name, message string }{{"input", "hello"}, {"command", "/spin"}} {
		for _, impl := range rpcSettleNodeImplementations(t) {
			t.Run(kind.name+"/"+impl.name, func(t *testing.T) {
				mark := filepath.Join(t.TempDir(), "spinning")
				p := impl.start(t, "RPC_SETTLE_SPIN=300", "RPC_SETTLE_SPIN_MARK="+mark)
				awaitReady(p)
				p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": kind.message})
				for deadline := time.Now().Add(testbudget.Wait(t)); ; time.Sleep(time.Millisecond) {
					if _, err := os.Stat(mark); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the handler never started spinning\n%s", p.stderr.String())
					}
				}
				p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
				var seen []string
				p.await("the get_state response", func(r rpcRecord) bool {
					seen = append(seen, rpcLabel(r))
					return r["id"] == "s"
				})
				if !slices.Contains(seen[:len(seen)-1], "response p") {
					t.Fatalf("records up to the get_state response = %v, want the prompt's response before it\n%s", seen, p.stderr.String())
				}
				p.closeAndWait("after the get_state response")
			})
		}
	}
}

// rpcWaitLabels consumes records until done reports true and returns the labels of the ones that order a conversation with a waiting handler: responses, extension errors and the run's start, end and settle.
func rpcWaitLabels(p *rpcProcess, what string, done func(rpcRecord) bool) []string {
	var labels []string
	p.await(what, func(r rpcRecord) bool {
		switch r["type"] {
		case "response", "extension_error", "agent_start", "agent_end", "agent_settled":
			labels = append(labels, rpcLabel(r))
		}
		return done(r)
	})
	return labels
}

func rpcResponseTo(id string) func(rpcRecord) bool {
	return func(r rpcRecord) bool { return r["type"] == "response" && r["id"] == id }
}

// A Node input handler that awaits I/O leaves the loop free, so what a client does meanwhile is as in Pi: two lines keep their order, a handler that throws after its wait is an extension error and the run goes on, an abort is answered at once, and stdin's end shuts the process down without waiting for the handler. Each scenario records what Pi did and requires Pig's runtime, isolated and packed, to have written the same records.
func TestRPCNodeInputHandlerWaitSemanticsComparedWithPi(t *testing.T) {
	type scenario struct {
		name string
		env  []string
		run  func(t *testing.T, p *rpcProcess, release func()) []string
	}
	scenarios := []scenario{
		{"two lines keep their order", nil, func(t *testing.T, p *rpcProcess, release func()) []string {
			p.sendJSON(map[string]any{"id": "s1", "type": "get_state"})
			p.sendJSON(map[string]any{"id": "s2", "type": "get_commands"})
			during := rpcWaitLabels(p, "both responses during the wait", rpcResponseTo("s2"))
			if want := []string{"response s1", "response s2"}; !slices.Equal(during, want) {
				t.Fatalf("records during the wait = %v, want %v\n%s", during, want, p.stderr.String())
			}
			release()
			return append(during, rpcWaitLabels(p, "agent_settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })...)
		}},
		{"a handler that throws after its wait", []string{"RPC_SETTLE_INPUT_THROW=1"}, func(t *testing.T, p *rpcProcess, release func()) []string {
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			during := rpcWaitLabels(p, "the get_state response during the wait", rpcResponseTo("s"))
			if want := []string{"response s"}; !slices.Equal(during, want) {
				t.Fatalf("records during the wait = %v, want %v\n%s", during, want, p.stderr.String())
			}
			release()
			return append(during, rpcWaitLabels(p, "agent_settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })...)
		}},
		{"an abort during the wait", nil, func(t *testing.T, p *rpcProcess, release func()) []string {
			p.sendJSON(map[string]any{"id": "a", "type": "abort"})
			during := rpcWaitLabels(p, "the abort response during the wait", rpcResponseTo("a"))
			if want := []string{"response a"}; !slices.Equal(during, want) {
				t.Fatalf("records during the wait = %v, want %v\n%s", during, want, p.stderr.String())
			}
			release()
			return append(during, rpcWaitLabels(p, "agent_settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })...)
		}},
	}
	for _, sc := range scenarios {
		var want []string
		for _, impl := range rpcSettleNodeImplementations(t) {
			t.Run(sc.name+"/"+impl.name, func(t *testing.T) {
				p, peer := startWaitingNodeInput(t, impl, sc.env...)
				release := func() {
					if _, err := peer.Write([]byte("go\n")); err != nil {
						t.Fatal(err)
					}
				}
				got := sc.run(t, p, release)
				p.closeAndWait("after the scenario")
				if impl.name == "pi" {
					want = got
					t.Logf("Pi wrote %v", got)
					return
				}
				if want == nil {
					t.Skip("Pi's row did not run")
				}
				if !slices.Equal(got, want) {
					t.Fatalf("records = %v, Pi wrote %v\n%s", got, want, p.stderr.String())
				}
			})
		}
	}
}

// Stdin's end while a Node input handler awaits I/O is not held back by the handler: Pi reads the end in its loop and shuts down (rpc-mode.ts:826-829), and so does Pig, whose shutdown does not wait for a handler that is suspended.
func TestRPCNodeInputEndDuringInputHandlerWaitComparedWithPi(t *testing.T) {
	var piExited bool
	for _, impl := range rpcSettleNodeImplementations(t) {
		t.Run(impl.name, func(t *testing.T) {
			p, peer := startWaitingNodeInput(t, impl)
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			p.await("the get_state response during the wait", rpcResponseTo("s"))
			p.closeInput()
			waited := make(chan error, 1)
			go func() { waited <- p.cmd.Wait() }()
			exited := false
			select {
			case <-waited:
				exited = true
				p.exited = true
			case <-time.After(testbudget.Wait(t)):
			}
			if !exited {
				_, _ = peer.Write([]byte("go\n"))
				select {
				case <-waited:
					p.exited = true
				case <-time.After(p.budget):
					_ = p.cmd.Process.Kill()
					<-waited
					p.exited = true
				}
			}
			if impl.name == "pi" {
				piExited = exited
				t.Logf("Pi exited before the handler returned: %v", exited)
				return
			}
			if exited != piExited {
				t.Fatalf("exited before the handler returned = %v, Pi %v\n%s", exited, piExited, p.stderr.String())
			}
		})
	}
}

// A Go handler that computes for longer than HandlerWaitQuantum without waiting is taken to wait (D86), so a get_state sent meanwhile is answered during it and the command's own response follows when it returns. Pi's loop, which the computation would block, answers it afterwards; the runtime cannot tell the two, which D86 records. The command busy-loops on a flag that the peer's line sets, so the test, not a clock, ends the computation; the control is TestRPCCommandAfterAgentEndAnswersAfterFastSettleHandlerAcrossSDKs, where a handler that answers within the quantum holds stdin.
func TestRPCCommandDuringGoHandlerComputationAnswersDuringIt(t *testing.T) {
	peer := newRPCSettlePeer(t)
	p := startRPCSettleWaitPig(t, buildGoSDKFixture(t, "rpc-settle-wait-go"), []string{"RPC_SETTLE_ADDR=" + peer.addr()})
	awaitReady(p)
	p.sendJSON(map[string]any{"id": "h", "type": "prompt", "message": "/spin"})
	conn := peer.accept()
	p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
	var seen []string
	p.await("the get_state response while the command computes", func(r rpcRecord) bool {
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
}
