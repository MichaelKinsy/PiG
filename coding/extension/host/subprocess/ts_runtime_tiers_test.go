//go:build !pig_strip_node_extensions && !windows

package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
)

// tierProbe records the runtime that ran it and its process. When PIG_TS_PROBE_THROW_ON names the runtime it runs on, the module fails to evaluate, as an
// extension does that imports a Node API the runtime lacks. PIG_TS_PROBE_THROW_ALL makes the factory throw on every runtime, as an extension's own bug does.
const tierProbe = `import fs from "node:fs";
const fake = process.env.PIG_FAKE_RUNTIME ?? "";
if (fake && (process.env.PIG_TS_PROBE_THROW_ON ?? "").split(",").includes(fake + ":" + process.env.PIG_EXT_NAME)) throw new Error("node:inspector is not implemented on " + fake);
export default function () {
  if (process.env.PIG_TS_PROBE_THROW_ALL) throw new Error("the extension's own bug");
  fs.appendFileSync(process.env.PIG_TS_PROBE_OUT, JSON.stringify({ name: process.env.PIG_EXT_NAME, fake, pid: process.pid }) + "\n");
}
`

type tierRecord struct {
	Name string `json:"name"`
	Fake string `json:"fake"`
	Pid  int    `json:"pid"`
}

