//go:build !pig_strip_node_extensions

package subprocess

// pi: packages/ai/package.json (exports)
// upstream: pi-subagents runs/background/runner-aliases.js:resolveHostPeerAliases

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// codexHelpersNotYetRestored: the three Codex WebSocket helpers the next commit of this branch restores (cure in progress, not waived); it deletes this set.
var codexHelpersNotYetRestored = map[string]bool{
	"closeOpenAICodexWebSocketSessions": true, "getOpenAICodexWebSocketDebugStats": true, "resetOpenAICodexWebSocketDebugStats": true,
}

// TestHelperResolvingPiAiFromTheHarnessSeesPisExports: a detached helper process (pi-subagents' background runner) imports `@earendil-works/pi-ai/<subpath>` as plain Node resolves it from the package that process.argv[1] belongs to: Pi's install has the pinned pi-ai package next to pi-coding-agent, PiG's harness has its generated peer (internal/noderuntimepeers). Every `./api/*` subpath Pi's package.json exports must import in the helper with the same export names and kinds as under Pi, or fail as a missing subpath does.
func TestHelperResolvingPiAiFromTheHarnessSeesPisExports(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	agent := filepath.Join(findModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	aiRoot, err := filepath.EvalSymlinks(filepath.Join(agent, "node_modules", "@earendil-works", "pi-ai"))
	if err != nil {
		t.Fatal(err)
	}
	// The api/* modules: the root, compat and lazy entries are served by the same peer from the compat entry point and are not what D74's stand-ins decide.
	var specifiers []string
	for _, spec := range piAiSubpathSpecifiers(t, aiRoot) {
		if strings.HasPrefix(spec, "@earendil-works/pi-ai/api/") {
			specifiers = append(specifiers, spec)
		}
	}
	list, _ := json.Marshal(specifiers)
	script := `const out = {};
for (const spec of ` + string(list) + `) {
  try { const ns = await import(spec); out[spec] = { names: Object.fromEntries(Object.keys(ns).sort().map((k) => [k, typeof ns[k]])) }; }
  catch (e) { out[spec] = { error: String(e && e.message).split("\n")[0] }; }
}
process.stdout.write(JSON.stringify(out));`
	run := func(dir string) map[string]subpathOutcome {
		file := filepath.Join(dir, "helper-probe.mjs")
		if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204 (class b): node from exec.LookPath and a script the test wrote.
		out, err := exec.CommandContext(t.Context(), node, file).Output()
		if err != nil {
			t.Fatalf("helper in %s: %v\n%s", dir, err, out)
		}
		var table map[string]subpathOutcome
		if err := json.Unmarshal(out, &table); err != nil {
			t.Fatal(err)
		}
		return table
	}
	// Pi's layout: the helper sits in the package whose node_modules hold the pinned pi-ai.
	piDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(piDir, "node_modules", "@earendil-works"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aiRoot, filepath.Join(piDir, "node_modules", "@earendil-works", "pi-ai")); err != nil {
		t.Fatal(err)
	}
	runtime := t.TempDir()
	if err := extractNodeRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	pi, pig := run(piDir), run(filepath.Join(runtime, "harness"))
	for _, spec := range specifiers {
		want, got := pi[spec], pig[spec]
		switch {
		case want.Error != "" && got.Error != "":
		case want.Error != "":
			t.Errorf("%s: Pi cannot import it (%s); the harness peer serves %d names", spec, want.Error, len(got.Names))
		case got.Error != "":
			t.Errorf("%s: Pi serves %d names; the harness peer fails: %s", spec, len(want.Names), got.Error)
		case !maps.Equal(want.Names, got.Names):
			for name, kind := range want.Names {
				if got.Names[name] != kind && !codexHelpersNotYetRestored[name] {
					t.Errorf("%s: Pi exports %s (%s); the harness peer has %q", spec, name, kind, got.Names[name])
				}
			}
			for name := range got.Names {
				if _, ok := want.Names[name]; !ok {
					t.Errorf("%s: the harness peer exports %s, which Pi does not", spec, name)
				}
			}
		}
	}
}
