//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An extension that starts a detached helper process (nicobailon/pi-subagents' background runner) locates Pi's host packages from the package process.argv[1] belongs to, reads each
// peer's package.json for its name and exports, and imports the file an export names. PiG's harness entry is that package, so every package Pi's pi-coding-agent lists as a
// dependency must be a peer next to it, with each export importable by a plain Node process. The peers are generated into the runtime archive (internal/noderuntimegen).
// upstream: packages/coding-agent/package.json dependencies; pi-subagents runs/background/runner-aliases.js:resolveHostPeerAliases.
func TestHarnessProvidesEveryPeerPackagePiListsAsADependency(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required to import the peer packages: %v", err)
	}
	runtime := t.TempDir()
	if err := extractNodeRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	var pinned struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	readJSON(t, filepath.Join(runtime, "shims", "pi-dist", "pi-coding-agent", "package.json"), &pinned)
	var peers, imports []string
	for name := range pinned.Dependencies {
		if strings.HasPrefix(name, "@earendil-works/") || name == "typebox" {
			peers = append(peers, name)
		}
	}
	if len(peers) < 6 {
		t.Fatalf("pinned pi-coding-agent dependencies name only %v", peers)
	}
	for _, name := range peers {
		dir := filepath.Join(runtime, "harness", "node_modules", filepath.FromSlash(name))
		var manifest struct {
			Name    string            `json:"name"`
			Exports map[string]string `json:"exports"`
		}
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
			t.Errorf("%s is a dependency of Pi's pi-coding-agent and the harness has no such peer: %v", name, err)
			continue
		}
		readJSON(t, filepath.Join(dir, "package.json"), &manifest)
		if manifest.Name != name || manifest.Exports["."] == "" {
			t.Errorf("%s: package.json name %q, exports %v", name, manifest.Name, manifest.Exports)
			continue
		}
		for _, target := range manifest.Exports {
			imports = append(imports, filepath.Join(dir, filepath.FromSlash(target)))
		}
	}
	// One process imports every export; a module that fails to load is named with its error. Each export must also carry exactly the bindings of the runtime module it serves,
	// including a default export (`import Type from "typebox"` works next to Pi's install), which `export *` alone drops.
	// The paths go in a file: Windows caps a command line at 32 KiB, and every export of every peer is listed here. A file path becomes a file URL
	// through pathToFileURL, as "file://" + a drive path is not a URL.
	script := `import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const files = JSON.parse(readFileSync(process.argv[1], "utf8")).map((p) => pathToFileURL(p).href); const failed = [];
for (const f of files) {
  try {
    const peer = Object.keys(await import(f)).sort();
    const target = new URL(/from "([^"]+)"/.exec(readFileSync(new URL(f), "utf8"))[1], f).href;
    const served = Object.keys(await import(target)).sort();
    if (JSON.stringify(peer) !== JSON.stringify(served)) failed.push(f + ": exports " + JSON.stringify(peer.filter((k) => !served.includes(k))) + " extra, " + JSON.stringify(served.filter((k) => !peer.includes(k))) + " missing against " + target);
  } catch (e) { failed.push(f + ": " + (e && e.message)); }
}
console.log(JSON.stringify(failed));`
	encoded, _ := json.Marshal(imports)
	list := filepath.Join(t.TempDir(), "imports.json")
	if err := os.WriteFile(list, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "--input-type=module", "-e", script, list).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var failed []string
	if err := json.Unmarshal(out, &failed); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	for _, f := range failed {
		t.Errorf("a peer export does not import as its module: %s", f)
	}
	t.Logf("%d peer exports of %d packages import", len(imports), len(peers))
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
