package cli

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// unawaitedPeer is the TCP peer of the session_info_changed and thinking_level_select handlers of testdata/rpc-settle-wait (RPC_NAME_ADDR): a
// handler dials it, announces itself with a "waiting" line and returns when the peer writes a line back. With auto >= 0 the peer writes that long
// after the announcement; otherwise it writes when the test calls release.
type unawaitedPeer struct {
	ln      net.Listener
	auto    time.Duration
	waiting chan struct{}
	mu      sync.Mutex
	held    []net.Conn
}

func newUnawaitedPeer(t *testing.T, auto time.Duration) *unawaitedPeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &unawaitedPeer{ln: ln, auto: auto, waiting: make(chan struct{}, 1024)}
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, conn := range p.held {
			_ = conn.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.held = append(p.held, conn)
			p.mu.Unlock()
			go func() {
				if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "waiting\n" {
					return
				}
				p.waiting <- struct{}{}
				if p.auto >= 0 {
					time.AfterFunc(p.auto, func() { _, _ = conn.Write([]byte("go\n")) })
				}
			}()
		}
	}()
	return p
}

func (p *unawaitedPeer) addr() string { return p.ln.Addr().String() }

// awaitHandler returns once a handler announced itself, so it is known to be inside its wait.
func (p *unawaitedPeer) awaitHandler(t *testing.T) {
	t.Helper()
	select {
	case <-p.waiting:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("no handler dialed the peer")
	}
}

// release lets every handler that waits on the peer return.
func (p *unawaitedPeer) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.held {
		_, _ = conn.Write([]byte("go\n"))
	}
}

// unawaitedImplementation starts testdata/rpc-settle-wait in one SDK realization, or Pi itself, with a reasoning model so that set_thinking_level changes the level.
type unawaitedImplementation struct {
	name string
	// node is true for Pi and for the Node runtime, which report the end of a handler's synchronous part exactly.
	node  bool
	start func(t *testing.T, env ...string) *rpcProcess
}

// writeReasoningModel writes the models.json of a reasoning model nothing is ever sent to, into agentDir.
func writeReasoningModel(t *testing.T, agentDir string) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"probe":{"baseUrl":"http://127.0.0.1:1","apiKey":"unused","api":"openai-completions","models":[{"id":"r1","name":"R1","reasoning":true}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
}

func startUnawaitedPig(t *testing.T, extension string, extraEnv []string) *rpcProcess {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	writeReasoningModel(t, agent)
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agent}
	env = append(env, extraEnv...)
	return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "probe/r1", "-e", extension)
}

func startUnawaitedPi(t *testing.T, extraEnv ...string) *rpcProcess {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	writeReasoningModel(t, agent)
	env := []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + agent}
	env = append(env, extraEnv...)
	args := []string{filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "probe/r1",
		"-e", filepath.Join(root, "coding", "cli", "testdata", "rpc-settle-wait.mjs")}
	return startJSONLProcessAt(t, t.TempDir(), env, node, args...)
}

