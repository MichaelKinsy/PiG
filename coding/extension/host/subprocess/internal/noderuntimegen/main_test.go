package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepeers"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runMain(t *testing.T, args ...string) {
	t.Helper()
	previousArgs, previousFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = previousArgs, previousFlags })
	flag.CommandLine = flag.NewFlagSet("noderuntimegen", flag.PanicOnError)
	os.Args = append([]string{"noderuntimegen"}, args...)
	main()
}

// peerTargets returns a runtime tree entry for every module the host peers re-export, so a synthetic runtime has what the generator requires.
func peerTargets() map[string]string {
	files := map[string]string{}
	for _, target := range noderuntimepeers.Targets() {
		files[strings.Replace(target, "*", "x", 1)] = "peer target"
	}
	return files
}

func syntheticRuntime(extra map[string]string) map[string]string {
	files := peerTargets()
	files["harness/package.json"] = `{"name":"@earendil-works/pi-coding-agent","version":"9.8.7"}`
	maps.Copy(files, extra)
	return files
}

// The command packages exactly the runtime sources (top-level *.mjs, harness/ without dot or underscore names, shims/)
// and the generated host peers into a noderuntimepack archive, and writes the hash of the entry names and bytes as the generated Go constant; the same input gives
// byte-identical outputs, and a changed source changes the hash.
func TestMainPackagesRuntimeReproducibly(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, syntheticRuntime(map[string]string{
		"main.mjs":            "main",
		"notes.txt":           "ignored: not .mjs",
		"nested/other.mjs":    "ignored: not top level",
		"harness/run.mjs":     "run",
		"harness/_private.js": "ignored: underscore",
		"harness/.hidden.mjs": "ignored: dot",
		"shims/a/b.js":        "shim",
	}))
	out := t.TempDir()
	archive, metadata := filepath.Join(out, "runtime.pack"), filepath.Join(out, "digest.go")
	runMain(t, "-root", root, "-archive", archive, "-metadata", metadata)

	packed, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu       sync.Mutex
		names    []string
		contents = map[string]string{}
	)
	if err := noderuntimepack.Extract(string(packed), func(name string, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		names = append(names, name)
		contents[name] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	var sources, peers []string
	for _, name := range names {
		if strings.Contains(name, "/harness/node_modules/") {
			peers = append(peers, name)
			continue
		}
		sources = append(sources, name)
	}
	wantSources := []string{"runtime-node/harness/package.json", "runtime-node/harness/run.mjs", "runtime-node/main.mjs", "runtime-node/shims/a/b.js"}
	for target := range peerTargets() {
		wantSources = append(wantSources, "runtime-node/"+target)
	}
	slices.Sort(wantSources)
	if !slices.Equal(sources, wantSources) {
		t.Fatalf("archive sources = %v, want %v", sources, wantSources)
	}
	for _, name := range []string{"pi-agent-core", "pi-ai", "pi-codemode", "pi-mcp", "pi-tui"} {
		pkg := "runtime-node/harness/node_modules/@earendil-works/" + name + "/package.json"
		manifest, ok := contents[pkg]
		if !ok || !strings.Contains(manifest, `"version": "9.8.7"`) || !strings.Contains(manifest, `"name": "@earendil-works/`+name+`"`) {
			t.Fatalf("%s = %q (present %v), want a manifest that carries the harness version", pkg, manifest, ok)
		}
	}
	if _, ok := contents["runtime-node/harness/node_modules/typebox/package.json"]; !ok || len(peers) == 0 {
		t.Fatalf("typebox peer missing from the archive: %v", peers)
	}

	// The digest covers the sources in walk order, then the peers in generation order.
	sourceBytes := map[string][]byte{}
	for _, name := range sources {
		sourceBytes[strings.TrimPrefix(name, "runtime-node/")] = []byte(contents[name])
	}
	generatedPeers, err := noderuntimepeers.HostPeerFiles("9.8.7", sourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	for _, name := range walkOrder(sources) {
		digest.Write([]byte(name))
		digest.Write([]byte(contents[name]))
	}
	for _, file := range generatedPeers {
		if contents[file.Name] != string(file.Data) {
			t.Fatalf("archive entry %s differs from the generated peer", file.Name)
		}
		digest.Write([]byte(file.Name))
		digest.Write(file.Data)
	}
	generated, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "const nodeRuntimeHash = "+strconv.Quote(string(digest.Sum(nil)))) {
		t.Fatalf("generated constant does not carry the content hash:\n%s", generated)
	}

	firstArchive, _ := os.ReadFile(archive)
	runMain(t, "-root", root, "-archive", archive, "-metadata", metadata)
	if secondArchive, _ := os.ReadFile(archive); !bytes.Equal(firstArchive, secondArchive) {
		t.Fatal("second run changed the archive bytes")
	}

	writeTree(t, root, map[string]string{"main.mjs": "changed"})
	runMain(t, "-root", root, "-archive", archive, "-metadata", metadata)
	if changed, _ := os.ReadFile(metadata); bytes.Equal(changed, generated) {
		t.Fatal("changing a runtime source did not change the generated hash")
	}
}

// walkOrder sorts runtime entry names the way filepath.WalkDir visits them: lexically within each directory, a directory's entries before its next sibling.
func walkOrder(names []string) []string {
	out := slices.Clone(names)
	slices.SortFunc(out, func(a, b string) int {
		return slices.Compare(strings.Split(a, "/"), strings.Split(b, "/"))
	})
	return out
}

// The generator needs the harness package manifest (the peers carry its version) and every module a peer re-exports; a runtime missing either is rejected, not packaged without its peers.
func TestGenerateRejectsAnIncompleteRuntime(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(files map[string]string)
		wantErr string
	}{
		{"missing harness package.json", func(files map[string]string) { delete(files, "harness/package.json") }, "package.json"},
		{"harness package.json without a version", func(files map[string]string) { files["harness/package.json"] = `{"name":"x"}` }, "has no version"},
		{"harness package.json not JSON", func(files map[string]string) { files["harness/package.json"] = `not json` }, "has no version"},
		{"missing peer target", func(files map[string]string) { delete(files, "shims/pi-agent-core.mjs") }, "shims/pi-agent-core.mjs is not in the runtime"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := syntheticRuntime(map[string]string{"main.mjs": "main"})
			tc.mutate(files)
			root, out := t.TempDir(), t.TempDir()
			writeTree(t, root, files)
			err := generate(root, filepath.Join(out, "runtime.pack"), filepath.Join(out, "digest.go"))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("generate error = %v, want one containing %q", err, tc.wantErr)
			}
			if _, statErr := os.Stat(filepath.Join(out, "runtime.pack")); statErr == nil {
				t.Fatal("generate wrote an archive for a runtime it rejected")
			}
		})
	}
}
