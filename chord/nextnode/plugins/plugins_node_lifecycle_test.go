package plugins

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's: what a Go app sees of a plugin that crashes, exits or never returns, with the plugin running in a Node
// subprocess (docs/plan/chord-plugins-node-evaluator.md, 10.7). Pi's plugin runs in the app's process, where an uncaught
// exception, `process.exit(n)` or an infinite loop is the app's own: Node prints the exception and ends the process with
// code 1, `process.exit(n)` ends it with n, and a loop stalls it. Here only the Node host ends or stalls, which is strictly
// more isolated than Pi (additive); what the app can observe is what Node would have shown.

// lifecycleOutput collects a process's output by stream.
type lifecycleOutput struct {
	mu   sync.Mutex
	text map[NodeStream]string
}

func (o *lifecycleOutput) options() NodeOptions {
	o.text = map[NodeStream]string{}
	return NodeOptions{OnOutput: func(stream NodeStream, chunk []byte) {
		o.mu.Lock()
		o.text[stream] += string(chunk)
		o.mu.Unlock()
	}}
}

func (o *lifecycleOutput) get(stream NodeStream) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text[stream]
}

// lifecyclePlugin is a worker plugin `crash` built for real (so it has a source map) whose setup runs `body` at a timer, in
// helper.ts line 2 column 8 (`throw` of the helper).
func lifecyclePlugin(t *testing.T, helper string) PluginScript {
	t.Helper()
	root := tempRoot(t)
	writePlugin(t, root, "crash", "@test/crash", map[string]string{
		"worker.ts": "import { later } from \"./helper.ts\";\nexport default { setup: () => { setTimeout(later, 0); } };",
		"helper.ts": helper,
	}, nil)
	return evaluateBuilt(t, root, "@test/crash", "worker", nil)
}

func loadLifecycle(t *testing.T, options NodeOptions, scripts ...PluginScript) *NodeHost {
	t.Helper()
	node, _, err := NewPluginRuntime(options).Load(RuntimeLoad{Kind: "worker", Scripts: scripts, Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return node
}

func exitOf(t *testing.T, node *NodeHost) *NodeExit {
	t.Helper()
	select {
	case <-node.Exited():
	case <-time.After(30 * time.Second):
		t.Fatal("the process did not end")
	}
	exit, ok := errors.AsType[*NodeExit](node.Err())
	if !ok {
		t.Fatalf("Err = %v, want a *NodeExit", node.Err())
	}
	return exit
}

// Node, for an uncaught exception, prints the error as `util.inspect` shows it (its stack, own properties, `[cause]`) and a
// blank line and `Node.js <version>` to stderr, and ends the process with code 1 (verified against `node -e`). The host does
// the same on the process's stderr, with the stack mapped through the plugin's source map as `mapStack` does (`crash/
// helper.ts:2:8`, not `/chord-plugins/...`), before it reports the `fatal`: the exit's stderr has the text, and its Code is 1.
func TestNodeAnUncaughtExceptionIsPrintedOnStderrWithItsMappedStackAndEndsWithCode1(t *testing.T) {
	var out lifecycleOutput
	node := loadLifecycle(t, out.options(), lifecyclePlugin(t, "// A helper.\nexport function later(): never {\n\tthrow new Error(\"plugin crashed\", { cause: new RangeError(\"root\") });\n}"))
	exit := exitOf(t, node)
	stderr := out.get(NodeStderr)
	for _, want := range []string{`Error: plugin crashed\n    at .* \(crash/helper\.ts:3:8\)`, `\[cause\]: RangeError: root\n      at .* \(crash/helper\.ts:3:45\)`} {
		if !regexp.MustCompile(want).MatchString(stderr) {
			t.Errorf("stderr = %q, want %q in it", stderr, want)
		}
	}
	if strings.Contains(stderr, "/chord-plugins/") {
		t.Errorf("stderr = %q names /chord-plugins/: the stack is not mapped", stderr)
	}
	if !regexp.MustCompile(`\n\nNode\.js v\d+\.\d+\.\d+\n$`).MatchString(stderr) {
		t.Errorf("stderr = %q, want it to end with the Node.js version line", stderr)
	}
	if exit.Fatal == nil || exit.Fatal.Message != "plugin crashed" || exit.Code != 1 || exit.Signal != "" {
		t.Errorf("exit = %+v (Fatal %v), want the fatal and code 1", exit, exit.Fatal)
	}
	if exit.Stderr != stderr {
		t.Errorf("exit.Stderr = %q, want the printed text %q", exit.Stderr, stderr)
	}
}

// Node's text for a thrown value that is no Error: the value, and the hint that names how to find where it was thrown.
func TestNodeAThrownStringIsPrintedAsNodePrintsIt(t *testing.T) {
	var out lifecycleOutput
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => { setTimeout(() => { throw "just text"; }, 0); }) };
	})`)
	node := loadLifecycle(t, out.options(), script)
	exit := exitOf(t, node)
	want := "just text\n(Use `node --trace-uncaught ...` to show where the exception was thrown)\n\nNode.js " + nodeVersionOf(t) + "\n"
	if got := out.get(NodeStderr); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if exit.Code != 1 {
		t.Errorf("Code = %d, want 1", exit.Code)
	}
}

// nodeVersionOf is the version of the node the tests run (`node --version`).
func nodeVersionOf(t *testing.T) string {
	t.Helper()
	path, err := discoverNode(background)
	if err != nil {
		t.Fatal(err)
	}
	text, err := exec.Command(path, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(text))
}

// An unhandled rejection is Node's default (`--unhandled-rejections=throw`): the rejection is raised as an uncaught exception,
// so it is printed and fatal as one, with code 1; a reason that is no Error is wrapped as Node wraps it
// (`UnhandledPromiseRejection`, code ERR_UNHANDLED_REJECTION).
func TestNodeAnUnhandledRejectionIsAnUncaughtExceptionAsNodesDefaultMakesIt(t *testing.T) {
	for _, each := range []struct {
		name, reject, message string
	}{
		{"an Error", `new Error("rejected")`, "rejected"},
		{"a string", `"just text"`, `The promise rejected with the reason "just text".`},
	} {
		t.Run(each.name, func(t *testing.T) {
			var out lifecycleOutput
			script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
				module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => { setTimeout(() => { Promise.reject(`+each.reject+`); }, 0); }) };
			})`)
			node := loadLifecycle(t, out.options(), script)
			exit := exitOf(t, node)
			if exit.Fatal == nil || !strings.Contains(exit.Fatal.Message, each.message) || exit.Code != 1 {
				t.Errorf("exit = %+v (Fatal %v), want the fatal with %q and code 1", exit, exit.Fatal, each.message)
			}
			if got := out.get(NodeStderr); !strings.Contains(got, each.message) || !strings.Contains(got, "\n\nNode.js v") {
				t.Errorf("stderr = %q, want Node's text with %q", got, each.message)
			}
		})
	}
}

