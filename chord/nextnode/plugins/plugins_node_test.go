package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Not upstream's: the Node subprocess of the plugins component (docs/plan/chord-plugins-node-evaluator.md), exercised with
// scripts written by hand as `evaluate.test.ts`'s "handmade" case does, so that its lifecycle needs no build.

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// nodeRoute is one Node host a Go host routes a host kind to: the app's wiring of a worker, as Pi's server does it
// (workers.ts `route`, plugins.test.ts's end to end): the host kind of the node's address, and who the app stamps the node as.
type nodeRoute struct {
	kind      *contracts.HostKind
	node      *NodeHost
	principal data.Principal
}

// workerRoute is the route of the fixtures' `Worker` kind to node, stamped as the worker.
func workerRoute(node *NodeHost) nodeRoute {
	return nodeRoute{kind: workerKind, node: node, principal: workerPrincipal}
}

// serverFor is a Go host closed when the test ends, with the principal of the server, that routes each route's kind to the
// link of its Node host: `Resolve` hands the link over (it is one link: a second resolve fails).
func serverFor(t testing.TB, routes ...nodeRoute) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
	for _, route := range routes {
		var resolved atomic.Bool
		_, err := h.Route(route.kind, host.RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
				if !resolved.CompareAndSwap(false, true) {
					return host.ResolvedLink{}, errors.New("the node's link was attached already")
				}
				return host.ResolvedLink{Link: route.node.Link(), Principal: route.principal, Trusted: true}, nil
			},
		}, background)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	return h
}

// acceptorOf is a Go host closed when the test ends that accepts the link of node as a child's, stamped as principal (the
// other way an app attaches it): for a node whose kind nothing is held from.
func acceptorOf(t testing.TB, node *NodeHost, principal data.Principal) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
	if _, err := h.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	if err := h.Accept(node.Link(), host.LinkOptions{Principal: principal, Trusted: true}, background); err != nil {
		t.Fatal(err)
	}
	return h
}

// handmade is a script that was not built: the source is the wrapper's text itself.
func handmade(t *testing.T, name, kind, source string) PluginScript {
	t.Helper()
	dir := t.TempDir()
	plugin := Plugin{Name: name, Dir: dir, Facets: map[string]string{kind: filepath.Join(dir, kind+".ts")}, Dependencies: []string{}, External: []string{}}
	return PluginScript{Plugin: plugin, Built: BuiltFacet{Kind: BuiltFacetScript, Plugin: name, Facet: kind, Source: source, Digest: sha256Hex(source)}}
}

var (
	serverPrincipal = delta.JsonObjectOf("kind", "server")
	workerPrincipal = delta.JsonObjectOf("kind", "worker")
)

// loadOn starts a Node host with scripts and a new Go host that routes the worker kind to its link, closed when the test
// ends.
func loadOn(t *testing.T, scripts ...PluginScript) (*NodeHost, host.LoadReport, host.Host) {
	t.Helper()
	address := workerKind.Address(nil)
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: scripts, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return node, report, serverFor(t, workerRoute(node))
}

const emptyFacet = `(function (require, module, exports) { module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => {}) }; })`

func TestNodeLoadsAFacetAndClosesWhenAskedTo(t *testing.T) {
	node, report, _ := loadOn(t, handmade(t, "@test/p", "worker", emptyFacet))
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %v", report.Failed)
	}
	select {
	case <-node.Exited():
		t.Fatal("the process ended on its own")
	default:
	}
	if err := node.Close(background); err != nil {
		t.Fatalf("Close = %v", err)
	}
	select {
	case <-node.Exited():
	default:
		t.Fatal("Close returned before the process ended")
	}
	if node.Err() != nil {
		t.Fatalf("Err = %v after a requested close", node.Err())
	}
}

// evaluate.test.ts › throws an import-time throw, a non-facet export and a refused require, naming the facet (the handmade
// script's refused require).
func TestNodeReportsARefusedRequireOfAHandmadeScriptNamingTheFacet(t *testing.T) {
	_, report, _ := loadOn(t, handmade(t, "@test/p", "worker", `(function (require, module, exports) { require("nope"); })`))
	if len(report.Failed) != 1 || report.Failed[0].Plugin != "@test/p" {
		t.Fatalf("failed = %+v", report.Failed)
	}
	want := `plugin "@test/p" (worker): "nope" is not a host package, a Node builtin or one of its chord.external packages`
	if got := report.Failed[0].Error.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	var nodeErr *NodeError
	if !errors.As(report.Failed[0].Error, &nodeErr) || !strings.Contains(nodeErr.Stack, "/chord-plugins/@test/p/worker.ts?") {
		t.Fatalf("stack = %+v", nodeErr)
	}
}

