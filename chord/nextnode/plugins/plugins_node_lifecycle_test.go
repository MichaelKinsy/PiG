package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
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
	path, err := discoverNode(background, nil)
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
// Exited closes, the NodeExit has the code and no signal, and the Go host's link to it closes. The plugin exits once a Go
// function it awaits returns, which the test lets happen after Load returned: with no such order, the process of a slow
// machine (Windows) ended before it sent the load's report, and the load failed with the exit instead.
func TestNodeAPluginsProcessExitEndsOnlyTheHostWithItsCode(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		const go = require("@acme/go");
		module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => { go.gate().then(() => { process.exit(7); }); }) };
	})`)
	gate := make(chan struct{})
	node, _, err := NewPluginRuntime(NodeOptions{OnOutput: func(NodeStream, []byte) {}}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{script}, Principal: workerPrincipal, Peer: serverPrincipal,
		GoPackages: []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{
			"gate": GoFunc(func(ctx context.Context, _ delta.JsonValue) (delta.JsonValue, error) {
				select {
				case <-gate:
				case <-ctx.Done():
				}
				return nil, nil
			}),
		}}},
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	close(gate)
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

// Not Pi's: Pi's `await reload()` is followed by `transported()` and `delivered()` (event-loop hops) before a test reads what a
// host's watchers saw. Here the process boundary has no shared loop, so Reload waits itself: when it returns, every frame
// the reload caused has been handled by the Go host the link is attached to and its watch listener entered, with no waiting by
// the caller. The frames carry a 40 KiB label deflated by the Node host (RuntimeLoad.Compression) to a Go host without a
// compressor, which inflates asynchronously and holds the later messages of the link meanwhile: the case where the frames
// are far behind the reply.
func TestEndToEndTheFramesAReloadCausedAreDeliveredWhenReloadReturnsAlsoWhenTheirPartsInflateAsynchronously(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	c := pcNew(t, root)
	address := workerKind.Address(nil)
	node, _, err := c.Load(PluginsLoad{Kind: "worker", Address: &address, Principal: workerPrincipal, Peer: serverPrincipal, Compression: true}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	server := serverFor(t, workerRoute(node))
	notes, err := server.Get(background, notesContract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(notes.Release)
	watch, err := notes.Member("label").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	frames := pcStartFrames(watch)
	step, err := c.Step("worker", node)
	if err != nil {
		t.Fatal(err)
	}
	label := strings.Repeat("0123456789abcdef", 40<<6)
	pcWrite(t, root, "notes", "label.ts", "export const label = \""+label+"\";\n")
	c.waitMarks(t, "dirty notes", pcDirty("@test/notes"))
	c.rollout(t, []RolloutStep{step}, c.build(t, c.Marks().Dirty...))
	if got := frames.all(); !reflect.DeepEqual(got, []delta.JsonValue{label}) {
		t.Fatalf("frames when Rollout returned = %d frames, want the one with the new label (%d bytes)", len(got), len(label))
	}
}

// The Node subprocess keeps V8's code cache of the modules it imports (Pi's type-stripped closure) in the extracted tree, so a
// start after the first begins from compiled code: BenchmarkNodeStart/warm/v8-cache against /no-v8-cache is the measure (about
// 0.31 s to `started` against 0.63 s on the reference machine). The process writes the cache once it is up, and after a load or
// reload, not as it exits (TestNodeTheCompileCacheIsWrittenBeforeTheProcessExits).
func TestNodeTheSubprocessKeepsV8sCompileCacheInTheExtractedTree(t *testing.T) {
	cache := t.TempDir()
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Close(background); err != nil {
		t.Fatal(err)
	}
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(tree, "v8"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("the compile cache = %v, %v; want a directory with the cache of this Node", entries, err)
	}
}

// warmRuntime is a runtime that closes with the test.
func warmRuntime(t *testing.T, options NodeOptions) *PluginRuntime {
	t.Helper()
	if options.CacheDir == "" {
		options.CacheDir = t.TempDir()
	}
	options.OnOutput = func(NodeStream, []byte) {}
	runtime := NewPluginRuntime(options)
	t.Cleanup(func() { _ = runtime.Close(background) })
	return runtime
}

func spareOf(runtime *PluginRuntime) *NodeHost {
	runtime.warm.mu.Lock()
	defer runtime.warm.mu.Unlock()
	if runtime.warm.spare == nil {
		return nil
	}
	return runtime.warm.spare.node
}

func loadEmpty(t *testing.T, runtime *PluginRuntime) *NodeHost {
	t.Helper()
	node, _, err := runtime.Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return node
}

// Warm blocks until a spare process is up to `started`, at most one is kept (a second Warm waits for the first's and returns
// nil), and the next Load takes it: the host it returns is that process, loaded, and the runtime has no spare left.
func TestNodeWarmStartsOneSpareTheNextLoadTakes(t *testing.T) {
	runtime := warmRuntime(t, NodeOptions{})
	if err := runtime.Warm(background); err != nil {
		t.Fatal(err)
	}
	spare := spareOf(runtime)
	if spare == nil {
		t.Fatal("Warm returned with no spare")
	}
	select {
	case <-spare.started:
	default:
		t.Fatal("Warm returned before the spare started")
	}
	if err := runtime.Warm(background); err != nil || spareOf(runtime) != spare {
		t.Fatalf("a second Warm = %v, spare %p, want nil and the same spare %p", err, spareOf(runtime), spare)
	}
	node := loadEmpty(t, runtime)
	if node != spare {
		t.Fatal("Load started a process of its own instead of taking the spare")
	}
	if spareOf(runtime) != nil {
		t.Fatal("the runtime kept the spare a Load took")
	}
	// The taken process is the Load's: the runtime's Close leaves it running.
	if err := runtime.Close(background); err != nil {
		t.Fatal(err)
	}
	select {
	case <-node.Exited():
		t.Fatal("the runtime's Close ended the process of a host it had given away")
	default:
	}
}

// KeepWarm: after each Load has its process the runtime starts the next spare in the background, so the Load after finds one;
// two hosts are two processes.
func TestNodeKeepWarmStartsTheNextSpareAfterEachLoad(t *testing.T) {
	runtime := warmRuntime(t, NodeOptions{KeepWarm: true})
	first := loadEmpty(t, runtime)
	// Warm waits for the spare the first Load started in the background.
	if err := runtime.Warm(background); err != nil {
		t.Fatal(err)
	}
	spare := spareOf(runtime)
	if spare == nil || spare == first {
		t.Fatalf("spare = %p after the first Load (%p), want another process", spare, first)
	}
	second := loadEmpty(t, runtime)
	if second != spare {
		t.Fatal("the second Load did not take the kept spare")
	}
	if err := runtime.Warm(background); err != nil || spareOf(runtime) == nil || spareOf(runtime) == second {
		t.Fatalf("no next spare after the second Load: %v", err)
	}
}

// Close closes the spare (its exit is a requested one) and ends the runtime: a Load or Warm after it is refused. A host a
// Load returned before is the caller's and keeps running.
func TestNodeRuntimeCloseClosesTheSpareAndRefusesLaterLoads(t *testing.T) {
	runtime := warmRuntime(t, NodeOptions{KeepWarm: true})
	kept := loadEmpty(t, runtime)
	if err := runtime.Warm(background); err != nil {
		t.Fatal(err)
	}
	spare := spareOf(runtime)
	if err := runtime.Close(background); err != nil {
		t.Fatal(err)
	}
	select {
	case <-spare.Exited():
	default:
		t.Fatal("Close returned before the spare ended")
	}
	if spare.Err() != nil {
		t.Fatalf("the spare's Err = %v after a requested close", spare.Err())
	}
	select {
	case <-kept.Exited():
		t.Fatal("Close ended a host a Load had returned")
	default:
	}
	if _, _, err := runtime.Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background); !errors.Is(err, errRuntimeClosed) {
		t.Fatalf("Load after Close = %v, want errRuntimeClosed", err)
	}
	if err := runtime.Warm(background); !errors.Is(err, errRuntimeClosed) {
		t.Fatalf("Warm after Close = %v, want errRuntimeClosed", err)
	}
	if err := runtime.Close(background); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
}

// startingSpare waits until the runtime has registered a spare, and returns it: its start may be under way.
func startingSpare(t *testing.T, runtime *PluginRuntime) *spare {
	t.Helper()
	var found *spare
	eventually(t, 15*time.Second, "the runtime to register its spare", func() bool {
		runtime.warm.mu.Lock()
		defer runtime.warm.mu.Unlock()
		found = runtime.warm.spare
		return found != nil
	})
	return found
}

// processEnded waits until the process of a spare whose start settled ended; a start that failed has none.
func processEnded(t *testing.T, s *spare) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the spare's start did not settle")
	}
	if s.err != nil {
		return
	}
	select {
	case <-s.node.Exited():
	case <-time.After(30 * time.Second):
		t.Fatal("the spare's process is still running after Close")
	}
}

// Close while the background spare is still starting cancels and joins that start: no process is left, nothing is reported.
func TestNodeRuntimeCloseWhileTheKeptSpareIsStartingLeavesNoProcess(t *testing.T) {
	reported := make(chan NodeHostError, 1)
	runtime := warmRuntime(t, NodeOptions{KeepWarm: true, OnError: func(err NodeHostError) { reported <- err }})
	loadEmpty(t, runtime)
	kept := startingSpare(t, runtime)
	if err := runtime.Close(background); err != nil {
		t.Fatal(err)
	}
	if spareOf(runtime) != nil {
		t.Fatal("a spare is left after Close")
	}
	processEnded(t, kept)
	select {
	case err := <-reported:
		t.Fatalf("Close reported %+v for the start it cancelled", err)
	default:
	}
}

// A Close whose ctx ends while a Warm the caller started is still starting returns the cause, and the process that start
// finishes is stopped, not left running until the Go process exits (a Node host no Load took has no heartbeat to end it).
func TestNodeRuntimeClosePastItsDeadlineStillStopsTheSpareThatIsStarting(t *testing.T) {
	runtime := warmRuntime(t, NodeOptions{})
	warmed := make(chan error, 1)
	go func() { warmed <- runtime.Warm(background) }()
	starting := startingSpare(t, runtime)
	expired, cancel := context.WithCancelCause(background)
	cancel(errors.New("the deadline passed"))
	if err := runtime.Close(expired); err == nil {
		select {
		case <-starting.done:
		default:
			t.Fatal("Close returned nil while the spare was still starting")
		}
	}
	if err := <-warmed; err != nil {
		t.Fatalf("Warm = %v", err)
	}
	processEnded(t, starting)
}

// A spare whose process ended meanwhile is not handed out: the Load starts a process of its own.
func TestNodeALoadDoesNotTakeASpareThatDied(t *testing.T) {
	runtime := warmRuntime(t, NodeOptions{})
	if err := runtime.Warm(background); err != nil {
		t.Fatal(err)
	}
	spare := spareOf(runtime)
	spare.kill()
	<-spare.Exited()
	node := loadEmpty(t, runtime)
	if node == spare {
		t.Fatal("Load took a spare that had died")
	}
	select {
	case <-node.Exited():
		t.Fatal("the Load's host is not alive")
	default:
	}
}

// A kept spare whose start fails is reported to OnError as `warm_failed`; the next Load starts its own process and has its own
// result. (The spare is up before Node leaves PATH: the Load takes it, and the start that follows cannot find Node.)
func TestNodeAFailedKeptSpareIsReportedAndTheNextLoadStartsItsOwn(t *testing.T) {
	reported := make(chan NodeHostError, 1)
	runtime := warmRuntime(t, NodeOptions{KeepWarm: true, OnError: func(err NodeHostError) { reported <- err }})
	if err := runtime.Warm(background); err != nil {
		t.Fatal(err)
	}
	isolateNodeSearch(t)
	t.Setenv("PATH", t.TempDir())
	loadEmpty(t, runtime)
	select {
	case err := <-reported:
		if err.Kind != "warm_failed" || err.Err == nil || !strings.Contains(err.Err.Error(), "Node.js 22.19.0 or newer") {
			t.Fatalf("reported %+v, want warm_failed naming the Node requirement", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the failed start was not reported")
	}
	if spareOf(runtime) != nil {
		t.Fatal("a failed start left a spare")
	}
	_, _, err := runtime.Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "Node.js 22.19.0 or newer") {
		t.Fatalf("Load = %v, want its own start's error", err)
	}
}

// The plugins component warms and closes its runtime: Plugins.Warm starts the spare its Load takes; Close of a component
// whose spare nothing took closes the spare.
func TestPluginsWarmStartsTheSpareLoadTakesAndCloseClosesAnUntakenOne(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	c := pcNew(t, root)
	if err := c.Warm(background); err != nil {
		t.Fatal(err)
	}
	spare := spareOf(c.runtime)
	address := workerKind.Address(nil)
	node, _, err := c.Load(PluginsLoad{Kind: "worker", Address: &address, Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if node != spare {
		t.Fatal("Plugins.Load did not take the spare Plugins.Warm started")
	}
	if err := c.Warm(background); err != nil {
		t.Fatal(err)
	}
	untaken := spareOf(c.runtime)
	c.Close()
	select {
	case <-untaken.Exited():
	default:
		t.Fatal("Plugins.Close left the spare running")
	}
}

// closeClock is the timers of one Close under test. Close starts the timer of each kill deadline through NodeHost.startAfter;
// a test that sets it to closeClock.after decides when each deadline passes, so what it orders is the process's answer to the
// close against a deadline. With wall time the order is how long the process takes to answer against the grace, which the
// test does not choose: a process that answers 200 ms late (starved on a loaded machine, as the Windows CI runners are) has
// not answered within a grace of 200 ms, and Close kills it as it kills one that never answers.
type closeClock struct {
	timers chan *closeTimer
}

// closeTimer is one deadline Close started: its length, and what it passes on.
type closeTimer struct {
	length time.Duration
	passed chan time.Time

	mu      sync.Mutex
	stopped bool
}

func (c *closeClock) after(length time.Duration) (<-chan time.Time, func()) {
	timer := &closeTimer{length: length, passed: make(chan time.Time, 1)}
	c.timers <- timer
	return timer.passed, func() {
		timer.mu.Lock()
		timer.stopped = true
		timer.mu.Unlock()
	}
}

// next is the next timer Close started.
func (c *closeClock) next(t *testing.T) *closeTimer {
	t.Helper()
	select {
	case timer := <-c.timers:
		return timer
	case <-time.After(30 * time.Second):
		t.Fatal("Close started no timer")
		return nil
	}
}

// pass is the moment the deadline passes. It reports whether Close still waited for it: a timer Close stopped passes nothing.
func (timer *closeTimer) pass() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	if timer.stopped {
		return false
	}
	timer.passed <- time.Time{}
	return true
}

// closingHost is a Node host with one call running that does not end until release, and its Close under way: the process was
// sent the close and Close started the timer of the grace.
type closingHost struct {
	node  *NodeHost
	clock *closeClock
	// grace is the timer of the answer, started when Close sent the close.
	grace *closeTimer
	// closed gets what Close returns; called what the call returns.
	closed  chan error
	called  chan error
	release func()
}

func startClosing(t *testing.T, grace time.Duration) *closingHost {
	t.Helper()
	gate := filepath.Join(t.TempDir(), "release")
	quoted, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var once sync.Once
	options := NodeOptions{StopGrace: &grace, OnOutput: func(_ NodeStream, chunk []byte) {
		if strings.Contains(string(chunk), "held call running") {
			once.Do(func() { close(started) })
		}
	}}
	_, node := probeNode(t, `held: async () => { const { existsSync } = await import("node:fs"); console.log("held call running"); while (!existsSync(`+string(quoted)+`)) await new Promise((resolve) => setTimeout(resolve, 5)); return "finished"; }`, options, nil)
	release := func() { _ = os.WriteFile(gate, nil, 0o644) }
	// Runs before the host's own cleanup, so a test that failed does not leave its Close waiting for the call.
	t.Cleanup(release)
	p := probeOn(t, serverFor(t, workerRoute(node)))
	called := make(chan error, 1)
	go func() {
		_, err := p.call("held", nil)
		called <- err
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the call never started")
	}
	clock := &closeClock{timers: make(chan *closeTimer, 4)}
	node.startAfter = clock.after
	closed := make(chan error, 1)
	go func() { closed <- node.Close(background) }()
	return &closingHost{node: node, clock: clock, grace: clock.next(t), closed: closed, called: called, release: release}
}

// answered waits until the process answered the close (`closing`) and Close took the answer, and returns the timer Close
// started for the exit of a host that is closing.
func (h *closingHost) answered(t *testing.T) *closeTimer {
	t.Helper()
	select {
	case <-h.node.closeBegun:
	case <-time.After(30 * time.Second):
		t.Fatal("the process did not answer the close")
	}
	return h.clock.next(t)
}

func (h *closingHost) closeResult(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.closed:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return")
		return nil
	}
}

// Close is Pi's close of the host: every object ends at once, then each drains and closes in turn, each step up to the host's
// closeTimeout, and then the app's store closes. A process that is running that close (it answered `closing`) is not killed when the stop grace passes: here
// the host answers while a call is running that does not end, the grace passes after the answer, and Close waits on: for
// the closeTimeout and the grace, from the answer, not the grace from the close. The order is the test's, not the machine's.
func TestNodeCloseDoesNotKillAHostThatIsStillClosingWithinItsCloseTimeout(t *testing.T) {
	grace := 200 * time.Millisecond
	h := startClosing(t, grace)
	if h.grace.length != grace {
		t.Fatalf("Close waits %v for the answer, want the grace %v", h.grace.length, grace)
	}
	exit := h.answered(t)
	if want := h.node.closeTimeout + grace; exit.length != want {
		t.Fatalf("Close waits %v for the exit of a host that is closing, want its closeTimeout %v and the grace %v", exit.length, h.node.closeTimeout, grace)
	}
	// The grace passes now, with the call still running: it was the deadline of the answer, and the host answered.
	if h.grace.pass() {
		t.Fatal("Close still waited for the grace after the host answered")
	}
	h.release()
	if err := h.closeResult(t); err != nil {
		if exit, ok := errors.AsType[*NodeExit](err); ok {
			t.Fatalf("Close = %v, want nil: the host was closing within its closeTimeout; exit %+v (signal %q, reason %q), stderr:\n%s", err, exit, exit.Signal, exit.Reason, exit.Stderr)
		}
		t.Fatalf("Close = %v, want nil: the host was closing within its closeTimeout", err)
	}
	if exit, ok := errors.AsType[*NodeExit](h.node.Err()); ok {
		t.Fatalf("the process ended with %+v, want a clean exit", exit)
	}
	<-h.called
}

// A host still running its close when its closeTimeout and the grace have passed since it answered is killed.
func TestNodeCloseKillsAHostThatIsStillClosingPastItsCloseTimeoutAndTheGrace(t *testing.T) {
	h := startClosing(t, 200*time.Millisecond)
	exit := h.answered(t)
	if !exit.pass() {
		t.Fatal("Close did not wait for the exit")
	}
	err := h.closeResult(t)
	if killed, ok := errors.AsType[*NodeExit](err); !ok || killed.Signal != "SIGKILL" {
		t.Fatalf("Close = %v, want the SIGKILL exit of a host that was still closing past its closeTimeout and the grace", err)
	}
	<-h.called
}

// A process that never answers the close (its loop is stalled) is killed once the grace passed, as before.
func TestNodeCloseKillsAHostThatDoesNotAnswerWithinTheGrace(t *testing.T) {
	grace := 200 * time.Millisecond
	started := make(chan struct{})
	var once sync.Once
	options := NodeOptions{StopGrace: &grace, OnOutput: func(_ NodeStream, chunk []byte) {
		if strings.Contains(string(chunk), "spinning") {
			once.Do(func() { close(started) })
		}
	}}
	_, node := probeNode(t, `spin: async () => { console.log("spinning"); for (;;) {} }`, options, nil)
	p := probeOn(t, serverFor(t, workerRoute(node)))
	go func() { _, _ = p.call("spin", nil) }()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the call never started")
	}
	err := node.Close(background)
	if exit, ok := errors.AsType[*NodeExit](err); !ok || exit.Signal != "SIGKILL" {
		t.Fatalf("Close = %v, want the SIGKILL exit of a process that did not answer", err)
	}
}

// A process that does not exit when its channel ended or it was told to close is killed after the same 5 s grace Pi's workers
// give a worker (`GRACE = 5_000` in packages/chord/src/next-node/workers/workers.ts:182); NodeOptions.StopGrace replaces it.
func TestNodeTheDefaultStopGraceIsPisFiveSeconds(t *testing.T) {
	if got := (&NodeHost{}).stopGrace(); got != 5*time.Second {
		t.Fatalf("default stop grace = %v, want 5s (Pi workers.ts:182 GRACE)", got)
	}
	grace := 200 * time.Millisecond
	if got := (&NodeHost{options: NodeOptions{StopGrace: &grace}}).stopGrace(); got != grace {
		t.Fatalf("stop grace with StopGrace set = %v, want %v", got, grace)
	}
}

// The context of the work the process asked of this side (a bridged proxy's request, NodeMember.Value) lives as long as the
// process does: it is not ended before and is ended once the process is.
func TestNodeLifeContextEndsWithTheProcess(t *testing.T) {
	_, node := probeNode(t, `echo: async (args) => args`, NodeOptions{OnOutput: func(NodeStream, []byte) {}}, nil)
	if err := node.life.Err(); err != nil {
		t.Fatalf("life ended while the process runs: %v", err)
	}
	if err := node.Close(background); err != nil {
		t.Fatal(err)
	}
	select {
	case <-node.life.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("life did not end with the process")
	}
}
