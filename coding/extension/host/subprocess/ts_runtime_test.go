//go:build !pig_strip_node_extensions && !windows

package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
)

// tsRuntimeProbe is an extension whose factory records the runtime that ran it.
const tsRuntimeProbe = `import fs from "node:fs";
export default function () {
  fs.appendFileSync(process.env.PIG_TS_PROBE_OUT, JSON.stringify({ name: process.env.PIG_EXT_NAME, fake: process.env.PIG_FAKE_RUNTIME ?? "", mode: process.env.PIG_FAKE_MODE ?? "", tree: process.env.PIG_TS_RUNTIME_TREE ?? "" }) + "\n");
}
`

type tsRuntimeProbeRecord struct {
	Name string `json:"name"`
	Fake string `json:"fake"`
	Mode string `json:"mode"`
	Tree string `json:"tree"`
}

func loadTSRuntimeProbes(t *testing.T, isolation string, names ...string) (*Host, []tsRuntimeProbeRecord) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "probe.jsonl")
	t.Setenv("PIG_TS_PROBE_OUT", out)
	var configs []ExtConfig
	for _, name := range names {
		source := filepath.Join(dir, name+".mjs")
		if err := os.WriteFile(source, []byte(tsRuntimeProbe), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: source, Enabled: true, Isolation: isolation})
	}
	h := newTestHost(t)
	t.Cleanup(func() { h.Shutdown("test done") })
	exts, errs := h.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(exts) != len(configs) {
		t.Fatalf("LoadAll: %d loaded, %v", len(exts), errs)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var records []tsRuntimeProbeRecord
	for line := range strings.Lines(string(data)) {
		var record tsRuntimeProbeRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return h, records
}

// A requested runtime that is not installed runs the extension on Node, and the Host records the typed reason.
func TestTypeScriptRuntimeFallsBackToNodeWithReason(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	t.Setenv(tsruntime.EnvRoot, t.TempDir())
	t.Setenv(tsruntime.EnvRuntime, "bun")
	h, records := loadTSRuntimeProbes(t, "isolated", "solo")
	if len(records) != 1 || records[0].Fake != "" || records[0].Tree != "" {
		t.Fatalf("records = %+v, want one Node run", records)
	}
	sel, ok := h.TypeScriptRuntimeSelections()["solo"]
	if !ok || !sel.IsNode() || sel.Fallback == nil || sel.Fallback.Reason != tsruntime.ReasonNotInstalled || sel.Request != (tsruntime.Request{ID: "bun", Source: tsruntime.SourceEnv}) {
		t.Fatalf("selection = %+v (fallback %+v)", sel, sel.Fallback)
	}
}

// installFakeExecutableRuntime installs a host executable that runs the tree's script on Node, marking the runs it served.
func installFakeExecutableRuntime(t *testing.T, root string) {
	t.Helper()
	installFakeRuntime(t, root, "fake", "1", "")
}

// warmFakeHost runs the script at path once, as the start check of a launch does (--version), and waits for it without a bound. The first run of a file a process
// has just written waits for the operating system's scan of a new executable: Start returns in about 3 ms and the script's exit then takes 2.9 s at the median
// and up to 22 s with a few dozen new programs starting at once, which exceeds the ten seconds the start check of a slot runtime gives its probe (tsruntime
// probeTimeout) and would have the fake runtime skipped. Once the scan is done the probe of the launch sees a warm executable.
func warmFakeHost(t *testing.T, path string) {
	t.Helper()
	if output, err := exec.CommandContext(t.Context(), path, "--version").CombinedOutput(); err != nil {
		t.Fatalf("%s --version: %v\n%s", path, err, output)
	}
}

// installFakeRuntime installs executable runtime id that marks its runs with PIG_FAKE_RUNTIME=marker; marker "fail" makes every
// launch exit 1 after answering --version.
func installFakeRuntime(t *testing.T, root, id, marker, fallback string) {
	t.Helper()
	run := `PIG_FAKE_RUNTIME=` + marker + ` PIG_FAKE_MODE=$mode exec node --import "$PIG_TS_RUNTIME_TREE/register-loader.mjs" "$PIG_TS_RUNTIME_TREE/$mode.mjs" "$@"`
	if marker == "fail" {
		run = `echo "this runtime cannot load extensions" >&2; exit 1`
	}
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo fake-1; exit 0; fi
mode=$1; shift
` + run + `
`
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	warmFakeHost(t, filepath.Join(dir, "host"))
	sum := sha256.Sum256([]byte(script))
	manifest := tsruntime.Manifest{ID: id, Kind: tsruntime.KindExecutable, Version: "1", Platform: tsruntime.Platform(), Provider: "test", Fallback: fallback, Image: &tsruntime.Image{Executable: "host", SHA256: hex.EncodeToString(sum[:])}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A slot runtime runs isolated and packed launches: each member's factory runs in the runtime the slot names, started in the
// script's mode with the runtime tree in PIG_TS_RUNTIME_TREE.
func TestTypeScriptRuntimeRunsASlotExecutable(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	root := t.TempDir()
	installFakeExecutableRuntime(t, root)
	t.Setenv(tsruntime.EnvRoot, root)
	t.Setenv(tsruntime.EnvRuntime, "fake")

	h, records := loadTSRuntimeProbes(t, "isolated", "solo")
	if len(records) != 1 || records[0].Fake != "1" || records[0].Mode != "cli" || !strings.HasSuffix(records[0].Tree, ".runtime") {
		t.Fatalf("isolated records = %+v", records)
	}
	if sel := h.TypeScriptRuntimeSelections()["solo"]; sel.Runtime != "fake" || sel.Fallback != nil || sel.Executable != filepath.Join(root, "fake", "host") {
		t.Fatalf("isolated selection = %+v (fallback %+v)", sel, sel.Fallback)
	}

	h, records = loadTSRuntimeProbes(t, "", "first", "second")
	names := []string{}
	for _, record := range records {
		if record.Fake != "1" || record.Mode != "cell" || filepath.Base(record.Tree) != "runtime" {
			t.Fatalf("packed record = %+v", record)
		}
		names = append(names, record.Name)
	}
	if !slices.Equal(names, []string{"first", "second"}) {
		t.Fatalf("packed members = %q", names)
	}
	selections := h.TypeScriptRuntimeSelections()
	if len(selections) != 1 {
		t.Fatalf("packed selections = %+v, want the one cell", selections)
	}
	for label, sel := range selections {
		if sel.Runtime != "fake" {
			t.Fatalf("selection %s = %+v", label, sel)
		}
	}
}

// The Bun launch reads no bunfig.toml, loads no .env file and installs nothing; a host executable takes the script's name as its mode.
func TestTypeScriptRuntimeCommandShapes(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	bun := typeScriptRuntimeCommand(t.Context(), tsruntime.Selection{Runtime: "bun", Kind: tsruntime.KindBun, Executable: "/slot/bun/bun"}, tree, "cell.mjs", "node cell loader", []string{"manifest.json", "/a.ts"})
	wantBun := []string{"/slot/bun/bun", "--no-install", "--no-env-file", "--config=" + filepath.Join(tree, "shims", "bunfig.toml"), "--preload", filepath.Join(tree, "register-loader.mjs"), filepath.Join(tree, "cell.mjs"), "manifest.json", "/a.ts"}
	if !slices.Equal(bun.Args, wantBun) {
		t.Fatalf("bun args = %q, want %q", bun.Args, wantBun)
	}
	if _, err := os.Stat(filepath.Join("runtime-node", "shims", "bunfig.toml")); err != nil {
		t.Fatalf("the runtime tree has no empty Bun config: %v", err)
	}
	exe := typeScriptRuntimeCommand(t.Context(), tsruntime.Selection{Runtime: "aot", Kind: tsruntime.KindExecutable, Executable: "/slot/aot/host"}, tree, "cli.mjs", "node extension loader", []string{"/a.ts"})
	if want := []string{"/slot/aot/host", "cli", "/a.ts"}; !slices.Equal(exe.Args, want) {
		t.Fatalf("executable args = %q, want %q", exe.Args, want)
	}
}

// A slot runtime that cannot start a set is skipped once: the isolated extension or the whole packed cell starts again on the
// runtime the failed one names as fallback, else on Node, with start_failed recorded.
func TestTypeScriptRuntimeRetriesAFailedStartOnTheNextRuntime(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	root := t.TempDir()
	installFakeRuntime(t, root, "broken", "fail", "")
	installFakeRuntime(t, root, "brokenfb", "fail", "good")
	installFakeRuntime(t, root, "good", "good", "")
	// chain fails, and so does its fallback brokenfb, whose own fallback is not followed.
	installFakeRuntime(t, root, "chain", "fail", "brokenfb")
	t.Setenv(tsruntime.EnvRoot, root)

	for _, c := range []struct {
		request, isolation, wantMarker, wantRuntime string
		names                                       []string
		wantSkipped                                 []string
	}{
		{"broken", "isolated", "", tsruntime.Node, []string{"solo"}, []string{"broken"}},
		{"broken", "", "", tsruntime.Node, []string{"first", "second"}, []string{"broken"}},
		{"brokenfb", "isolated", "good", "good", []string{"solo"}, []string{"brokenfb"}},
		{"brokenfb", "", "good", "good", []string{"first", "second"}, []string{"brokenfb"}},
		{"chain", "isolated", "", tsruntime.Node, []string{"solo"}, []string{"chain", "brokenfb"}},
		{"chain", "", "", tsruntime.Node, []string{"first", "second"}, []string{"chain", "brokenfb"}},
	} {
		t.Run(c.request+"/"+c.isolation, func(t *testing.T) {
			t.Setenv(tsruntime.EnvRuntime, c.request)
			h, records := loadTSRuntimeProbes(t, c.isolation, c.names...)
			if len(records) != len(c.names) {
				t.Fatalf("records = %+v", records)
			}
			for _, record := range records {
				if record.Fake != c.wantMarker {
					t.Fatalf("record %+v, want runtime marker %q", record, c.wantMarker)
				}
			}
			selections := h.TypeScriptRuntimeSelections()
			if len(selections) != 1 {
				t.Fatalf("selections = %+v", selections)
			}
			for _, sel := range selections {
				var skipped []string
				for _, skip := range sel.Skipped {
					if skip.Reason != tsruntime.ReasonStartFailed {
						t.Fatalf("skip %+v", skip)
					}
					skipped = append(skipped, skip.Runtime)
				}
				if sel.Runtime != c.wantRuntime || sel.Fallback == nil || sel.Fallback.Reason != tsruntime.ReasonStartFailed || !slices.Equal(skipped, c.wantSkipped) {
					t.Fatalf("selection = %+v (fallback %+v, skipped %+v)", sel, sel.Fallback, sel.Skipped)
				}
			}
		})
	}
}

// A project's .env files do not reach the extension process on any runtime, as on Node. Pi builds its Bun binaries with
// --no-compile-autoload-dotenv so that project env files do not leak into its environment (scripts/build-binaries.sh:129-135, Pi
// #10473); stock Bun loads .env, .env.local and .env.<NODE_ENV> from its cwd, which is the project. The test runs on the runtime this
// process requests, so the Bun row of the runtime matrix (PIG_TS_RUNTIME=bun with Bun in the slot) checks the Bun launch.
func TestTypeScriptRuntimeIgnoresProjectEnvFiles(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	project := t.TempDir()
	for _, name := range []string{".env", ".env.local", ".env.development", ".env.production"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("PIG_TS_PROBE_DOTENV=leaked-from-"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "probe.txt")
	t.Setenv("PIG_TS_PROBE_OUT", out)
	source := filepath.Join(dir, "dotenv.mjs")
	probe := `import fs from "node:fs";
export default function () {
  fs.appendFileSync(process.env.PIG_TS_PROBE_OUT, (process.env.PIG_TS_PROBE_DOTENV ?? "unset") + "\n");
}
`
	if err := os.WriteFile(source, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	request := TypeScriptRuntimeRequest()
	for _, isolation := range []string{"isolated", ""} {
		if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		h := NewHost(project)
		exts, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "dotenv", Source: source, Enabled: true, Isolation: isolation}})
		selections := h.TypeScriptRuntimeSelections()
		h.Shutdown("test done")
		if len(errs) != 0 || len(exts) != 1 {
			t.Fatalf("isolation %q: LoadAll: %d loaded, %v", isolation, len(exts), errs)
		}
		for label, sel := range selections {
			if sel.Runtime != request.ID {
				t.Fatalf("isolation %q: %s ran on %s, the process requests %s (skipped %+v)", isolation, label, sel.Runtime, request.ID, sel.Skipped)
			}
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(data)); got != "unset" {
			t.Errorf("isolation %q on %s: the extension read PIG_TS_PROBE_DOTENV=%s from the project's env files", isolation, request.ID, got)
		}
	}
}

// An extension that imports a package nobody installed fails to load on every runtime, as on Node, and the runtime asks no
// registry for it. Stock Bun installs a missing package on import when no node_modules directory is in reach; PiG starts it with
// --no-install. Every registry variable points at a local server that counts requests, so an install attempt fails the test even
// offline. The test runs on the runtime this process requests, so the Bun row of the runtime matrix checks the Bun launch.
func TestTypeScriptRuntimeNeverInstallsMissingPackages(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	var requests atomic.Int64
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(registry.Close)
	for _, name := range []string{"BUN_CONFIG_REGISTRY", "NPM_CONFIG_REGISTRY", "npm_config_registry"} {
		t.Setenv(name, registry.URL)
	}
	const missing = "pig-ts-probe-missing-package"
	dir := t.TempDir()
	source := filepath.Join(dir, "missing.mjs")
	if err := os.WriteFile(source, []byte(fmt.Sprintf("import probe from %q;\nexport default function () { probe(); }\n", missing)), 0o644); err != nil {
		t.Fatal(err)
	}
	request := TypeScriptRuntimeRequest()
	for _, isolation := range []string{"isolated", ""} {
		h := NewHost(t.TempDir())
		exts, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "missing", Source: source, Enabled: true, Isolation: isolation}})
		selections := h.TypeScriptRuntimeSelections()
		h.Shutdown("test done")
		if len(exts) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), missing) {
			t.Fatalf("isolation %q on %s: LoadAll: %d loaded, %v; want one load error naming %s", isolation, request.ID, len(exts), errs, missing)
		}
		for label, sel := range selections {
			if sel.Runtime != request.ID && !slices.ContainsFunc(sel.Skipped, func(skip tsruntime.Skip) bool { return skip.Runtime == request.ID }) {
				t.Fatalf("isolation %q: %s ran on %s, the process requests %s", isolation, label, sel.Runtime, request.ID)
			}
		}
		if n := requests.Load(); n != 0 {
			t.Fatalf("isolation %q on %s: the runtime sent %d registry requests for a missing package", isolation, request.ID, n)
		}
	}
}

// Only a process that started on a slot runtime and then exited or failed to register retries on the next runtime. An error the host
// raises after the register handshake (a provider registration failure, for example) is not a runtime start failure: retrying it
// would run the factory a second time after part of its registration was applied.
func TestTypeScriptRuntimeRetriesOnlyStartFailures(t *testing.T) {
	t.Setenv(tsruntime.EnvRoot, t.TempDir())
	slot := tsruntime.Selection{Request: tsruntime.Request{ID: "bun", Source: tsruntime.SourceEnv}, Runtime: "bun", Kind: tsruntime.KindBun}
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"provider registration error", errors.New(`provider "p" is already registered`), false},
		{"factory failure", newLoadError("solo", "load", "load_failed", errors.New("boom")), false},
		{"module failed before its factory", newLoadError("solo", "load", "load_failed", &FactoryLoadError{Message: "Failed to load extension: x", Phase: LoadPhaseImport}), true},
		{"factory threw", newLoadError("solo", "load", "load_failed", &FactoryLoadError{Message: "Failed to load extension: x", Phase: LoadPhaseFactory}), false},
		{"load failure without a phase", newLoadError("solo", "load", "load_failed", &FactoryLoadError{Message: "Failed to load extension: x"}), false},
		{"packed member factory threw", &packedAcceptError{perMember: map[string]error{"a": newLoadError("a", "load", "load_failed", &FactoryLoadError{Message: "Failed to load extension: x", Phase: LoadPhaseFactory})}}, false},
		{"process exited", newLoadError("solo", "connect", "process_exited", errors.New("exit 1")), true},
		{"register failed", newLoadError("solo", "register", "register_failed", errors.New("eof")), true},
		{"spawn failed", newLoadError("solo", "spawn", "spawn_failed", errors.New("ENOEXEC")), true},
		{"cell exited before admission", errNodeCellExitedBeforeAdmission, true},
		{"admission write failed", &admissionWriteError{err: errors.New("write |1: broken pipe")}, true},
		{"packed members all exited", &packedAcceptError{perMember: map[string]error{"a": newLoadError("a", "connect", "process_exited", errors.New("exit 1")), "b": errNodeCellExitedBeforeAdmission}}, true},
		{"packed member registration error", &packedAcceptError{perMember: map[string]error{"a": newLoadError("a", "connect", "process_exited", errors.New("exit 1")), "b": errors.New(`provider "p" is already registered`)}}, false},
		{"packed with no member errors", &packedAcceptError{perMember: map[string]error{}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newTestHost(t)
			h.tsRuntimes.selections = map[string]tsruntime.Selection{"solo": slot}
			h.tsRuntimes.sets = map[string]tsruntime.Set{"solo": {}}
			if got := h.retryOnNextRuntime(t.Context(), "solo", c.err); got != c.want {
				t.Fatalf("retryOnNextRuntime(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// slotOnlyThrowProbe is an extension whose factory records the runtime that ran it. On a slot runtime, the member PIG_TS_THROW names
// fails to evaluate, as an extension that imports an API the runtime lacks does.
const slotOnlyThrowProbe = `import fs from "node:fs";
if (process.env.PIG_EXT_NAME === process.env.PIG_TS_THROW && process.env.PIG_FAKE_RUNTIME) throw new Error("this runtime lacks an API");
export default function () {
  fs.appendFileSync(process.env.PIG_TS_PROBE_OUT, process.env.PIG_EXT_NAME + ":" + (process.env.PIG_FAKE_RUNTIME || "node") + "\n");
}
`

// A later member of a packed cell that fails to start on a slot runtime cannot start again on Node without running the earlier
// members' factories twice, so its failure is this launch's load error. The same extension isolated, or first in its cell, loads on
// Node, so the set is recorded as Node-only and its next launch loads every member on Node instead of failing the member again.
func TestTypeScriptRuntimeLaterPackedMemberFailureKeepsTheSetOnNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	root := t.TempDir()
	installFakeRuntime(t, root, "good", "good", "")
	t.Setenv(tsruntime.EnvRoot, root)
	t.Setenv(tsruntime.EnvRuntime, "good")
	t.Setenv("PIG_TS_THROW", "second")
	dir := t.TempDir()
	out := filepath.Join(dir, "probe.txt")
	t.Setenv("PIG_TS_PROBE_OUT", out)
	var configs []ExtConfig
	for _, name := range []string{"first", "second"} {
		source := filepath.Join(dir, name+".mjs")
		if err := os.WriteFile(source, []byte(slotOnlyThrowProbe), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: source, Enabled: true})
	}
	launch := func() (int, []error, []string) {
		if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		h := newTestHost(t)
		defer h.Shutdown("test done")
		exts, errs := h.LoadAll(t.Context(), configs)
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return len(exts), errs, strings.Fields(string(data))
	}
	loaded, errs, runs := launch()
	if loaded != 1 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "this runtime lacks an API") || !slices.Equal(runs, []string{"first:good"}) {
		t.Fatalf("first launch: %d loaded, errors %v, factory runs %q", loaded, errs, runs)
	}
	if records := tsruntime.ListNodeOnly(root); len(records) != 1 || len(records[0].Members) != 2 {
		t.Fatalf("Node-only records after the first launch = %+v, want the cell's set", records)
	}
	loaded, errs, runs = launch()
	if loaded != 2 || len(errs) != 0 || !slices.Equal(runs, []string{"first:node", "second:node"}) {
		t.Fatalf("second launch: %d loaded, errors %v, factory runs %q", loaded, errs, runs)
	}
}

// Node reads an absolute path as a URL path only in an import: require() opens the path as written, and import() decodes its percent
// escapes. An extension on any runtime gets the file Node gets for each.
func TestTypeScriptRuntimeAbsolutePathSpecifiersOpenTheFilesNodeOpens(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	for name, label := range map[string]string{"p%20q": "as written", "p q": "decoded"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "m.cjs"), []byte(fmt.Sprintf("module.exports = %q;\n", label)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "e.mjs"), []byte(fmt.Sprintf("export default %q;\n", label)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	literal := filepath.Join(dir, "p%20q")
	source := filepath.Join(dir, "paths.mjs")
	out := filepath.Join(dir, "out.json")
	probe := fmt.Sprintf(`import fs from "node:fs";
import { createRequire } from "node:module";
const required = createRequire(import.meta.url)(%q);
const imported = (await import(%q)).default;
export default function () { fs.writeFileSync(%q, JSON.stringify({ required, imported })); }
`, filepath.Join(literal, "m.cjs"), filepath.Join(literal, "e.mjs"), out)
	if err := os.WriteFile(source, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, isolation := range []string{"isolated", ""} {
		_ = os.Remove(out)
		h := NewHost(t.TempDir())
		exts, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "paths", Source: source, Enabled: true, Isolation: isolation}})
		h.Shutdown("test done")
		if len(exts) != 1 || len(errs) != 0 {
			t.Fatalf("isolation %q: LoadAll: %d loaded, %v", isolation, len(exts), errs)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(data), `{"required":"as written","imported":"decoded"}`; got != want {
			t.Fatalf("isolation %q on %s: %s, want %s", isolation, TypeScriptRuntimeRequest().ID, got, want)
		}
	}
}
