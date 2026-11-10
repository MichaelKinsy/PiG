package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	"github.com/MichaelKinsy/PiG/chord/nextnode"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
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
	plugin := model.Plugin{Name: name, Dir: dir, Root: dir, Layout: model.LayoutDirectory, Facets: map[string]string{kind: filepath.Join(dir, kind+".ts")}, Dependencies: []string{}, External: []string{}}
	return PluginScript{Plugin: plugin, Built: model.BuiltFacet{Kind: model.BuiltFacetScript, Plugin: name, Facet: kind, Source: source, Digest: sha256Hex(source)}}
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

const emptyFacet = `(function (require, module, exports) { module.exports = { default: require("@earendil-works/chord").defineFacet(() => {}) }; })`

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
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) { console.log("to stdout"); console.error("to stderr"); module.exports = { default: require("@earendil-works/chord").defineFacet(() => {}) }; })`)
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
		module.exports = { default: require("@earendil-works/chord").defineFacet(() => { setTimeout(() => { throw new Error("late failure"); }, 0); }) };
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
		module.exports = { default: require("@earendil-works/chord").defineFacet(() => { if (typeof globalThis.gc !== "function") throw new Error("no gc"); }) };
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
	isolateNodeSearch(t)
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
	if _, err := os.Stat(filepath.Join(first, "pi", "chord", "src", "node", "plugins", "evaluate.ts")); err != nil {
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
	return probeNodeWith(t, ops, options, func(load *RuntimeLoad) { load.Heartbeat = heartbeat })
}

// probeNodeWith is probeNode with the load changed by edit.
func probeNodeWith(t *testing.T, ops string, options NodeOptions, edit func(load *RuntimeLoad)) (*PluginRuntime, *NodeHost) {
	t.Helper()
	if options.CacheDir == "" {
		options.CacheDir = t.TempDir()
	}
	address := workerKind.Address(nil)
	runtime := NewPluginRuntime(options)
	load := RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, options.CacheDir, ops)}, Address: &address,
		Principal: workerPrincipal, Peer: serverPrincipal,
	}
	edit(&load)
	node, _, err := runtime.Load(load, background)
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

// Not Pi's: a Pi worker connects its parent link when it starts, and its heartbeat runs from then. Here the Node host connects
// its parent link when the Go host first uses the other end (NodeHost.Link: OnMessage or Send), so a NodeHost nothing
// attaches has no link and no heartbeat clock: it stays alive for as long as the app wants it, and Close of it works. (The
// heartbeat is shortened here; the default is 30 s.) No Pi line governs the expectation this changed: the commit that made it
// (e0f6bd3215) cites Pi's eager `new ParentLink(...)` (workers/main.ts:114), which Pi did not change, and
// test/parity/testprune-reviewed.toml records it as NOBEHAVIOR.
func TestNodeAnUnattachedHostStaysAliveWithNoClockAndClosesWhenAsked(t *testing.T) {
	ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
	runtime, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter})
	select {
	case <-node.Exited():
		t.Fatalf("an unattached host ended after its heartbeat window: %v", node.Err())
	case <-time.After(10 * closeAfter):
	}
	if _, err := runtime.Reload(node, nil, background); err != nil {
		t.Fatalf("an unattached host refused a request: %v", err)
	}
	if err := node.Close(background); err != nil {
		t.Fatalf("Close = %v, want nil", err)
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

// The first use of the link by the Go host attaches it, as the form the use takes: the host's heartbeat starts then. Here the
// other end is a Go end that never answers its pings, so the host closes the link and ends with it one heartbeat after it
// was attached, not before.
func TestNodeTheFirstUseOfTheLinkAttachesItAndStartsTheHostsHeartbeat(t *testing.T) {
	hello, err := link.BinaryCodec.Encode(&link.HelloMessage{Protocol: data.ProtocolVersion, Incarnation: "00112233445566778899aabbccddeeff", Calls: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, use := range []struct {
		name string
		do   func(l link.Link)
	}{
		{"OnMessage", func(l link.Link) { l.OnMessage(func([]byte) {}) }},
		{"Send", func(l link.Link) { l.Send(hello) }},
	} {
		t.Run(use.name, func(t *testing.T) {
			ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
			_, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter})
			select {
			case <-node.Exited():
				t.Fatalf("the host ended before its link was used: %v", node.Err())
			case <-time.After(5 * closeAfter):
			}
			use.do(node.Link())
			select {
			case <-node.Exited():
			case <-time.After(30 * time.Second):
				t.Fatal("the process outlived its attached link that never answered")
			}
			var exit *NodeExit
			if !errors.As(node.Err(), &exit) || exit.Err != nil || exit.Fatal != nil {
				t.Fatalf("Err = %v, want a clean exit that nobody asked for", node.Err())
			}
			closed := make(chan struct{})
			node.Link().OnClose(func() { close(closed) })
			<-closed
		})
	}
}

// compileCacheFiles counts the cache entries (not their temporary files) in dir, the V8 compile cache of a process's tree.
func compileCacheFiles(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.Contains(entry.Name(), ".") {
			count++
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return count
}

// Not Pi's: Pi's plugins run in the app's process. Node writes the V8 compile cache of the modules a process compiled when the
// process exits, one file after another, before it is gone. A process whose cache is cold (a new tree, new plugins) had its
// writes on its exit path, where each took long enough on Windows under load that together they outlasted the stop grace after
// its link closed: the Go side killed the process after it had run its exit handlers
// (TestNodeTheFirstUseOfTheLinkAttachesItAndStartsTheHostsHeartbeat, "its link closed and it did not exit within 5s", exit
// status 1). The process writes the cache once it is up and after each load and reload, so its exit has none left to write.
//
// host.mjs replies to a load before it writes the load's cache entries (the reply is out first, so the Go side is not held for the write), so the
// count that follows Load must wait for them: a request after the load is handled only once the write is done. Counting right after Load took the
// entry of the probe's own module, compiled by the load and written after its reply, for a write on the exit path (#426, Linux test-fast: 16
// entries before the exit, 17 after). The probe here loads 300 modules, so the write after the reply is long and a count that does not wait for it
// fails on every run, not on the loaded ones.
func TestNodeTheCompileCacheIsWrittenBeforeTheProcessExits(t *testing.T) {
	cache := t.TempDir()
	modules := t.TempDir()
	var imports []string
	for i := range 300 {
		file := filepath.Join(modules, "m"+strconv.Itoa(i)+".mjs")
		if err := os.WriteFile(file, []byte("export const n = "+strconv.Itoa(i)+";\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		imports = append(imports, strconv.Quote((&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(file), "/")}).String()))
	}
	runtime, node := probeNode(t, `loaded: await Promise.all([`+strings.Join(imports, ",")+`].map((module) => import(module))), echo: async (args) => args`, NodeOptions{CacheDir: cache}, nil)
	// The load's reply precedes the write of its cache; a request after it is handled only once that write is done.
	if _, err := runtime.Reload(node, nil, background); err != nil {
		t.Fatal(err)
	}
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-node.Exited():
		t.Fatalf("the process ended: %v", node.Err())
	default:
	}
	dir := filepath.Join(tree, "v8")
	before := compileCacheFiles(t, dir)
	if before == 0 {
		t.Fatal("the process, still running after its load, has written none of its compile cache: it would write it as it exits")
	}
	if err := node.Close(background); err != nil {
		t.Fatal(err)
	}
	if after := compileCacheFiles(t, dir); after != before {
		t.Fatalf("the compile cache has %d entries before the process exited and %d after: it wrote %d on its exit path", before, after, after-before)
	}
}

// A link attached long after the heartbeat's window carries the host, and the heartbeat then runs for it: the Go host
// answers the pings, so the host outlives several windows attached.
func TestNodeALinkAttachedAfterManyHeartbeatWindowsCarriesTheHost(t *testing.T) {
	ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
	_, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter})
	time.Sleep(4 * closeAfter)
	p := probeOn(t, serverFor(t, workerRoute(node)))
	if got := p.must("echo", "late"); got != "late" {
		t.Fatalf("echo = %v", got)
	}
	time.Sleep(4 * closeAfter)
	select {
	case <-node.Exited():
		t.Fatalf("an attached host whose peer answers ended: %v", node.Err())
	default:
	}
	if got := p.must("echo", "again"); got != "again" {
		t.Fatalf("echo = %v", got)
	}
}

// A send after the channel ended but before the process exited returns the exit once it is known, not that the connection
// is closed.
func TestNodeASendAfterTheChannelEndedReturnsTheProcessesExit(t *testing.T) {
	runtime, node := probeNode(t, `echo: async (args) => args`, NodeOptions{}, nil)
	// The app closes the link: the connection is closed here at once, the process notices later.
	node.Link().Close()
	_, err := runtime.Reload(node, nil, background)
	if _, ok := errors.AsType[*NodeExit](err); !ok {
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
	evaluate := filepath.Join(dir, "pi", "chord", "src", "node", "plugins", "evaluate.ts")
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
		group.Go(func() {
			<-start
			dirs[i], errs[i] = extractNode(cache)
		})
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

// The decoder of the host's reports: each kind of upstream's `HostError` (host.ts, the batch's and the registries'), as
// host.mjs sends it (every field of the JS value, errors as wire errors, a failure as its kind and ChordError JSON).
func TestNodeDecodesEveryKindOfHostErrorWithItsFields(t *testing.T) {
	const wired = `{"name":"Error","message":"boom","stack":"Error: boom\n    at x"}`
	const failure = `{"kind":"broken","error":{"code":"unavailable","message":"not here"}}`
	cases := []struct {
		name string
		json string
		want NodeHostError
	}{
		{"listener_error", `{"kind":"listener_error","error":` + wired + `}`, NodeHostError{Kind: "listener_error"}},
		{"delivery_failed", `{"kind":"delivery_failed","error":` + wired + `}`, NodeHostError{Kind: "delivery_failed"}},
		{"registry_conflict", `{"kind":"registry_conflict","registry":"r","message":"two","plugins":["@a/x","@b/y"]}`,
			NodeHostError{Kind: "registry_conflict", Registry: "r", Message: "two", Plugins: []string{"@a/x", "@b/y"}}},
		{"registry_failed", `{"kind":"registry_failed","registry":"r","error":` + wired + `}`, NodeHostError{Kind: "registry_failed", Registry: "r"}},
		{"contribution_failed", `{"kind":"contribution_failed","registry":"r","plugin":"@a/x","type":"t","error":` + wired + `}`,
			NodeHostError{Kind: "contribution_failed", Registry: "r", Plugin: "@a/x", Type: "t"}},
		{"contribution_refused", `{"kind":"contribution_refused","registry":"r","plugin":"@a/x","version":3}`,
			NodeHostError{Kind: "contribution_refused", Registry: "r", Plugin: "@a/x", Version: 3}},
		{"activation_failed", `{"kind":"activation_failed","object":"test.a","failure":` + failure + `}`, NodeHostError{Kind: "activation_failed", Object: "test.a"}},
		{"object_failed", `{"kind":"object_failed","object":"test.a","failure":` + failure + `}`, NodeHostError{Kind: "object_failed", Object: "test.a"}},
		{"storage_failed", `{"kind":"storage_failed","error":` + wired + `}`, NodeHostError{Kind: "storage_failed"}},
		{"close_failed", `{"kind":"close_failed","object":"test.a","native":{"kind":"native","position":2},"error":` + wired + `}`,
			NodeHostError{Kind: "close_failed", Object: "test.a", Native: host.Position{Kind: host.PositionNative, Position: 2}}},
		{"close_timeout", `{"kind":"close_timeout","object":"test.a","native":{"kind":"native","position":1}}`,
			NodeHostError{Kind: "close_timeout", Object: "test.a", Native: host.Position{Kind: host.PositionNative, Position: 1}}},
		{"drain_timeout", `{"kind":"drain_timeout","object":"test.a"}`, NodeHostError{Kind: "drain_timeout", Object: "test.a"}},
		{"protocol_error", `{"kind":"protocol_error","error":` + wired + `}`, NodeHostError{Kind: "protocol_error"}},
		{"resolve_failed", `{"kind":"resolve_failed","address":"test.worker","error":` + wired + `}`, NodeHostError{Kind: "resolve_failed", Address: "test.worker"}},
		{"derive_failed", `{"kind":"derive_failed","object":"test.a","member":"n","error":` + wired + `}`, NodeHostError{Kind: "derive_failed", Object: "test.a", Member: "n"}},
		{"routine_failed", `{"kind":"routine_failed","object":"test.a","routine":{"kind":"onLive","position":3},"error":` + wired + `}`,
			NodeHostError{Kind: "routine_failed", Object: "test.a", Routine: host.Position{Kind: host.PositionOnLive, Position: 3}}},
		{"job_failed", `{"kind":"job_failed","object":"test.a","job":"j","id":"i1","error":` + wired + `}`, NodeHostError{Kind: "job_failed", Object: "test.a", Job: "j", ID: "i1"}},
		{"job_finish_failed", `{"kind":"job_finish_failed","object":"test.a","job":"j","id":"i1","error":` + wired + `}`, NodeHostError{Kind: "job_finish_failed", Object: "test.a", Job: "j", ID: "i1"}},
		{"job_interrupted", `{"kind":"job_interrupted","object":"test.a","job":"j","id":"i1","restarts":5}`, NodeHostError{Kind: "job_interrupted", Object: "test.a", Job: "j", ID: "i1", Restarts: 5}},
		{"job_dropped", `{"kind":"job_dropped","object":"test.a","job":"j","id":"i1"}`, NodeHostError{Kind: "job_dropped", Object: "test.a", Job: "j", ID: "i1"}},
		{"parked_long", `{"kind":"parked_long","object":"test.a","waitingFor":"test.b","state":"failed","since":1700000000123}`,
			NodeHostError{Kind: "parked_long", Object: "test.a", WaitingFor: "test.b", State: "failed", Since: 1700000000123}},
	}
	for _, each := range cases {
		got := make(chan NodeHostError, 1)
		h := &NodeHost{options: NodeOptions{OnError: func(err NodeHostError) { got <- err }}}
		h.control([]byte(`{"event":"error","error":` + each.json + `}`))
		select {
		case err := <-got:
			wantErr, wantFailure := strings.Contains(each.json, `"error":{"name"`), strings.Contains(each.json, `"failure":`)
			if (err.Err != nil) != wantErr || (err.Failure.Error != nil) != wantFailure {
				t.Errorf("%s: Err = %v, Failure = %+v", each.name, err.Err, err.Failure)
			}
			if wantErr && (err.Err == nil || err.Err.Error() != "boom") {
				t.Errorf("%s: Err = %v", each.name, err.Err)
			}
			if wantFailure && (err.Failure.Error == nil || err.Failure.Kind != data.FailureBroken || err.Failure.Error.Message != "not here") {
				t.Errorf("%s: Failure = %+v", each.name, err.Failure)
			}
			err.Err, err.Failure = nil, data.FailureCause{}
			if !reflect.DeepEqual(err, each.want) {
				t.Errorf("%s: got %+v, want %+v", each.name, err, each.want)
			}
		default:
			t.Errorf("%s: no report", each.name)
		}
	}
}

// An error's `cause` chain crosses (a cause that is no Error as an error of its string), errors.As finds a ChordError
// anywhere in it, and a cycle ends the chain.
func TestNodeAnUncaughtExceptionKeepsItsCauseChain(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		const next = require("@earendil-works/chord");
		module.exports = { default: next.defineFacet(() => { setTimeout(() => {
			const provider = new next.ChordError({ code: "unavailable", message: "no provider" });
			provider.cause = "not an error";
			throw new Error("outer", { cause: new TypeError("middle", { cause: new Error("root", { cause: provider }) }) });
		}, 0); }) };
	})`)
	node, _, _ := loadOn(t, script)
	<-node.Exited()
	exit, ok := errors.AsType[*NodeExit](node.Err())
	if !ok || exit.Fatal == nil {
		t.Fatalf("Err = %v, want a *NodeExit with a Fatal", node.Err())
	}
	var chain []string
	for each := exit.Fatal; each != nil; each = each.Cause {
		chain = append(chain, each.Name+": "+each.Message)
	}
	if want := []string{"Error: outer", "TypeError: middle", "Error: root", "ChordError: no provider", "Error: not an error"}; !reflect.DeepEqual(chain, want) {
		t.Fatalf("chain = %q, want %q", chain, want)
	}
	if chord, ok := errors.AsType[*data.ChordError](exit.Fatal); !ok || chord.Code != data.CodeUnavailable {
		t.Fatalf("errors.As finds %v, want the ChordError with code unavailable", chord)
	}
	if !errors.Is(exit.Fatal, exit.Fatal.Cause) {
		t.Fatalf("Unwrap = %v", exit.Fatal.Unwrap())
	}
}

