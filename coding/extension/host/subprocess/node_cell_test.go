package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// N8 (docs/additive-features.md D20; blog-pack EXTENSION-ARCH.md item N8):
// TS/JS extensions must share one Node process (a "Node cell") like upstream
// Pi's loader.ts/runner.ts, instead of one Node process per extension. These
// tests gate that work: TestNodeCellHostsFiveExtensionsInOneProcess,
// TestNodeCellRegistrationOrderMatchesConfigOrder, and
// TestNodeCellReloadKeepsOneProcessForAllExtensions must fail on main before
// the Node-cell fix lands.

// nodeCellTrivialExtension writes a minimal TS/JS extension at path that
// registers one tool (named toolName) answering with toolName, so the test
// can prove both "loaded" and "answers a call" per extension.
func nodeCellTrivialExtension(t testing.TB, path, toolName string) {
	t.Helper()
	src := fmt.Sprintf(`export default function (pi) {
  pi.registerTool({
    name: %[1]q,
    label: %[1]q,
    description: "trivial node-cell probe tool",
    parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: %[1]q}]}; },
  });
}
`, toolName)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nodeCellOrderedExtension is like nodeCellTrivialExtension, but it also
// synchronously appends its own name to orderLog at module-evaluation time,
// before registerTool runs. That captures the true in-process registration
// order (as Pi's loader.ts iterates configs), independent of any later,
// possibly re-sorted, reporting order.
func nodeCellOrderedExtension(t testing.TB, path, name, orderLog string) {
	t.Helper()
	src := fmt.Sprintf(`import fs from "node:fs";
fs.appendFileSync(%[2]q, %[1]q + "\n");
export default function (pi) {
  pi.registerTool({
    name: %[1]q,
    label: %[1]q,
    description: "order probe tool",
    parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: %[1]q}]}; },
  });
}
`, name, orderLog)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nodeCellThrowingExtension writes a TS/JS extension that throws synchronously
// while loading, matching Pi's loader.ts continue-on-error contract: one
// extension's load failure must not block the others.
func nodeCellThrowingExtension(t testing.TB, path string) {
	t.Helper()
	src := "export default function () { throw new Error(\"node-cell-boom\"); }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nodeCellProcessesForMarker lists real Node processes using a test-unique path marker, independently of the host's process registry.
func nodeCellProcessesForMarker(t testing.TB, marker string) []int {
	t.Helper()
	out, err := nodeCellProcessSnapshot(runtime.GOOS, func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	})
	if err != nil {
		t.Fatalf("list Node processes: %v", err)
	}
	var pids []int
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, marker) {
			continue
		}
		if !strings.Contains(line, "node") {
			continue
		}
		fields := strings.SplitN(line, " ", 2)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func nodeCellRequireNode(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the Node-cell fixture: %v", err)
	}
}

// TestNodeCellHostsFiveExtensionsInOneProcess is acceptance test 1: with 5
// trivial TS extensions configured, exactly 1 Node process hosts them all,
// and each extension still registers and answers a call.
//
// On main (pre-fix), each TS extension gets its own subprocess.NewHost Node
// process, so this fails with "node processes = 5, want 1": the exact,
// documented C1 finding in blog-pack/EXTENSION-ARCH.md.
func TestNodeCellHostsFiveExtensionsInOneProcess(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 5 {
		name := fmt.Sprintf("nc-ext-%d", i)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 {
		t.Fatalf("LoadAll errs = %v, want none", errs)
	}
	if len(loaded) != 5 {
		t.Fatalf("loaded = %d extensions, want 5", len(loaded))
	}

	for _, ext := range h.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("extension %s did not register its tool", ext.Name)
		}
		result, err := tool.Definition.Execute(ctx, "nc-call-"+ext.Name, json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatalf("execute %s: %v", ext.Name, err)
		}
		if result == nil {
			t.Fatalf("execute %s: nil result", ext.Name)
		}
	}

	pids := nodeCellProcessesForMarker(t, root)
	if len(pids) != 1 {
		t.Fatalf("node processes for 5 trivial extensions = %d (%v), want 1 Node cell", len(pids), pids)
	}
}

