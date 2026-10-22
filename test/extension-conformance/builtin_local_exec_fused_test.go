package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestConformance_FusedLocalExecIsNotCarriedByAnotherExtension: Pi's createLocalBashOperations().exec runs the command in the extension's own process, so a
// command's outcome cannot depend on any other extension. A fused extension shares its process with the host and with every other fused extension, so the
// SDK answers the call from the host directly; a call carried over the connection of whichever fused extension is running first would end with that
// extension, as it did when a neighbour shut down while a command with a timeout ran ("extension stopped while waiting for builtinTool.localExec
// response" instead of Pi's "timeout:1").
func TestConformance_FusedLocalExecIsNotCarriedByAnotherExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the command is a POSIX shell command (touch, exec), which on Windows needs a Git bash or WSL the Windows runners do not guarantee")
	}
	// The neighbour starts first, so it is the one a call that rides the first running extension's connection uses.
	neighbour := makeFusedGoHarness(t)
	h := makeFusedGoHarness(t)
	neighbourStopped := false
	stopNeighbour := func() {
		if !neighbourStopped {
			neighbourStopped = true
			neighbour.host.Shutdown("neighbour done")
		}
	}
	t.Cleanup(func() {
		for _, harness := range []*harness{h, neighbour} {
			if harness.cleanup != nil {
				harness.cleanup()
			}
		}
		stopNeighbour()
		h.host.Shutdown("test done")
	})

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	args, err := json.Marshal(map[string]any{
		"kind": "local", "shell": "bash", "factoryCwd": dir, "timeout": 1,
		"command": "touch '" + started + "'; exec sleep 30",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(h.ui.Recorded())
	go h.runner.ExecuteCommand(context.Background(), "builtin_probe", string(args))
	// The command is running when its file exists; the neighbour ends while the command still has its second to run.
	waitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
	stopNeighbour()

	var reported string
	pollUntilConformance(t, "builtin_probe: no report for "+string(args), func() bool {
		for _, n := range h.ui.Recorded()[before:] {
			if rest, ok := strings.CutPrefix(n, "builtin:"); ok {
				reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
				return true
			}
		}
		return false
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(reported), &got); err != nil {
		t.Fatalf("%s is not JSON: %v", reported, err)
	}
	if got["error"] != "timeout:1" {
		t.Fatalf("a command with a 1 second timeout, while another fused extension stopped, reported %s; want {\"error\":\"timeout:1\"}, as Pi's exec rejects", reported)
	}
}