func TestNodeACauseThatIsItsOwnCauseEndsTheChain(t *testing.T) {
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) {
		module.exports = { default: require("@earendil-works/chord").defineFacet(() => { setTimeout(() => {
			const cycle = new Error("cycle");
			cycle.cause = new Error("again", { cause: cycle });
			throw cycle;
		}, 0); }) };
	})`)
	node, _, _ := loadOn(t, script)
	<-node.Exited()
	exit, ok := errors.AsType[*NodeExit](node.Err())
	if !ok || exit.Fatal == nil {
		t.Fatalf("Err = %v", node.Err())
	}
	var chain []string
	for each := exit.Fatal; each != nil; each = each.Cause {
		chain = append(chain, each.Message)
	}
	if want := []string{"cycle", "again"}; !reflect.DeepEqual(chain, want) {
		t.Fatalf("chain = %q, want %q", chain, want)
	}
}

// countingLink counts the bytes of the messages of a link, by direction.
type countingLink struct {
	link.Link
	sent, received atomic.Int64
}

func (c *countingLink) Send(message []byte) {
	c.sent.Add(int64(len(message)))
	c.Link.Send(message)
}

func (c *countingLink) OnMessage(listener func([]byte)) {
	c.Link.OnMessage(func(message []byte) {
		c.received.Add(int64(len(message)))
		listener(message)
	})
}

// A part above 16 KiB crosses deflated when the app asks for compression (RuntimeLoad.Compression on the Node host,
// host.HostOptions.Compression on the Go host); without it the same call crosses whole.
func TestNodeAPartOver16KiBCrossesTheLinkDeflatedWhenTheAppAsksForCompression(t *testing.T) {
	const size = 256 << 10
	cross := func(compressed bool) (sent, received int64) {
		_, node := probeNodeWith(t, `echo: async (args) => args`, NodeOptions{}, func(load *RuntimeLoad) { load.Compression = compressed })
		counted := &countingLink{Link: node.Link()}
		var compression link.Compression
		if compressed {
			compression = nextnode.NodeCompression
		}
		h, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, Compression: compression, OnError: func(host.HostError) {}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(background) })
		if _, err := h.Route(workerKind, host.RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
				return host.ResolvedLink{Link: counted, Principal: workerPrincipal, Trusted: true}, nil
			},
		}, background); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Load(nil, background); err != nil {
			t.Fatal(err)
		}
		p := probeOn(t, h)
		before, beforeReceived := counted.sent.Load(), counted.received.Load()
		payload := strings.Repeat("compressible ", size/13)
		if got := p.must("echo", payload); got != payload {
			t.Fatalf("the echo came back changed (%d bytes)", len(payload))
		}
		return counted.sent.Load() - before, counted.received.Load() - beforeReceived
	}
	plainSent, plainReceived := cross(false)
	if plainSent < size || plainReceived < size {
		t.Fatalf("uncompressed, the call carried %d bytes out and %d back, want each over %d", plainSent, plainReceived, size)
	}
	deflatedSent, deflatedReceived := cross(true)
	if deflatedSent > size/10 || deflatedReceived > size/10 {
		t.Fatalf("compressed, the call carried %d bytes out and %d back, want each under %d", deflatedSent, deflatedReceived, size/10)
	}
}

// The host's keep-alive counts reach the app (NodeOptions.OnActivity, NodeHost.Activity): a hold arriving over the link and
// served there is a reference until it is released. A Go host that only routes to the NodeHost counts none of this.
func TestNodeTheHostsKeepAliveCountsReachTheApp(t *testing.T) {
	counts := make(chan host.ActivityCounts, 64)
	_, node := probeNode(t, `echo: async (args) => args`, NodeOptions{OnActivity: func(c host.ActivityCounts) { counts <- c }}, nil)
	waitFor := func(what string, ok func(host.ActivityCounts) bool) {
		t.Helper()
		for {
			select {
			case c := <-counts:
				if ok(c) {
					return
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("no counts with %s", what)
			}
		}
	}
	waitFor("no references at the start", func(c host.ActivityCounts) bool { return c.References == 0 })
	server := serverFor(t, workerRoute(node))
	held, err := server.Get(background, probeContract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor("the probe held", func(c host.ActivityCounts) bool { return c.References == 1 })
	if got := node.Activity(); got.References != 1 {
		t.Fatalf("Activity = %+v, want one reference", got)
	}
	held.Release()
	waitFor("the probe released", func(c host.ActivityCounts) bool { return c.References == 0 })
}

// An app's own core module imports Pi's chord by its package name: the host answers it with the copy it runs.
func TestNodeACoreModuleImportsChordByItsPackageName(t *testing.T) {
	cache := t.TempDir()
	module := filepath.Join(t.TempDir(), "core.mjs")
	source := `import { defineFacet, hostKind, method, remote, str } from "@earendil-works/chord";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
