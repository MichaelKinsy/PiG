package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's: the Node subprocess of the plugins component (docs/plan/chord-plugins-node-evaluator.md), exercised with
// scripts written by hand as `evaluate.test.ts`'s "handmade" case does, so that its lifecycle needs no build.

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// goHost is a Go host closed when the test ends: the parent a Node host links to.
func goHost(t *testing.T) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
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

// loadOn starts a Node host with scripts, linked to a new Go host, closed when the test ends.
func loadOn(t *testing.T, scripts ...PluginScript) (*NodeHost, host.LoadReport, host.Host) {
	t.Helper()
	parent := goHost(t)
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: scripts, Parent: parent, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return node, report, parent
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
	parent := goHost(t)
	script := handmade(t, "@test/p", "worker", `(function (require, module, exports) { process.stderr.write("going down\n"); process.exit(3); })`)
	_, _, err := NewPluginRuntime(NodeOptions{OnOutput: func(NodeStream, []byte) {}}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{script}, Parent: parent, Principal: workerPrincipal, Peer: serverPrincipal,
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

func TestNodeLoadWithoutNodeNamesTheRequirement(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker", Parent: goHost(t), Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "evaluating plugins needs Node.js 22.19.0 or newer; node was not found on PATH") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeLoadNeedsAParentAndBothPrincipals(t *testing.T) {
	_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker"}, background)
	if err == nil || !strings.Contains(err.Error(), "a Parent host, a Principal and a Peer are required") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeAbortedStartKillsTheProcess(t *testing.T) {
	ctx, cancel := chordctx.WithCancel(background)
	cancel(errors.New("stop"))
	_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{Kind: "worker", Parent: goHost(t), Principal: workerPrincipal, Peer: serverPrincipal}, ctx)
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
// the manifest does not list, fails here (automation/gen/vendor-chord-plugins-node.sh regenerates both).
func TestNodeVendoredFilesAreExactlyTheManifests(t *testing.T) {
	text, err := nodeFiles.ReadFile("node/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Commit string `json:"commit"`
		Files  []struct {
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
