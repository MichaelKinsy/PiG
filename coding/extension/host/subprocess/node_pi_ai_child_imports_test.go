package subprocess

// pi: packages/coding-agent/src/core/extensions/loader.ts

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const childProbeShared = `
async function probe(spec) {
  let file;
  try { file = import.meta.resolve(spec); } catch (e) { return { error: String(e && e.message).split("\n")[0] }; }
  try {
    const ns = await import(spec);
    return { file, names: Object.fromEntries(Object.keys(ns).sort().map((k) => [k, typeof ns[k]])) };
  } catch (e) { return { file, error: String(e && e.message).split("\n")[0] }; }
}
async function probeAll(specs) {
  const out = {};
  for (const spec of specs) out[spec] = await probe(spec);
  return out;
}
`

type childOutcome struct {
	File  string            `json:"file"`
	Names map[string]string `json:"names"`
	Error string            `json:"error"`
}

// childProbeExtension starts a worker thread and a forked child, each of which imports the specifiers; the extension reports both tables.
func childProbeExtension(dir string, specifiers []string) string {
	list, _ := json.Marshal(specifiers)
	worker, child := filepath.Join(dir, "worker.mjs"), filepath.Join(dir, "child.mjs")
	return fmt.Sprintf(`import { Worker } from "node:worker_threads";
import { fork } from "node:child_process";
export default async function (pi) {
  const specs = %s;
  const worker = await new Promise((resolve, reject) => {
    const w = new Worker(%q, { workerData: specs });
    w.once("message", resolve); w.once("error", reject);
  });
  const child = await new Promise((resolve, reject) => {
    const c = fork(%q, [JSON.stringify(specs)]);
    c.once("message", (m) => { resolve(m); c.kill(); }); c.once("error", reject);
  });
  pi.registerCommand("probe", { description: JSON.stringify({ worker, child }), handler: async () => {} });
}
`, list, worker, child)
}

// TestChildAndWorkerImportsOfPiAiResolveAsUnderPi: code an extension starts without an extension runtime (a worker_threads Worker, a forked child) is loaded by Node itself. The same extension runs under the pinned Pi and under PiG, in two set-ups: the extension's package has its own node_modules/@earendil-works/pi-ai (the pinned package), or has none; the worker and the child import the pi-ai root, /compat, /models, /oauth, /providers/all and some /api and /utils subpaths.
func TestChildAndWorkerImportsOfPiAiResolveAsUnderPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Pi extension probe: %v", err)
	}
	modRoot := findModuleRoot(t)
	agent := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	aiRoot, err := filepath.EvalSymlinks(filepath.Join(agent, "node_modules", "@earendil-works", "pi-ai"))
	if err != nil {
		t.Fatal(err)
	}
	var specifiers []string
	for _, sub := range []string{"", "/compat", "/models", "/oauth", "/providers/all", "/api/openai-completions", "/api/pi-messages", "/api/openai-codex-responses", "/api/google-shared", "/utils/uuid"} {
		specifiers = append(specifiers, "@earendil-works/pi-ai"+sub)
	}
	script, err := filepath.Abs(filepath.Join("testdata", "pi_ai_subpaths_pi.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range []string{"own-node-modules", "no-node-modules"} {
		dir := t.TempDir()
		if setup == "own-node-modules" {
			if err := os.MkdirAll(filepath.Join(dir, "node_modules", "@earendil-works"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(aiRoot, filepath.Join(dir, "node_modules", "@earendil-works", "pi-ai")); err != nil {
				t.Fatal(err)
			}
		}
		for name, source := range map[string]string{
			"worker.mjs": `import { parentPort, workerData } from "node:worker_threads";` + childProbeShared + `parentPort.postMessage(await probeAll(workerData));`,
			"child.mjs":  childProbeShared + `process.send(await probeAll(JSON.parse(process.argv[2])));`,
			"index.js":   childProbeExtension(dir, specifiers),
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		extension := filepath.Join(dir, "index.js")
		type tables struct {
			Worker, Child map[string]childOutcome
		}
		read := func(description string) tables {
			var out tables
			if err := json.Unmarshal([]byte(description), &out); err != nil {
				t.Fatalf("undecodable probe table %q: %v", description, err)
			}
			return out
		}
		//nolint:gosec // G204 (class b): node from exec.LookPath, a testdata script and paths the test built.
		cmd := exec.CommandContext(t.Context(), node, script, "virtual", agent, extension)
		cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: Pi: %v\n%s", setup, err, out)
		}
		pi := read(string(out))
		host := NewHost(t.TempDir())
		host.SetUIBridge(NewUIBridge(func() {}))
		ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "child-" + setup, Source: extension, Enabled: true})
		if err != nil {
			host.Shutdown("test done")
			t.Fatalf("%s: PiG: %v", setup, err)
		}
		pig := read(ext.Commands["probe"].Description)
		host.Shutdown("test done")
		served := map[string]bool{}
		for _, sub := range []string{"", "/compat", "/models", "/oauth", "/providers/all"} {
			served["@earendil-works/pi-ai"+sub] = true
		}
		for _, kind := range []string{"worker", "child"} {
			piT, pigT := pi.Worker, pig.Worker
			if kind == "child" {
				piT, pigT = pi.Child, pig.Child
			}
			for _, spec := range specifiers {
				want, got := piT[spec], pigT[spec]
				label := setup + " " + kind + " " + spec
				if served[spec] {
					// The loader hooks the worker and the child inherit (execArgv) serve these five specifiers from PiG's runtime: where Pi loads the extension's own copy PiG serves its module, which has every name Pi's has, and where Pi finds no package PiG still serves one.
					if got.Error != "" || got.File == "" {
						t.Errorf("%s: PiG fails: %s", label, got.Error)
					}
					if setup == "no-node-modules" && want.Error == "" {
						t.Errorf("%s: Pi resolves it with no package installed", label)
					}
					for name, typ := range want.Names {
						if got.Names[name] != typ {
							t.Errorf("%s: Pi exports %s (%s); PiG has %q", label, name, typ, got.Names[name])
						}
					}
					continue
				}
				// Every other subpath is Node's own resolution in both: the extension's copy of pi-ai (the same file, with Pi's own exports) or the same failure.
				if got.File != want.File || got.Error != want.Error || !maps.Equal(got.Names, want.Names) {
					t.Errorf("%s differs\n Pi : file=%s error=%q names=%v\n PiG: file=%s error=%q names=%v", label, want.File, want.Error, want.Names, got.File, got.Error, got.Names)
				}
				if setup == "own-node-modules" && (want.Error != "" || len(want.Names) == 0) {
					t.Errorf("%s: Pi does not load the extension's own module: %q", label, want.Error)
				}
			}
		}
	}
}