const Worker = hostKind("test.worker");
const Hello = remote("test.hello", { version: 1, params: {}, hostAddress: () => Worker(), variants: { main: { state: {}, methods: { hi: method(str(), str()) } } } });
export default defineFacet((host) => host.serve(Hello, () => ({ variant: "main", members: { hi: async (name) => "hi " + name + (BACKGROUND_CONTEXT === undefined ? "?" : "") } })));
`
	if err := os.WriteFile(module, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{{Name: "@test/hello", Module: module}}, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	hello := mustContract(contracts.Remote("test.hello", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return workerKind.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			Methods: []contracts.MethodEntry{contracts.Meth("hi", contracts.NewMethod(contracts.Str(), contracts.Str()))},
		})},
	}))
	ref, err := serverFor(t, workerRoute(node)).Get(background, hello, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	if got, err := ref.Call("hi", "there", background); err != nil || got != "hi there" {
		t.Fatalf("hi = %v, %v", got, err)
	}
}

// holdingNode is a Node host (stop grace one second) whose process started a child that inherited its output pipes and sleeps
// for a minute; the child is killed with the test.
func holdingNode(t *testing.T) *NodeHost {
	t.Helper()
	grace := time.Second
	_, node := probeNode(t, `hold: async () => { const { spawn } = await import("node:child_process"); const child = spawn(process.execPath, ["-e", "setTimeout(() => {}, 60000)"], { stdio: ["ignore", "inherit", "inherit"], detached: true }); child.unref(); return child.pid; }`, NodeOptions{StopGrace: &grace}, nil)
	pid := probeOn(t, serverFor(t, workerRoute(node))).must("hold", nil)
	t.Cleanup(func() {
		if number, ok := pid.(float64); ok {
			if process, err := os.FindProcess(int(number)); err == nil {
				_ = process.Kill()
			}
		}
	})
	return node
}

// Not Pi's wording: the exit of the process is what the Go side waits for (Pi's server awaits `child.process.exited`), not the
// end of its output pipes. A process the plugins started that inherited the pipes (here a Node that sleeps) outlives the host's
// process and holds them; the host's exit must not wait for it, as `exec.Cmd.Wait` does without a WaitDelay, and the exit is a
// clean one: the process exited with code 0.
func TestNodeAProcessTheHostStartedThatHoldsItsPipesDoesNotHoldItsExit(t *testing.T) {
	node := holdingNode(t)
	done := make(chan error, 1)
	go func() { done <- node.Close(background) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close = %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the process's exit waited for what holds its pipes")
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

// The same child holds the pipes when the link closes: the process exits by itself, so the grace of the closed link, which ends
// before the exit is seen (the exit waits for the pipes up to the grace after the process ended), kills nothing and the exit says
// no kill happened: it is a clean one that nobody asked for, not "its link closed and it did not exit within the grace".
func TestNodeAProcessThatExitedByItselfAfterItsLinkClosedWhileAChildHeldItsPipesIsNotReportedKilled(t *testing.T) {
	node := holdingNode(t)
	node.Link().Close()
	select {
	case <-node.Exited():
	case <-time.After(20 * time.Second):
		t.Fatal("the process's exit waited for what holds its pipes")
	}
	var exit *NodeExit
	if !errors.As(node.Err(), &exit) {
		t.Fatalf("Err = %v, want the process's exit", node.Err())
	}
	if exit.Err != nil || exit.Reason != "" || exit.Signal != "" || exit.Code != 0 {
		t.Fatalf("exit = %+v, want a clean exit with no kill reason (the Go side killed nothing)", exit)
	}
}