// `process.exit(7)` in a plugin ends the Node host with code 7 and the app goes on (Pi: it would end the app with code 7):
// Exited closes, the NodeExit has the code and no signal, and the Go host's link to it closes.
func TestNodeAPluginsProcessExitEndsOnlyTheHostWithItsCode(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => { setTimeout(() => { process.exit(7); }, 0); }) };
	})`)
	node := loadLifecycle(t, NodeOptions{OnOutput: func(NodeStream, []byte) {}}, script)
	exit := exitOf(t, node)
	if exit.Code != 7 || exit.Signal != "" || exit.Fatal != nil || exit.Reason != "" {
		t.Fatalf("exit = %+v, want code 7, no signal, no fatal, no reason", exit)
	}
	if _, ok := errors.AsType[*exec.ExitError](exit); !ok {
		t.Fatalf("exit = %v, want it to wrap the process's *exec.ExitError", exit)
	}
	closed := make(chan struct{})
	node.Link().OnClose(func() { close(closed) })
	<-closed
}

// A plugin that never returns stalls the Node host (Pi: it stalls the app). Its link's heartbeat on the Go host closes the
// link: the host's silence is the Go host's to detect, and the stalled process, which cannot read its closed channel, is
// killed once its stop grace ran. Exited closes and the NodeExit says why: the signal that ended it, and that the Go side
// did it for a link it closed.
func TestNodeAPluginsInfiniteLoopEndsWithTheGoHostsHeartbeatOnTheLink(t *testing.T) {
	grace := 200 * time.Millisecond
	_, node := probeNode(t, `echo: async (args) => args, spin: async () => { for (;;) {} }`, NodeOptions{StopGrace: &grace, OnOutput: func(NodeStream, []byte) {}}, nil)
	ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
	server, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, Heartbeat: &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter}, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(background) })
	if _, err := server.Route(workerKind, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
			return host.ResolvedLink{Link: node.Link(), Principal: workerPrincipal, Trusted: true}, nil
		},
	}, background); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	p := probeOn(t, server)
	if got := p.must("echo", nil); got != nil {
		t.Fatalf("echo = %v", got)
	}
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	go func() { _, _ = p.ref.Call("call", `{"op":"spin"}`, ctx) }()
	exit := exitOf(t, node)
	if exit.Signal != "SIGKILL" || exit.Code != -1 || !strings.Contains(exit.Reason, "link") {
		t.Fatalf("exit = %+v, want SIGKILL, code -1 and a reason that names the link", exit)
	}
}