func TestNodeAnEndedProcessFailsTheLoadWithItsExitAndStderr(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) { process.stderr.write("going down\n"); process.exit(3); })`)
	_, _, err := NewPluginRuntime(NodeOptions{OnOutput: func(NodeStream, []byte) {}}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{script}, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	var exit *NodeExit
	if !errors.As(err, &exit) {
		t.Fatalf("error = %v, want a *NodeExit", err)
	}
	if !strings.Contains(exit.Stderr, "going down") || !strings.Contains(exit.Error(), "exit status 3") {
		t.Fatalf("exit = %v", exit)
	}
}

func TestNodeAReloadAfterTheProcessEndedFailsWithItsExit(t *testing.T) {
	node, _, _ := loadOn(t, handmade(t, "@test/p", "worker", emptyFacet))
	node.kill()
	<-node.Exited()
	_, err := NewPluginRuntime(NodeOptions{}).Reload(node, nil, background)
	var exit *NodeExit
	if !errors.As(err, &exit) {
		t.Fatalf("error = %v, want a *NodeExit", err)
	}
	if err := node.Close(background); !errors.As(err, &exit) {
		t.Fatalf("Close = %v, want the exit", err)
	}
}

func TestNodeGivesTheProcessesOutputToOnOutputByStream(t *testing.T) {
	var mu sync.Mutex
	got := map[NodeStream]string{}
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) { console.log("to stdout"); console.error("to stderr"); module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => {}) }; })`)
	node, _, err := NewPluginRuntime(NodeOptions{OnOutput: func(stream NodeStream, chunk []byte) {
		mu.Lock()
		got[stream] += string(chunk)
		mu.Unlock()
	}}).Load(RuntimeLoad{Kind: "worker", Scripts: []PluginScript{script}, Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err != nil {
		t.Fatal(err)
	}
	// The process ends once it is closed: every byte it wrote has been given by then.
	if err := node.Close(background); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got[NodeStdout] != "to stdout\n" || got[NodeStderr] != "to stderr\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestNodeAnUncaughtExceptionEndsTheProcessWithItsFatalAndClosesTheLink(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => { setTimeout(() => { throw new Error("late failure"); }, 0); }) };
	})`)
	node, _, _ := loadOn(t, script)
	<-node.Exited()
	var exit *NodeExit
	if !errors.As(node.Err(), &exit) || exit.Fatal == nil || exit.Fatal.Message != "late failure" {
		t.Fatalf("Err = %v, want a *NodeExit whose Fatal is the exception", node.Err())
	}
	if !strings.Contains(exit.Fatal.Stack, "late failure") {
		t.Fatalf("stack = %q", exit.Fatal.Stack)
	}
	closed := make(chan struct{})
	node.Link().OnClose(func() { close(closed) })
	<-closed
}

// NodeOptions.ExecArgv are Node's flags of the process: --expose-gc is how the tests that read the heap start it.
func TestNodeExecArgvAreTheProcessesFlags(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		module.exports = { default: require("@earendil-works/chord/next").defineFacet(() => { if (typeof globalThis.gc !== "function") throw new Error("no gc"); }) };
	})`)
	load := func(argv ...string) host.LoadReport {
		node, report, err := NewPluginRuntime(NodeOptions{ExecArgv: argv}).Load(RuntimeLoad{Kind: "worker", Scripts: []PluginScript{script}, Principal: workerPrincipal, Peer: serverPrincipal}, background)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = node.Close(background) })
		return report
	}
	if report := load(); len(report.Failed) != 1 || !strings.Contains(report.Failed[0].Error.Error(), "no gc") {
		t.Fatalf("without the flag: failed = %+v", report.Failed)
	}
	if report := load("--expose-gc"); len(report.Failed) != 0 {
		t.Fatalf("with the flag: failed = %+v", report.Failed)
	}
}

func TestNodeLoadWithoutNodeNamesTheRequirement(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "evaluating plugins needs Node.js 22.19.0 or newer; node was not found on PATH") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeLoadNeedsThePeer(t *testing.T) {
	_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker"}, background)
	if err == nil || !strings.Contains(err.Error(), "a Peer is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeAbortedStartKillsTheProcess(t *testing.T) {
	ctx, cancel := chordctx.WithCancel(background)
	cancel(errors.New("stop"))
	_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, ctx)
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("error = %v", err)
	}
}

// The runtime tree is extracted once per content: a second extraction finds the first's, whatever the process.
func TestNodeTreeIsExtractedOncePerContent(t *testing.T) {
	cache := t.TempDir()
	first, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	second, err := extractNode(cache)
	if err != nil || second != first {
		t.Fatalf("second = %q, %v; want %q", second, err, first)
	}
	if _, err := os.Stat(filepath.Join(first, "pi", "chord", "src", "next-node", "plugins", "evaluate.ts")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache = %v, %v", entries, err)
	}
}