// TestNodeCellContinueOnErrorLoadsHealthyExtensions is acceptance test 2:
// one extension that throws at load is reported as an extension issue, and
// the other 4 load and work, matching Pi's loader.ts continue-on-error.
func TestNodeCellContinueOnErrorLoadsHealthyExtensions(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 4 {
		name := fmt.Sprintf("nc-good-%d", i)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	brokenEntry := filepath.Join(root, "nc-broken.mjs")
	nodeCellThrowingExtension(t, brokenEntry)
	configs = append(configs, ExtConfig{Name: "nc-broken", Source: brokenEntry, Enabled: true})

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(loaded) != 4 {
		t.Fatalf("loaded = %d extensions, want the 4 healthy ones", len(loaded))
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly the throwing extension reported", errs)
	}
	loadErr, ok := errors.AsType[*ExtensionLoadError](errs[0])
	if !ok || loadErr.Name != "nc-broken" {
		t.Fatalf("err = %#v, want nc-broken reported as an extension issue", errs[0])
	}

	for _, ext := range h.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("healthy extension %s did not register its tool", ext.Name)
		}
		if _, err := tool.Definition.Execute(ctx, "nc-call-"+ext.Name, json.RawMessage(`{}`), nil); err != nil {
			t.Fatalf("execute %s: %v", ext.Name, err)
		}
	}
}

// TestNodeCellRegistrationOrderMatchesConfigOrder is acceptance test 3:
// registration order equals config order for the Node cell's extensions,
// matching Pi's loader.ts, which iterates configs in order.
//
// Names are deliberately not alphabetical so that any component which
// re-sorts by name or by a generated cell key (rather than preserving config
// order) is caught. On main this fails: PlanCells keys isolated cells as
// "isolated:<name>:<hash>" and sorts cells by that key
// (cell_plan.go PlanCells), so registration order becomes alphabetical by
// name instead of config order (the documented C3/N1 finding).
func TestNodeCellRegistrationOrderMatchesConfigOrder(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	orderLog := filepath.Join(root, "order.log")
	configOrder := []string{"echo", "alpha", "delta", "bravo", "charlie"}
	var configs []ExtConfig
	for _, name := range configOrder {
		entry := filepath.Join(root, name+".mjs")
		nodeCellOrderedExtension(t, entry, name, orderLog)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 {
		t.Fatalf("LoadAll errs = %v, want none", errs)
	}
	if len(loaded) != 5 {
		t.Fatalf("loaded = %d extensions, want 5", len(loaded))
	}

	data, err := os.ReadFile(orderLog)
	if err != nil {
		t.Fatal(err)
	}
	var gotOrder []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			gotOrder = append(gotOrder, line)
		}
	}
	if strings.Join(gotOrder, ",") != strings.Join(configOrder, ",") {
		t.Fatalf("node-cell registration order = %v, want config order %v", gotOrder, configOrder)
	}
}

// TestNodeCellReloadKeepsOneProcessForAllExtensions is acceptance test 4:
// after /reload (Host.Reload), all 5 extensions come back and work, still in
// one Node process.
//
// On main this fails the same way as TestNodeCellHostsFiveExtensionsInOneProcess:
// there is no Node cell, so 5 Node processes exist both before and after reload.
func TestNodeCellReloadKeepsOneProcessForAllExtensions(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 5 {
		name := fmt.Sprintf("nc-reload-%d", i)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}

	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	if pids := nodeCellProcessesForMarker(t, root); len(pids) != 1 {
		t.Fatalf("node processes after initial load = %d (%v), want 1", len(pids), pids)
	}

	loaded, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded) != 5 {
		t.Fatalf("reloaded = %d extensions, want 5", len(loaded))
	}
	for _, ext := range h.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("reloaded extension %s did not register its tool", ext.Name)
		}
		if _, err := tool.Definition.Execute(ctx, "nc-reload-call-"+ext.Name, json.RawMessage(`{}`), nil); err != nil {
			t.Fatalf("execute %s after reload: %v", ext.Name, err)
		}
	}

	pids := nodeCellProcessesForMarker(t, root)
	if len(pids) != 1 {
		t.Fatalf("node processes after reload = %d (%v), want 1 Node cell", len(pids), pids)
	}
}

