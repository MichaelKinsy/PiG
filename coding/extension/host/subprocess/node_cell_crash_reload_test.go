package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// killTestProcess sends a fatal signal to the process identified by pid,
// portably: os.FindProcess/Process.Kill (SIGKILL on Unix, TerminateProcess
// on Windows) instead of the Unix-only syscall.Kill, so this package's test
// binary compiles under GOOS=windows go vet/go test -c even though the
// crash fixtures these tests spawn only run on Unix in practice
// (nodeCellRequireNode gates the actual test bodies on a real "node"
// binary). A process that already exited is not an error: os.ErrProcessDone
// is the portable sentinel for exactly that race, replacing the Unix-only
// syscall.ESRCH check.
func killTestProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// TestNodeCellCrashThenReloadRestoresOneProcess is CNC-002's regression: after
// the Node cell process is killed and an explicit /reload recovers the
// extensions (TestNodeCellProcessDeathStopsExtensionsAndReloadRecovers, one
// of the protected acceptance tests, already proves that recovery happens and
// the tools work again), the recovered extensions must be back in one shared
// Node process, not fissioned into one process per extension forever.
//
// Before the CNC-002 fix, quarantine was permanent for the life of the Host:
// nothing ever removed a cell key from Host.quarantinedCells, so PlanCells
// fissioned the same key on every later Reload too, and the crashed Node
// cell's five extensions stayed as five separate isolated Node processes
// until the whole pig process restarted. Host.Reload now calls
// releaseRetryableQuarantines first, which clears a quarantined key's
// fission unless that key's Supervisor (the same circuit breaker every
// isolated extension already has) has tripped, so a shared Node cell that
// crashes once (this test) is given back its one-process default on the very
// next reload, and only a cell that keeps crashing stays split.
func TestNodeCellCrashThenReloadRestoresOneProcess(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	names := make([]string, 0, 5)
	for i := range 5 {
		name := fmt.Sprintf("nc-crash-reload-%d", i)
		names = append(names, name)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
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
	if len(pids) != 1 {
		t.Fatalf("node processes before crash = %d (%v), want 1 Node cell", len(pids), pids)
	}

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

	t.Logf("quarantine before reload: %v", h.QuarantinedCells())
	loaded, err := h.Reload(ctx)
	t.Logf("quarantine after reload: %v", h.QuarantinedCells())
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

	recoveredPids := nodeCellProcessesForMarker(t, root)
	if len(recoveredPids) != 1 {
		t.Fatalf("node processes after crash+reload = %d (%v), want 1 Node cell restored (CNC-002)", len(recoveredPids), recoveredPids)
	}
}

// TestNodeCellRepeatedCrashesStayIsolated proves the crash-loop-protection
// half of CNC-002's rule: a packed cell key that keeps crashing does not
// repack forever. It reuses the exact circuit breaker every isolated
// extension already has (Supervisor, DefaultSupervisorConfig - no bespoke
// threshold for packed cells): it quarantines the same cell key
// DefaultSupervisorConfig().MaxCrashes times directly (bypassing the timing
// of a real repeated kill/reload cycle, which would be slow and flaky under
// load) and asserts that a further Reload no longer clears that key, so
// PlanCells keeps fissioning it.
func TestNodeCellRepeatedCrashesStayIsolated(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 2 {
		name := fmt.Sprintf("nc-strike-%d", i)
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
	cellKey := ""
	for key := range h.QuarantinedCells() {
		t.Fatalf("unexpected pre-existing quarantine: %v", key)
	}
	for _, cell := range PlanCells(configs, nil) {
		if cell.Strategy == CellStrategyPackedNode {
			cellKey = cell.Key
		}
	}
	if cellKey == "" {
		t.Fatal("expected a packed-node cell for the two configured extensions")
	}

	maxCrashes := DefaultSupervisorConfig().MaxCrashes
	for i := range maxCrashes {
		h.quarantinePackedCell(cellKey, "synthetic crash for CNC-002 strike test")
		h.releaseRetryableQuarantines()
		if i < maxCrashes-1 {
			if _, quarantined := h.QuarantinedCells()[cellKey]; quarantined {
				t.Fatalf("crash %d: cell still quarantined after releaseRetryableQuarantines, want it cleared (under the circuit breaker's MaxCrashes budget)", i)
			}
		}
	}
	if _, quarantined := h.QuarantinedCells()[cellKey]; !quarantined {
		t.Fatalf("after %d crashes (Supervisor's MaxCrashes), cell should stay quarantined (crash-loop protection)", maxCrashes)
	}
}

// TestFailedReloadKeepsCurrentCellCrashOwnership is CNC-003's regression.
// armPackedProcessGenerations (CNC-002) bumps every known packed cell key's
// generation so a crash report for a process this Reload is about to
// replace can never race the replacement into existence (see its doc
// comment). That reasoning depends on this Reload actually going on to
// replace or explicitly tear down every currently-known packed process.
// When the config loader fails, Reload returns immediately and retains
// every currently running extension and process completely unchanged - so
// arming generations before that failure is checked would invalidate crash
// ownership for a process this aborted reload never touched: a real, later
// crash of that still-current, still-running process would then be
// silently discarded as stale (quarantinePackedCellGeneration's staleness
// check) instead of quarantined, and pig would never notice the cell died.
//
// This test does not need a real Node process: it seeds the bookkeeping a
// live packed process would have (a claimed generation and a
// packedProcesses entry for its cell key) directly, fails Reload's config
// load, and then submits the same crash report watchPackedProcess would
// have sent for that still-current process. Before the fix (arming
// generations unconditionally at the top of Reload), the failed Reload
// call already invalidated the seeded generation, so the crash report was
// discarded and QuarantinedCells stayed empty.
func TestFailedReloadKeepsCurrentCellCrashOwnership(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	const key = "live-cell"
	h.packedCellGeneration = map[string]int{key: 1}
	h.packedProcesses = map[string]*packedProcessState{key: {key: key, generation: 1}}
	h.SetConfigLoader(func() ([]ExtConfig, error) {
		return nil, errors.New("bad config")
	})

	if _, err := h.Reload(t.Context()); err == nil {
		t.Fatal("expected the config loader's error to fail Reload")
	}

	h.quarantinePackedCellGeneration(key, 1, "real later crash of the still-current, unreplaced process")
	if reason := h.QuarantinedCells()[key]; reason == "" {
		t.Fatal("a failed reload invalidated the still-current cell's crash ownership: the later crash was discarded as stale instead of quarantined")
	}
}