// node/manifest.json records the Pi commit and the hash of every file vendored under node/pi: a file edited by hand, or one
// the manifest does not list, fails here (automation/gen/vendor-chord-plugins-node.sh regenerates both). The commit is the
// pin's (`.upstream/pin`), unless the manifest names it as an explicit `catchup`, which the script writes when it vendors a
// commit other than the pin's; the manifest also records the pin it was made against, so a pin that moved since fails here
// until the copy is regenerated (`make pin-bump` does it).
func TestNodeVendoredFilesAreExactlyTheManifests(t *testing.T) {
	text, err := nodeFiles.ReadFile("node/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Commit  string `json:"commit"`
		Pin     string `json:"pin"`
		Catchup string `json:"catchup"`
		Files   []struct {
			Path   string `json:"path"`
			Sha256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(text, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Commit) != 40 {
		t.Fatalf("commit = %q", manifest.Commit)
	}
	pinFile, err := os.ReadFile(filepath.Join("..", "..", "..", ".upstream", "pin"))
	if err != nil {
		t.Fatal(err)
	}
	var pin string
	for line := range strings.Lines(string(pinFile)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "commit="); ok {
			pin = value
		}
	}
	if pin == "" {
		t.Fatal(".upstream/pin names no commit")
	}
	if manifest.Pin != pin {
		t.Errorf("the pin moved to %s since the copy was made against %s: make vendor-chord-plugins-node", pin, manifest.Pin)
	}
	if manifest.Commit != pin && manifest.Catchup != manifest.Commit {
		t.Errorf("the copy is Pi %s, the pin %s, and the manifest names no catch-up for it (catchup = %q)", manifest.Commit, pin, manifest.Catchup)
	}
	if manifest.Commit == pin && manifest.Catchup != "" {
		t.Errorf("the copy is the pin's, yet the manifest names the catch-up %s", manifest.Catchup)
	}
	listed := map[string]bool{}
	for _, file := range manifest.Files {
		listed[file.Path] = true
		content, err := nodeFiles.ReadFile("node/" + file.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256Hex(string(content)); got != file.Sha256 {
			t.Errorf("%s: sha256 %s, manifest %s", file.Path, got, file.Sha256)
		}
	}
	err = fs.WalkDir(nodeFiles, "node/pi", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && !listed[strings.TrimPrefix(path, "node/")] {
			t.Errorf("%s is vendored but not in the manifest", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// probeNode loads a Node host with only the probe core (ops), as the worker, with options.
func probeNode(t *testing.T, ops string, options NodeOptions, heartbeat *host.HeartbeatOptions) (*PluginRuntime, *NodeHost) {
	t.Helper()
	if options.CacheDir == "" {
		options.CacheDir = t.TempDir()
	}
	address := workerKind.Address(nil)
	runtime := NewPluginRuntime(options)
	node, _, err := runtime.Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, options.CacheDir, ops)}, Address: &address,
		Principal: workerPrincipal, Peer: serverPrincipal, Heartbeat: heartbeat,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return runtime, node
}

// The link is the app's to attach, after Load returned: what the process's host sent before it (its hello) waits in the link,
// and the control channel goes on meanwhile.
func TestNodeALinkAttachedAfterLoadAndOtherRequestsStillCarriesTheHost(t *testing.T) {
	runtime, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, nil)
	if _, err := runtime.Reload(node, nil, background); err != nil {
		t.Fatal(err)
	}
	p := probeOn(t, serverFor(t, workerRoute(node)))
	if got := p.must("echo", "late"); got != "late" {
		t.Fatalf("echo = %v", got)
	}
}

// A host closes a link silent for its heartbeat, its handshake included, and the process ends with it: the app attaches the
// link in time. (The heartbeat is shortened here; the default is 30 s.)
func TestNodeALinkNotAttachedWithinTheHeartbeatEndsTheProcessAndTheLink(t *testing.T) {
	ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
	runtime, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter})
	select {
	case <-node.Exited():
	case <-time.After(30 * time.Second):
		t.Fatal("the process outlived its unattached link")
	}
	var exit *NodeExit
	if !errors.As(node.Err(), &exit) || exit.Err != nil || exit.Fatal != nil {
		t.Fatalf("Err = %v, want a clean exit that nobody asked for", node.Err())
	}
	closed := make(chan struct{})
	node.Link().OnClose(func() { close(closed) })
	<-closed
	if _, err := runtime.Reload(node, nil, background); !errors.As(err, &exit) {
		t.Fatalf("reload = %v, want the exit", err)
	}
}

// A send after the channel ended but before the process exited returns the exit once it is known, not that the connection
// is closed.
func TestNodeASendAfterTheChannelEndedReturnsTheProcessesExit(t *testing.T) {
	runtime, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, nil)
	// The app closes the link: the connection is closed here at once, the process notices later.
	node.Link().Close()
	_, err := runtime.Reload(node, nil, background)
	var exit *NodeExit
	if !errors.As(err, &exit) {
		t.Fatalf("reload = %v (%T), want a *NodeExit", err, err)
	}
}