// TestNodeCellProcessDeathStopsExtensionsAndReloadRecovers is acceptance test
// 5: if the Node cell process is killed, pig keeps running, those extensions
// stop, and a reload brings them back. This mirrors
// TestPackedProcessDeathQuarantinesAffectedCell (liveness_test.go) and
// TestPackedCellFailureIsolatesFailingMember (reload_unresolved_test.go) for
// the packed Go/Python case.
func TestNodeCellProcessDeathStopsExtensionsAndReloadRecovers(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	names := make([]string, 0, 5)
	for i := range 5 {
		name := fmt.Sprintf("nc-crash-%d", i)
		names = append(names, name)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		// A single-crash circuit breaker disables the extension immediately
		// instead of racing this test's explicit Reload against the
		// supervisor's own backoff-and-restart, matching how the packed Go
		// crash tests avoid that race (TestPackedProcessDeathQuarantinesAffectedCell).
		configs = append(configs, ExtConfig{
			Name: name, Source: entry, Enabled: true,
			SupervisorConfig: SupervisorConfig{MaxCrashes: 1, CrashWindow: time.Minute},
		})
	}

	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	crashed := make(chan string, len(names)*4)
	h.SetCrashHandler(func(name string, _ time.Duration, _ bool, _ string) {
		crashed <- name
	})
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	pids := nodeCellProcessesForMarker(t, root)
	if len(pids) == 0 {
		t.Fatal("no node process found hosting the extensions")
	}

	// Kill every Node process backing this session's extensions: on the
	// fixed Node-cell host that is one shared process; on main (isolated
	// per-extension processes) it is all of them. Either way, pig must keep
	// running, and the crash handler must report each extension.
	for _, pid := range pids {
		if err := killTestProcess(pid); err != nil {
			t.Fatalf("kill %d: %v", pid, err)
		}
	}

	seen := map[string]bool{}
	deadline := time.After(20 * time.Second)
loop:
	for len(seen) < len(names) {
		select {
		case name := <-crashed:
			seen[name] = true
		case <-deadline:
			break loop
		}
	}
	if len(seen) == 0 {
		t.Fatalf("crash handler saw no extension crash after killing the Node process(es) %v", pids)
	}

	// pig (the host) is still alive and can reload; the dead extensions come
	// back and work again.
	loaded, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload after Node-cell death: %v", err)
	}
	if len(loaded) != 5 {
		t.Fatalf("reloaded = %d extensions after crash, want 5", len(loaded))
	}
	for _, ext := range h.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("recovered extension %s did not register its tool", ext.Name)
		}
		if _, err := tool.Definition.Execute(ctx, "nc-recovered-"+ext.Name, json.RawMessage(`{}`), nil); err != nil {
			t.Fatalf("execute %s after recovery: %v", ext.Name, err)
		}
	}
}

// TestNodeCellIsolatedEscapeHatchGetsOwnProcess is acceptance test 6: an
// extension configured as isolated (Isolation: "isolated", mirroring the
// existing --isolated / isolation-strict convention in
// cmd/pig/extension_validate_command.go and cell_plan.go's
// isShareableIsolation) still gets its own process, separate from the shared
// Node cell hosting the other extensions.
func TestNodeCellIsolatedEscapeHatchGetsOwnProcess(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 3 {
		name := fmt.Sprintf("nc-shared-%d", i)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	isolatedEntry := filepath.Join(root, "nc-isolated.mjs")
	nodeCellTrivialExtension(t, isolatedEntry, "nc-isolated")
	configs = append(configs, ExtConfig{Name: "nc-isolated", Source: isolatedEntry, Enabled: true, Isolation: "isolated"})

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 {
		t.Fatalf("LoadAll errs = %v, want none", errs)
	}
	if len(loaded) != 4 {
		t.Fatalf("loaded = %d extensions, want 4", len(loaded))
	}

	sharedRoot := root // marker for all extensions in root
	total := nodeCellProcessesForMarker(t, sharedRoot)
	isolatedOnly := nodeCellProcessesForMarker(t, isolatedEntry)
	if len(isolatedOnly) != 1 {
		t.Fatalf("isolated extension node processes = %d (%v), want its own 1 process", len(isolatedOnly), isolatedOnly)
	}
	// The isolated extension's process must be distinct from every process
	// hosting the 3 shared-ok extensions.
	otherEntry := filepath.Join(root, "nc-shared-0.mjs")
	sharedOnly := nodeCellProcessesForMarker(t, otherEntry)
	for _, p := range isolatedOnly {
		for _, s := range sharedOnly {
			if p == s {
				t.Fatalf("isolated extension shares a process (%d) with a shared-ok extension", p)
			}
		}
	}
	if len(total) < 2 {
		t.Fatalf("total node processes = %d (%v), want at least 2 (shared cell + isolated)", len(total), total)
	}
}