// fakeTier installs a slot runtime of kind executable. Its script counts each start in $PIG_FAKE_LOG, then runs the tree's script on Node as runtime id, or
// exits at once when fail is set, as a runtime that cannot start the extension host does.
func fakeTier(t *testing.T, root, id, fallback string, fail bool, inputs []tsruntime.Input) {
	t.Helper()
	body := `exec env PIG_FAKE_RUNTIME=` + id + ` node --import "$PIG_TS_RUNTIME_TREE/register-loader.mjs" "$PIG_TS_RUNTIME_TREE/$mode.mjs" "$@"`
	if fail {
		body = `echo "` + id + ` cannot run this extension" >&2; exit 3`
	}
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo fake-1; exit 0; fi\nmode=$1; shift\necho " + id + " >> \"$PIG_FAKE_LOG\"\n" + body + "\n"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	warmFakeHost(t, filepath.Join(dir, "host"))
	sum := sha256.Sum256([]byte(script))
	manifest := tsruntime.Manifest{ID: id, Kind: tsruntime.KindExecutable, Version: "1", Platform: tsruntime.Platform(), Provider: "test", Fallback: fallback,
		Image: &tsruntime.Image{Executable: "host", SHA256: hex.EncodeToString(sum[:]), Inputs: inputs}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type tierFixture struct {
	t       *testing.T
	root    string
	out     string
	log     string
	dir     string
	configs []ExtConfig
	// ctx lives as long as the test: a Host ties its extension processes to the context of the load that started them.
	ctx context.Context
}

func newTierFixture(t *testing.T, isolation string, names ...string) *tierFixture {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	f := &tierFixture{t: t, root: t.TempDir(), dir: t.TempDir()}
	var cancel context.CancelFunc
	f.ctx, cancel = context.WithTimeout(t.Context(), 5*time.Minute)
	t.Cleanup(cancel)
	f.out, f.log = filepath.Join(f.dir, "probe.jsonl"), filepath.Join(f.dir, "starts.log")
	t.Setenv("PIG_TS_PROBE_OUT", f.out)
	t.Setenv("PIG_FAKE_LOG", f.log)
	t.Setenv(tsruntime.EnvRoot, f.root)
	t.Setenv(tsruntime.EnvRuntime, "tier1")
	for _, name := range names {
		source := filepath.Join(f.dir, name+".mjs")
		if err := os.WriteFile(source, []byte(tierProbe), 0o644); err != nil {
			t.Fatal(err)
		}
		f.configs = append(f.configs, ExtConfig{Name: name, Source: source, Enabled: true, Isolation: isolation})
	}
	return f
}

func (f *tierFixture) host() *Host {
	f.t.Helper()
	h := NewHost(f.t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return f.configs, nil })
	f.t.Cleanup(func() { h.Shutdown("test done") })
	return h
}

func (f *tierFixture) reload(h *Host) {
	f.t.Helper()
	exts, err := h.Reload(f.ctx)
	if err != nil || len(exts) != len(f.configs) {
		f.t.Fatalf("Reload: %d of %d extensions loaded, %v (issues %v)", len(exts), len(f.configs), err, h.LastReloadReport().Issues)
	}
}

func (f *tierFixture) records() []tierRecord {
	f.t.Helper()
	data, _ := os.ReadFile(f.out)
	var records []tierRecord
	for line := range strings.Lines(string(data)) {
		var record tierRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			f.t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func (f *tierFixture) starts() []string {
	data, _ := os.ReadFile(f.log)
	return strings.Fields(string(data))
}

func (f *tierFixture) selection(h *Host, label string) tsruntime.Selection {
	f.t.Helper()
	sel, ok := h.TypeScriptRuntimeSelections()[label]
	if !ok {
		f.t.Fatalf("no selection for %q in %v", label, h.TypeScriptRuntimeSelections())
	}
	return sel
}

func TestTypeScriptRuntimeNodeOnlyExtensionFallsBackToNodeAndIsRemembered(t *testing.T) {
	f := newTierFixture(t, "isolated", "solo")
	fakeTier(t, f.root, "tier1", "tier2", false, nil)
	fakeTier(t, f.root, "tier2", "", false, nil)
	t.Setenv("PIG_TS_PROBE_THROW_ON", "tier1:solo,tier2:solo")
	h := f.host()
	f.reload(h)
	records := f.records()
	if len(records) != 1 || records[0].Fake != "" {
		t.Fatalf("records = %+v, want one run on Node", records)
	}
	if got := f.starts(); !slices.Equal(got, []string{"tier1", "tier2"}) {
		t.Fatalf("starts = %v, want one try on each tier", got)
	}
	sel := f.selection(h, "solo")
	if !sel.IsNode() || len(sel.Skipped) != 2 || sel.Skipped[0].Reason != tsruntime.ReasonStartFailed || sel.Skipped[1].Runtime != "tier2" || !strings.Contains(sel.Skipped[1].Detail, "node:inspector is not implemented on tier2") {
		t.Fatalf("selection = %+v skipped %+v", sel, sel.Skipped)
	}
	h.tsRuntimes.mu.Lock()
	set := h.tsRuntimes.sets["solo"]
	h.tsRuntimes.mu.Unlock()
	if _, ok := tsruntime.ReadNodeOnly(f.root, set); !ok {
		t.Fatalf("the Node-only set was not recorded")
	}
	if listed := tsruntime.ListNodeOnly(f.root); len(listed) != 1 || listed[0].Members[0].Name != "solo" {
		t.Fatalf("ListNodeOnly = %+v", listed)
	}

	// The next start goes straight to Node.
	h2 := f.host()
	f.reload(h2)
	if got := f.starts(); !slices.Equal(got, []string{"tier1", "tier2"}) {
		t.Fatalf("starts after the next start = %v, want no new tier attempt", got)
	}
	sel = f.selection(h2, "solo")
	if !sel.IsNode() || sel.Fallback == nil || sel.Fallback.Reason != tsruntime.ReasonNodeOnly || !strings.Contains(sel.Fallback.Detail, "node:inspector is not implemented on tier1") {
		t.Fatalf("second selection = %+v (%+v)", sel, sel.Fallback)
	}
}

// A module that exports no factory on a slot runtime failed before any factory ran (Pi checks the export before it calls the factory, loader.ts:645-648), so
// it starts again on the next runtime, as a module that fails to evaluate does.
func TestTypeScriptRuntimeModuleWithoutAFactoryOnASlotRuntimeStartsOnNode(t *testing.T) {
	f := newTierFixture(t, "isolated", "solo")
	fakeTier(t, f.root, "tier1", "", false, nil)
	const probe = `import fs from "node:fs";
const fake = process.env.PIG_FAKE_RUNTIME ?? "";
function install() {
  fs.appendFileSync(process.env.PIG_TS_PROBE_OUT, JSON.stringify({ name: process.env.PIG_EXT_NAME, fake, pid: process.pid }) + "\n");
}
export default fake ? { install } : install;
`
	if err := os.WriteFile(f.configs[0].Source, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	h := f.host()
	f.reload(h)
	if records := f.records(); len(records) != 1 || records[0].Fake != "" {
		t.Fatalf("records = %+v, want one run on Node", records)
	}
	if got := f.starts(); !slices.Equal(got, []string{"tier1"}) {
		t.Fatalf("starts = %v, want one start on tier1", got)
	}
	sel := f.selection(h, "solo")
	if !sel.IsNode() || len(sel.Skipped) != 1 || sel.Skipped[0].Reason != tsruntime.ReasonStartFailed || !strings.Contains(sel.Skipped[0].Detail, "does not export a valid factory function") {
		t.Fatalf("selection = %+v skipped %+v", sel, sel.Skipped)
	}
}

// An extension whose factory throws on a slot runtime reports that error: the factory ran, and Pi runs it once, so no other runtime runs it again and
// nothing is remembered about it.
func TestTypeScriptRuntimeExtensionThatFailsOnEveryRuntimeReportsItsOwnError(t *testing.T) {
	f := newTierFixture(t, "isolated", "solo")
	fakeTier(t, f.root, "tier1", "tier2", false, nil)
	fakeTier(t, f.root, "tier2", "", false, nil)
	t.Setenv("PIG_TS_PROBE_THROW_ALL", "1")
	h := f.host()
	_, err := h.Reload(f.ctx)
	if report := h.LastReloadReport(); err == nil && (report == nil || len(report.Issues) == 0) {
		t.Fatalf("a failing extension loaded: %+v", report)
	}
	if got := f.starts(); !slices.Equal(got, []string{"tier1"}) {
		t.Fatalf("starts = %v, want one start on the first slot runtime", got)
	}
	if issues := h.LastReloadReport(); issues == nil || len(issues.Issues) != 1 || !strings.Contains(issues.Issues[0], "the extension's own bug") {
		t.Fatalf("report = %+v, want the factory's own error once", issues)
	}
	if listed := tsruntime.ListNodeOnly(f.root); len(listed) != 0 {
		t.Fatalf("a failing extension was recorded as Node-only: %+v", listed)
	}
}

func TestTypeScriptRuntimeReloadAfterASourceChangeLeavesTheStaleCompiledHost(t *testing.T) {
	for _, isolation := range []string{"isolated", ""} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			f := newTierFixture(t, isolation, "solo")
			source := f.configs[0].Source
			info, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			inputs := []tsruntime.Input{{Path: source, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}}
			fakeTier(t, f.root, "tier1", "tier2", false, inputs)
			fakeTier(t, f.root, "tier2", "", false, nil)
			h := f.host()
			f.reload(h)
			first := f.records()
			if len(first) != 1 || first[0].Fake != "tier1" {
				t.Fatalf("first records = %+v", first)
			}
			// Unchanged source: the factory runs again in the same process.
			f.reload(h)
			second := f.records()
			if len(second) != 2 || second[1].Fake != "tier1" || second[1].Pid != first[0].Pid {
				t.Fatalf("after an unchanged reload: %+v", second)
			}
			// Changed source: the image is stale, so the set runs on tier2 in a new process, and the old compiled process is not reused.
			edited := tierProbe + "// edited\n"
			if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(time.Minute)
			if err := os.Chtimes(source, future, future); err != nil {
				t.Fatal(err)
			}
			f.reload(h)
			third := f.records()
			last := third[len(third)-1]
			if len(third) != 3 || last.Fake != "tier2" || last.Pid == first[0].Pid {
				t.Fatalf("after the source changed: %+v", third)
			}
			var label string
			for l := range h.TypeScriptRuntimeSelections() {
				label = l
			}
			sel := f.selection(h, label)
			if sel.Runtime != "tier2" || sel.Fallback == nil || sel.Fallback.Reason != tsruntime.ReasonStale || len(sel.Skipped) != 1 || sel.Skipped[0].Runtime != "tier1" {
				t.Fatalf("selection = %+v (%+v)", sel, sel.Fallback)
			}
		})
	}
}

// The compiled runtime's cache can be damaged or unusable in ways the user never sees as an error: each moves the set to the next runtime, ending on Node, with
// the reason recorded in the selection (gate: corrupt cache, missing runtime, unsupported platform and build failure fall back, Bun then Node, with the reason).
func TestTypeScriptRuntimeUnusableCompiledRuntimeFallsBackWithItsReason(t *testing.T) {
	tamper := func(t *testing.T, root string, edit func(m *tsruntime.Manifest)) {
		t.Helper()
		path := filepath.Join(root, "tier1", tsruntime.ManifestFile)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m tsruntime.Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		edit(&m)
		if data, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		reason tsruntime.Reason
		// onNode is set when the manifest that names the fallback is gone, so the set goes straight to Node.
		onNode bool
		setup  func(t *testing.T, f *tierFixture)
	}{
		{"corrupt image", tsruntime.ReasonVerifyFailed, false, func(t *testing.T, f *tierFixture) {
			if err := os.WriteFile(filepath.Join(f.root, "tier1", "host"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"runtime missing", tsruntime.ReasonNotInstalled, true, func(t *testing.T, f *tierFixture) {
			if err := os.RemoveAll(filepath.Join(f.root, "tier1")); err != nil {
				t.Fatal(err)
			}
		}},
		{"unsupported platform", tsruntime.ReasonUnsupportedPlatform, false, func(t *testing.T, f *tierFixture) {
			tamper(t, f.root, func(m *tsruntime.Manifest) { m.Platform = "plan9-mips" })
		}},
		{"build failed so no image", tsruntime.ReasonNoImage, false, func(t *testing.T, f *tierFixture) {
			tamper(t, f.root, func(m *tsruntime.Manifest) { m.Image = nil })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTierFixture(t, "isolated", "solo")
			fakeTier(t, f.root, "tier1", "tier2", false, nil)
			fakeTier(t, f.root, "tier2", "", false, nil)
			tc.setup(t, f)
			h := f.host()
			f.reload(h)
			wantRuntime, wantStarts := "tier2", []string{"tier2"}
			if tc.onNode {
				wantRuntime, wantStarts = tsruntime.Node, nil
			}
			records := f.records()
			if len(records) != 1 || records[0].Fake != strings.TrimPrefix(wantRuntime, tsruntime.Node) {
				t.Fatalf("records = %+v, want the extension on %s", records, wantRuntime)
			}
			if got := f.starts(); !slices.Equal(got, wantStarts) {
				t.Fatalf("starts = %v, want %v: the broken runtime never starts", got, wantStarts)
			}
			sel := f.selection(h, "solo")
			if sel.Runtime != wantRuntime || len(sel.Skipped) != 1 || sel.Skipped[0].Runtime != "tier1" || sel.Skipped[0].Reason != tc.reason || sel.Fallback == nil || sel.Fallback.Reason != tc.reason {
				t.Fatalf("selection = %+v, skipped %+v: want tier1 skipped for %s", sel, sel.Skipped, tc.reason)
			}
		})
	}
}
