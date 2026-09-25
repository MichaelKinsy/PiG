package subprocess

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestNodeCellMemoryProbe is not an acceptance test. It measures the total
// RSS of the Node process(es) hosting 5 trivial TS extensions, so the N8
// report (blog-pack/EXTENSION-ARCH.md item N8, C1) can quote a real
// before/after number instead of an estimate.
//
// Gated behind PIG_NODE_CELL_MEMORY_PROBE=1 because it is a measurement, not
// a pass/fail gate:
//
//	PIG_NODE_CELL_MEMORY_PROBE=1 go test ./coding/extension/host/subprocess/... -run TestNodeCellMemoryProbe -v
func TestNodeCellMemoryProbe(t *testing.T) {
	if os.Getenv("PIG_NODE_CELL_MEMORY_PROBE") != "1" {
		t.Skip("set PIG_NODE_CELL_MEMORY_PROBE=1 to run the Node-cell memory probe")
	}
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 5 {
		name := fmt.Sprintf("nc-mem-%d", i)
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != 5 {
		t.Fatalf("LoadAll = %d loaded, %v errs, want 5 loaded and no errs", len(loaded), errs)
	}

	pids := nodeCellProcessesForMarker(t, root)
	if len(pids) == 0 {
		t.Fatal("no node process found hosting the extensions")
	}
	totalKB := 0
	for _, pid := range pids {
		out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			t.Fatalf("ps rss for pid %d: %v", pid, err)
		}
		rss, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			t.Fatalf("parse rss %q: %v", string(out), err)
		}
		totalKB += rss
	}
	t.Logf("Node-cell memory probe: %d node process(es), total RSS = %d KB (%.1f MB) for 5 trivial TS extensions", len(pids), totalKB, float64(totalKB)/1024)
}