// unawaitedImplementations builds each fixture once for the test t.
func unawaitedImplementations(t *testing.T) []unawaitedImplementation {
	t.Helper()
	abs := func(name string) string {
		path, err := filepath.Abs(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	node, python, lisp := abs("rpc-settle-wait.mjs"), abs("rpc-settle-wait.py"), abs("rpc-settle-wait.lisp")
	goFixture := buildGoSDKFixture(t, "rpc-settle-wait-go")
	rustFixture := buildRustSettleWait(t)
	return []unawaitedImplementation{
		{"pi", true, startUnawaitedPi},
		{"pig-node", true, func(t *testing.T, env ...string) *rpcProcess { return startUnawaitedPig(t, node, env) }},
		{"pig-go", false, func(t *testing.T, env ...string) *rpcProcess { return startUnawaitedPig(t, goFixture, env) }},
		{"pig-rust", false, func(t *testing.T, env ...string) *rpcProcess { return startUnawaitedPig(t, rustFixture, env) }},
		{"pig-python", false, func(t *testing.T, env ...string) *rpcProcess { return startUnawaitedPig(t, python, env) }},
		{"pig-lisp", false, func(t *testing.T, env ...string) *rpcProcess { return startUnawaitedPig(t, lisp, env) }},
	}
}

// unawaitedCall is one RPC command whose Session call Pi follows with `void this._extensionRunner.emit(...)`.
type unawaitedCall struct {
	command string
	event   string
	// message is the command's n'th request; every request changes what it sets, so that it emits its event.
	message func(id string, n int) map[string]any
}

func unawaitedCalls() []unawaitedCall {
	return []unawaitedCall{
		// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:680-686, agent-session.ts:setSessionName
		{"set_session_name", "session_info_changed", func(id string, n int) map[string]any {
			return map[string]any{"id": id, "type": "set_session_name", "name": "name-" + strconv.Itoa(n)}
		}},
		// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:518-520, agent-session.ts:setThinkingLevel
		{"set_thinking_level", "thinking_level_select", func(id string, n int) map[string]any {
			return map[string]any{"id": id, "type": "set_thinking_level", "level": []string{"low", "high"}[n%2]}
		}},
	}
}

func isNameHandlerDone(r rpcRecord, event string) bool {
	return r["type"] == "extension_ui_request" && r["method"] == "notify" && r["message"] == "name-handler-done:"+event
}

// unawaitedOutcome is what a client sees of one request: how long the reply took and whether the handler's effect, its notification, came first.
type unawaitedOutcome struct {
	replied       bool
	reply         time.Duration
	effectFirst   bool
	effectArrived bool
}

// callAndObserve sends request and reads records until its reply, or until limit has passed without one. With drain it then waits for the handler's notification.
func callAndObserve(p *rpcProcess, event string, request map[string]any, limit time.Duration, drain bool) unawaitedOutcome {
	p.t.Helper()
	var out unawaitedOutcome
	start := time.Now()
	p.sendJSON(request)
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for !out.replied || (drain && !out.effectArrived) {
		select {
		case r, ok := <-p.records:
			if !ok {
				p.t.Fatalf("RPC output ended: %v\n%s", p.outputError(), p.stderr.String())
			}
			switch {
			case r["type"] == "response" && r["id"] == request["id"]:
				out.replied, out.reply = true, time.Since(start)
				if r["success"] != true {
					p.t.Fatalf("%v failed: %v", request["type"], r)
				}
			case isNameHandlerDone(r, event):
				out.effectArrived = true
				out.effectFirst = !out.replied
			}
		case <-timer.C:
			return out
		}
		if out.replied && !drain {
			return out
		}
	}
	return out
}

// TestRPCUnawaitedEventAcrossSDKs builds the fixtures once for the rows below.
func TestRPCUnawaitedEventAcrossSDKs(t *testing.T) {
	impls := unawaitedImplementations(t)
	t.Run("ReplyDoesNotWaitForAHandler", func(t *testing.T) { rpcUnawaitedReplyDoesNotWait(t, impls) })
	t.Run("ShortWaitOrder", func(t *testing.T) { rpcUnawaitedShortWaitOrder(t, impls) })
	t.Run("ComputeOrder", func(t *testing.T) { rpcUnawaitedComputeOrder(t, impls) })
}

// An RPC command that changes the session name or the thinking level returns to the client while an extension's handler for the event waits on a
// socket: Pi does not await `void this._extensionRunner.emit(...)` (agent-session.ts:4122, :2801) and its handler awaits the socket
// (runner.ts:1110), so the reply (rpc-mode.ts:518-520, :680-686) is written at the handler's first await. The handler's own effect follows when its wait ends.
func rpcUnawaitedReplyDoesNotWait(t *testing.T, impls []unawaitedImplementation) {
	for _, impl := range impls {
		for _, call := range unawaitedCalls() {
			t.Run(impl.name+"/"+call.command, func(t *testing.T) {
				peer := newUnawaitedPeer(t, -1)
				p := impl.start(t, "RPC_NAME_ADDR="+peer.addr())
				awaitReady(p)
				request := call.message("c", 0)
				p.sendJSON(request)
				peer.awaitHandler(t)
				var seen []string
				// The reply must come while the handler waits, so a missing reply is a hang: bound it, not the run's budget.
				budget := p.budget
				p.budget = 20 * time.Second
				p.awaitProgress(func() string {
					return "the " + call.command + " reply while the handler waits; seen " + fmt.Sprint(seen)
				}, func(r rpcRecord) bool {
					seen = append(seen, fmt.Sprint(r["type"]))
					if isNameHandlerDone(r, call.event) {
						t.Fatalf("the handler's notification arrived before its wait ended")
					}
					return r["id"] == "c"
				})
				p.budget = budget
				peer.release()
				p.await("the handler's notification after its wait ended", func(r rpcRecord) bool { return isNameHandlerDone(r, call.event) })
				p.closeAndWait("after the handler returned")
			})
		}
	}
}

// A handler whose wait ends at once is Pi's synchronous prefix plus a microtask-free tail only in Node: Pi's handler awaits a socket, so its
// notification follows the reply (rpc-mode.ts:518-520, :680-686; runner.ts:1110). A runtime that cannot report a wait holds the reply until the
// handler returned or outlived the quantum (D86), so its notification, written by the handler before it returned, precedes the reply. Pinned here so
// the divergence's observable effect is declared: it is exactly this order, for a handler that waits less than the quantum.
func rpcUnawaitedShortWaitOrder(t *testing.T, impls []unawaitedImplementation) {
	for _, impl := range impls {
		for _, call := range unawaitedCalls() {
			t.Run(impl.name+"/"+call.command, func(t *testing.T) {
				peer := newUnawaitedPeer(t, 0)
				p := impl.start(t, "RPC_NAME_ADDR="+peer.addr())
				awaitReady(p)
				var seen []string
				p.sendJSON(call.message("c", 0))
				p.await("the reply and the notification", func(r rpcRecord) bool {
					if r["id"] == "c" {
						seen = append(seen, "reply")
					}
					if isNameHandlerDone(r, call.event) {
						seen = append(seen, "effect")
					}
					return len(seen) == 2
				})
				want := []string{"effect", "reply"}
				if impl.node {
					want = []string{"reply", "effect"}
				}
				if !slices.Equal(seen, want) {
					t.Fatalf("order = %v, want %v", seen, want)
				}
				p.closeAndWait("after the handler returned")
			})
		}
	}
}

// A handler that computes for longer than the quantum without awaiting is, in Pi, the synchronous prefix of `void emit(...)`: the caller waits for it,
// so its notification precedes the reply (rpc-mode.ts:518-520, :680-686; runner.ts:1110). A runtime that cannot tell computing from a wait takes
// such a handler to be waiting once it outlives the quantum (D86), so its notification follows the reply. Pinned here so that the cost of the
// quantum is declared: a handler that computes for longer than the quantum has its effect after the reply, not before it.
func rpcUnawaitedComputeOrder(t *testing.T, impls []unawaitedImplementation) {
	for _, impl := range impls {
		for _, call := range unawaitedCalls() {
			t.Run(impl.name+"/"+call.command, func(t *testing.T) {
				p := impl.start(t, "RPC_NAME_BUSY_MS=700")
				awaitReady(p)
				var seen []string
				p.sendJSON(call.message("c", 0))
				p.await("the reply and the notification", func(r rpcRecord) bool {
					if r["id"] == "c" {
						seen = append(seen, "reply")
					}
					if isNameHandlerDone(r, call.event) {
						seen = append(seen, "effect")
					}
					return len(seen) == 2
				})
				want := []string{"effect", "reply"}
				if !impl.node {
					want = []string{"reply", "effect"}
				}
				switch {
				case impl.name == "pig-node":
					// Not this change's: on the base a Node handler's synchronous notification also reaches the client after the reply, where Pi writes it first. Row of its own.
					t.Logf("pig-node order = %v, Pi's = %v", seen, want)
				case !slices.Equal(seen, want):
					t.Fatalf("order = %v, want %v", seen, want)
				}
				p.closeAndWait("after the handler returned")
			})
		}
	}
}

// TestRPCUnawaitedEventLatencyProbe measures how long the reply of set_session_name and set_thinking_level takes in each SDK while the handler returns
// at once, after 50 ms, or never. It is a measurement, not a gate, and runs only when PIG_PROBE_NAME_LATENCY is set:
//
//	PIG_PROBE_NAME_LATENCY=1 go test ./coding/cli -run TestRPCUnawaitedEventLatencyProbe -v
//
// PIG_PROBE_NAME_N sets the calls per cell (default 50) and PIG_PROBE_NAME_CAP_MS how long a call is awaited (default 12000); a cell stops at its first call
// that has no reply by then. PIG_PROBE_NAME_IMPL names one implementation.
func TestRPCUnawaitedEventLatencyProbe(t *testing.T) {
	if os.Getenv("PIG_PROBE_NAME_LATENCY") == "" {
		t.Skip("a measurement: set PIG_PROBE_NAME_LATENCY=1")
	}
	n, capMS := 50, 12000
	if v, err := strconv.Atoi(os.Getenv("PIG_PROBE_NAME_N")); err == nil && v > 0 {
		n = v
	}
	if v, err := strconv.Atoi(os.Getenv("PIG_PROBE_NAME_CAP_MS")); err == nil && v > 0 {
		capMS = v
	}
	limit := time.Duration(capMS) * time.Millisecond
	// auto >= 0: the handler awaits the peer, which answers after auto; auto < 0: the peer never answers; busy > 0: the handler computes for busy milliseconds, awaiting nothing.
	handlers := []struct {
		name string
		auto time.Duration
		busy int
	}{
		{"at-once", 0, 0}, {"await-50ms", 50 * time.Millisecond, 0}, {"await-700ms", 700 * time.Millisecond, 0}, {"never", -1, 0},
		{"compute-50ms", -1, 50}, {"compute-700ms", -1, 700},
	}
	for _, impl := range unawaitedImplementations(t) {
		if only := os.Getenv("PIG_PROBE_NAME_IMPL"); only != "" && only != impl.name {
			continue
		}
		for _, call := range unawaitedCalls() {
			for _, handler := range handlers {
				t.Run(impl.name+"/"+call.command+"/"+handler.name, func(t *testing.T) {
					peer := newUnawaitedPeer(t, handler.auto)
					env := []string{"RPC_NAME_ADDR=" + peer.addr()}
					if handler.busy > 0 {
						env = []string{"RPC_NAME_BUSY_MS=" + strconv.Itoa(handler.busy)}
					}
					p := impl.start(t, env...)
					awaitReady(p)
					var replies []time.Duration
					var effectFirst, replyFirst, noReply int
					for i := range n {
						out := callAndObserve(p, call.event, call.message("c"+strconv.Itoa(i), i), limit, handler.auto >= 0 || handler.busy > 0)
						if !out.replied {
							noReply++
							break
						}
						replies = append(replies, out.reply)
						switch {
						case !out.effectArrived:
						case out.effectFirst:
							effectFirst++
						default:
							replyFirst++
						}
					}
					slices.Sort(replies)
					pick := func(q int) time.Duration {
						if len(replies) == 0 {
							return -1
						}
						return replies[min(len(replies)-1, len(replies)*q/100)]
					}
					t.Logf("PROBE impl=%s rpc=%s handler=%s n=%d replied=%d no-reply-within-%s=%d p50=%v p95=%v max=%v effect-before-reply=%d reply-before-effect=%d",
						impl.name, call.command, handler.name, n, len(replies), limit, noReply, pick(50), pick(95), pick(100), effectFirst, replyFirst)
				})
			}
		}
	}
}
