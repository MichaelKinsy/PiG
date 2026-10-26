//go:build integration && linux

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// Interactive startup to the first ready frame, then Ctrl+D on the empty editor: the whole process's allocations and its exit state, from PIG_PROFILE=metrics under GOMAXPROCS=1, and its memory at the ready frame (rpi's "RSS at ready"). The second start in the same home is gated, so first-run cache materialization is excluded. The memory gate is anonymous memory, ceiling 7.5 MB (process.interactive_first_frame.anon_bytes_at_ready), not RSS: RSS counts the binary's file pages that happen to be resident, which is why RSS, PSS and peak RSS are reported and not gated (see TestRPCReadyMemoryGate). 30 runs on one host measured an anonymous 6.73 to 6.82 MB, against an RSS of 47.6 to 54.2 MB; the ceiling is the largest anonymous figure plus 10%. The test process turns transparent huge pages off before it starts the tmux server (see TestMain and TestRPCReadyMemoryGate): with the runners' THP policy "always" the figure would measure 2 MiB page alignment. On such a runner, 4 runs measured 6.83 to 6.85 MB.
func TestInteractiveFirstFrameProcessGate(t *testing.T) {
	pigHome := t.TempDir()
	var record profiling.ProcessMetrics
	var memory perfgate.Memory
	for run := range 2 {
		h := newHarness(t)
		profileDir := t.TempDir()
		h.startArgs([]string{"--no-extensions", "--no-session", "--model", "test-faux/faux-1"},
			"PIG_HOME="+pigHome, "PIG_TEST_FAUX=1", "GOMAXPROCS=1", "PIG_PROFILE=metrics", "PIG_PROFILE_DIR="+profileDir)
		h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")
		out, err := tmuxCommand("display-message", "-p", "-t", h.session, "#{pane_pid}").Output()
		if err != nil {
			t.Fatalf("tmux pane_pid: %v", err)
		}
		panePID, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			t.Fatalf("tmux pane_pid %q: %v", out, err)
		}
		pid := perfgate.Descendant(panePID, filepath.Base(h.bin))
		if pid == 0 {
			t.Fatalf("no %s process under pane %d", filepath.Base(h.bin), panePID)
		}
		memory = perfgate.ProcessMemory(t, pid)
		h.sendKey("C-d")
		var path string
		if !pollUntil(startupReadyWait, func() bool {
			matches, _ := filepath.Glob(filepath.Join(profileDir, "pig-*-metrics.json"))
			if len(matches) == 1 {
				if info, err := os.Stat(matches[0]); err == nil && info.Size() > 0 {
					path = matches[0]
					return true
				}
			}
			return false
		}) {
			t.Fatalf("run %d: pig wrote no metrics record after Ctrl+D\n%s", run, h.capture())
		}
		// The record is written before exit; give the encoder its final newline before reading.
		var data []byte
		pollUntil(5*time.Second, func() bool {
			data, _ = os.ReadFile(path)
			return json.Valid(data)
		})
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, data)
		}
	}
	t.Logf("perfgate: process.interactive_first_frame record %+v; at ready %+v", record, memory)
	perfgate.Report(t, "process.interactive_first_frame.rss_bytes_at_ready", memory.Rss)
	perfgate.Report(t, "process.interactive_first_frame.pss_bytes_at_ready", memory.Pss)
	perfgate.Report(t, "process.interactive_first_frame.max_rss_bytes", float64(record.MaxRSSBytes))
	perfgate.CheckAnonymous(t, "process.interactive_first_frame.anon_bytes_at_ready", memory)
	perfgate.Check(t, "process.interactive_first_frame.allocs", float64(record.AllocObjects))
	perfgate.Check(t, "process.interactive_first_frame.bytes", float64(record.AllocBytes))
	perfgate.Check(t, "process.interactive_first_frame.heap_live_bytes", float64(record.HeapLiveBytes))
	if record.ChildProcesses >= 0 {
		perfgate.Check(t, "process.interactive_first_frame.children_at_exit", float64(record.ChildProcesses))
	}
}
