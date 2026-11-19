//go:build !pig_strip_node_extensions

package subprocess

// pi: packages/ai/package.json (exports)
// pi: packages/coding-agent/src/core/extensions/loader.ts
// pi: packages/coding-agent/src/core/extensions/virtual-modules.ts

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// piAiSubpathSpecifiers lists every specifier the pinned @earendil-works/pi-ai package.json "exports" names, a pattern expanded over the files of the pinned package's dist directory, for each of the two package scopes Pi's extension loader aliases.
func piAiSubpathSpecifiers(t *testing.T, aiRoot string) []string {
	t.Helper()
	var manifest struct {
		Exports map[string]json.RawMessage `json:"exports"`
	}
	data, err := os.ReadFile(filepath.Join(aiRoot, "package.json"))
	if err != nil {
		t.Fatalf("pinned pi-ai package missing (run make parity-deps): %v", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var subpaths []string
	for key := range manifest.Exports {
		dir, pattern := strings.CutSuffix(strings.TrimPrefix(key, "./"), "/*")
		if !pattern {
			subpaths = append(subpaths, strings.TrimPrefix(key, "."))
			continue
		}
		entries, err := os.ReadDir(filepath.Join(aiRoot, "dist", dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if name, ok := strings.CutSuffix(entry.Name(), ".js"); ok && !entry.IsDir() {
				subpaths = append(subpaths, "/"+dir+"/"+name)
			}
		}
	}
	sort.Strings(subpaths)
	var specifiers []string
	for _, scope := range []string{"@earendil-works", "@mariozechner"} {
		for _, subpath := range subpaths {
			specifiers = append(specifiers, scope+"/pi-ai"+subpath)
		}
	}
	return specifiers
}

// piAiSubpathProbe is an extension that imports each specifier by dynamic import and reports every export's name and typeof, or the first line of the import error.
func piAiSubpathProbe(specifiers []string) string {
	list, _ := json.Marshal(specifiers)
	return fmt.Sprintf(`export default async function (pi) {
  const out = {};
  for (const spec of %s) {
    try {
      const ns = await import(spec);
      out[spec] = { names: Object.fromEntries(Object.keys(ns).sort().map((k) => [k, typeof ns[k]])) };
    } catch (e) {
      out[spec] = { error: String(e && e.message).split("\n")[0] };
    }
  }
  pi.registerCommand("probe", { description: JSON.stringify(out), handler: async () => {} });
}
`, list)
}

type subpathOutcome struct {
	Names map[string]string `json:"names"`
	Error string            `json:"error"`
}

// TestExtensionImportsOfPiAiSubpathsResolveAsUnderPi loads one real extension, through the Node runtime as the host starts it, that imports every subpath the pinned pi-ai package exports (the root, compat, models, oauth, providers/*, api/*, utils/*, bedrock-provider, bun-oauth, lazy-bedrock-provider, lazy-oauth), under the @earendil-works and the @mariozechner scope, and compares each import with the same extension under the pinned Pi's two extension resolution modes: the unbundled build (jiti prefix aliases) and the bundled build (VIRTUAL_MODULES, exact specifiers). A specifier Pi serves must serve the same export names with the same kinds; a specifier Pi cannot resolve must fail in PiG as well, never answer with a stand-in value.
func TestExtensionImportsOfPiAiSubpathsResolveAsUnderPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Pi extension probe: %v", err)
	}
	modRoot := findModuleRoot(t)
	agent := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	specifiers := piAiSubpathSpecifiers(t, filepath.Join(agent, "node_modules", "@earendil-works", "pi-ai"))
	probe := filepath.Join(t.TempDir(), "pi-ai-subpaths.js")
	if err := os.WriteFile(probe, []byte(piAiSubpathProbe(specifiers)), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("testdata", "pi_ai_subpaths_pi.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	read := func(description string) map[string]subpathOutcome {
		var table map[string]subpathOutcome
		if err := json.Unmarshal([]byte(description), &table); err != nil {
			t.Fatalf("undecodable probe table %q: %v", description, err)
		}
		return table
	}
	pi := map[string]map[string]subpathOutcome{}
	for _, mode := range []string{"alias", "virtual"} {
		cmd := exec.CommandContext(t.Context(), node, script, mode, agent, probe)
		cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("Pi %s mode: %v\n%s", mode, err, out)
		}
		pi[mode] = read(string(out))
	}

	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	host.SetUIBridge(NewUIBridge(func() {}))
	ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "pi-ai-subpaths", Source: probe, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pig := read(ext.Commands["probe"].Description)

	resolved := 0
	for _, spec := range specifiers {
		got, ok := pig[spec]
		if !ok {
			t.Errorf("%s: PiG's extension reported no outcome", spec)
			continue
		}
		for _, mode := range []string{"virtual", "alias"} {
			want := pi[mode][spec]
			switch {
			case want.Error != "" && got.Error != "":
			case want.Error != "":
				t.Errorf("%s: Pi (%s mode) cannot resolve it (%s); PiG resolves it with %d export names", spec, mode, want.Error, len(got.Names))
			case got.Error != "":
				t.Errorf("%s: Pi (%s mode) resolves it with %d export names; PiG fails: %s", spec, mode, len(want.Names), got.Error)
			default:
				for name, kind := range want.Names {
					if gotKind, ok := got.Names[name]; !ok {
						t.Errorf("%s: Pi (%s mode) exports %s (%s); PiG does not", spec, mode, name, kind)
					} else if gotKind != kind {
						t.Errorf("%s: %s is %s in Pi (%s mode), %s in PiG", spec, name, kind, mode, gotKind)
					}
				}
				for name := range got.Names {
					if _, ok := want.Names[name]; !ok {
						t.Errorf("%s: PiG exports %s, which Pi (%s mode) does not", spec, name, mode)
					}
				}
			}
		}
		if got.Error == "" {
			resolved++
		}
	}
	if len(specifiers) < 150 || resolved < 5 {
		t.Fatalf("the probe imported %d specifiers and %d resolved in PiG; it reads the wrong package", len(specifiers), resolved)
	}
	var served []string
	for spec, outcome := range pi["virtual"] {
		if outcome.Error == "" {
			served = append(served, spec)
		}
	}
	slices.Sort(served)
	t.Logf("Pi (bundled resolution) serves %d of %d specifiers: %v", len(served), len(specifiers), served)
}
