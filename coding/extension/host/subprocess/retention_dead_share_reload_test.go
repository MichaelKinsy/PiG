//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"
)

// TestReloadOfAKilledNodeCellRefusesItsUnreapedProcess drives Reload through the window TestNodeCellCrashThenReloadRestoresOneProcess hit under load: the Node cell was killed and the host has handled every member's closed connection, but the process's exit and closed stdin are not yet observed, so the retained share still looks alive to the exit channel and to the noop probe. A closed connection must already keep the share out of retention claims, so a reload that begins then starts the cell in a new process and every member registers. The end of the process is its watcher's to report, and the reload replaced the cell before the watcher's recovery ran, so nothing is reported. The test holds the exit unobserved by replacing the share's exit channel and control stdin after the kill, and keeps the automatic recovery out by invalidating the dead process's generation, as Reload itself does.
func TestReloadOfAKilledNodeCellRefusesItsUnreapedProcess(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	names := map[string]bool{}
	for i := range 3 {
		name := fmt.Sprintf("nc-unreaped-%d", i)
		names[name] = true
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	crashed := make(chan string, len(configs)*4)
	h.SetCrashHandler(func(name string, _ time.Duration, _ bool, _ string) { crashed <- name })
	// closed receives each member whose connection loop has handled its closed connection: it has withdrawn the process from retention claims.
	closed := make(chan string, len(configs)*4)
	h.deathGate = func(seenBy deathObserver, name string) func() {
		if seenBy != deathSeenByConnection {
			return func() {}
		}
		return func() { closed <- name }
	}
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	pids := nodeCellProcessesForMarker(t, root)
	if len(pids) != 1 {
		t.Fatalf("node processes after initial load = %d (%v), want 1", len(pids), pids)
	}
	retention := h.retention()
	retention.mu.Lock()
	var share *processShare
	for _, candidate := range retention.procs {
		if candidate.node && candidate.alive() {
			share = candidate
		}
	}
	retention.mu.Unlock()
	if share == nil {
		t.Fatal("initial load registered no retained Node process")
	}

	// Automatic recovery would release the share; it waits for transitionMu and then finds its generation stale.
	h.transitionMu.Lock()
	if err := killTestProcess(pids[0]); err != nil {
		h.transitionMu.Unlock()
		t.Fatalf("kill %d: %v", pids[0], err)
	}
	seen := map[string]bool{}
	deadline := time.After(20 * time.Second)
	for len(seen) < len(names) {
		select {
		case name := <-closed:
			seen[name] = true
		case <-deadline:
			h.transitionMu.Unlock()
			t.Fatalf("members whose closed connection the host handled after the Node cell was killed = %v, want %d", seen, len(names))
		}
	}
	h.armPackedProcessGenerations()
	retention.mu.Lock()
	share.exitCh = make(chan struct{})
	retention.mu.Unlock()
	share.admitMu.Lock()
	share.admit = discardAdmitWriter{io.Discard}
	share.admitMu.Unlock()
	h.transitionMu.Unlock()

	loaded, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded) != len(configs) {
		t.Fatalf("reloaded = %d extensions after the crash notices, want %d; issues: %v", len(loaded), len(configs), h.LastReloadReport().Issues)
	}
	for _, ext := range h.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("extension %s did not register its tool", ext.Name)
		}
		if _, err := tool.Definition.Execute(ctx, "nc-unreaped-"+ext.Name, json.RawMessage(`{}`), nil); err != nil {
			t.Fatalf("execute %s after reload: %v", ext.Name, err)
		}
	}
	if after := nodeCellProcessesForMarker(t, root); len(after) != 1 || after[0] == pids[0] {
		t.Fatalf("node processes after reload = %v (killed %v), want one fresh Node cell", after, pids)
	}
	select {
	case name := <-crashed:
		t.Fatalf("the end of the process the reload replaced was reported for %s", name)
	default:
	}
}