// gatedLink holds the delivery of the messages that arrive once armed: the reader of the channel waits in the listener.
type gatedLink struct {
	link.Link
	armed   atomic.Bool
	blocked chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedLink) OnMessage(listener func([]byte)) {
	g.Link.OnMessage(func(message []byte) {
		if g.armed.Load() {
			g.once.Do(func() { close(g.blocked) })
			<-g.release
		}
		listener(message)
	})
}

// A `fatal` the process sent before it exited is in the NodeExit also when the channel's reader reads it after the process was
// waited for: the exit waits for the reader. The reader is held in a link listener until the process was seen gone.
func TestNodeAFatalReadAfterTheProcessWasWaitedForIsInTheExit(t *testing.T) {
	_, node := probeNode(t, `later: async () => { setTimeout(() => { throw new Error("late failure"); }, 0); return null; }`, NodeOptions{}, nil)
	gate := &gatedLink{Link: node.Link(), blocked: make(chan struct{}), release: make(chan struct{})}
	h, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
	_, err = h.Route(workerKind, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
			return host.ResolvedLink{Link: gate, Principal: workerPrincipal, Trusted: true}, nil
		},
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	p := probeOn(t, h)
	gate.armed.Store(true)
	// The call's reply is the first message the reader meets: it waits there, with the `fatal` after it unread.
	go func() { _, _ = p.call("later", nil) }()
	<-gate.blocked
	<-node.reaped
	close(gate.release)
	<-node.Exited()
	var exit *NodeExit
	if !errors.As(node.Err(), &exit) || exit.Fatal == nil || exit.Fatal.Message != "late failure" {
		t.Fatalf("Err = %v, want a *NodeExit whose Fatal is the exception", node.Err())
	}
}

func TestNodeTreeIsRepairedWhenItIsPartialOrHasNoMarker(t *testing.T) {
	cache := t.TempDir()
	dir, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, completeMarker)); err != nil {
		t.Fatalf("a marker is written last: %v", err)
	}
	evaluate := filepath.Join(dir, "pi", "chord", "src", "next-node", "plugins", "evaluate.ts")
	for _, damage := range []struct {
		what string
		do   func() error
	}{
		{"a deleted file", func() error { return os.Remove(evaluate) }},
		{"a truncated file", func() error { return os.WriteFile(evaluate, []byte("x"), 0o644) }},
		{"a deleted marker (a tree of an older version)", func() error { return os.Remove(filepath.Join(dir, completeMarker)) }},
		{"a deleted driver and manifest", func() error {
			return errors.Join(os.Remove(filepath.Join(dir, "host.mjs")), os.Remove(filepath.Join(dir, "manifest.json")))
		}},
	} {
		if err := damage.do(); err != nil {
			t.Fatalf("%s: %v", damage.what, err)
		}
		if markerNamesTree(t, dir) && nodeTreeMissing(dir) == nil {
			t.Fatalf("%s: the damaged tree is trusted", damage.what)
		}
		again, err := extractNode(cache)
		if err != nil || again != dir {
			t.Fatalf("%s: extract = %q, %v; want %q", damage.what, again, err, dir)
		}
		if !markerNamesTree(t, dir) {
			t.Fatalf("%s: the tree is not complete after the repair", damage.what)
		}
	}
}

// markerNamesTree reports whether dir's marker holds the hash its name starts with.
func markerNamesTree(t *testing.T, dir string) bool {
	t.Helper()
	marker, err := os.ReadFile(filepath.Join(dir, completeMarker))
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(marker), strings.TrimPrefix(filepath.Base(dir), "chord-plugins-node-")) && nodeTreeComplete(dir, string(marker))
}

// Many processes (here goroutines) extracting into one cache at once: each gets the one directory, and the tree is complete.
func TestNodeConcurrentExtractionsShareOneCompleteTree(t *testing.T) {
	cache := t.TempDir()
	const extractors = 16
	dirs := make([]string, extractors)
	errs := make([]error, extractors)
	var group sync.WaitGroup
	start := make(chan struct{})
	for i := range extractors {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			dirs[i], errs[i] = extractNode(cache)
		}()
	}
	close(start)
	group.Wait()
	for i := range extractors {
		if errs[i] != nil {
			t.Fatalf("extractor %d: %v", i, errs[i])
		}
		if dirs[i] != dirs[0] {
			t.Fatalf("extractor %d: %q, want %q", i, dirs[i], dirs[0])
		}
	}
	if !markerNamesTree(t, dirs[0]) {
		t.Fatal("the tree is not complete")
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache = %v, %v (staging directories are left behind)", entries, err)
	}
}
