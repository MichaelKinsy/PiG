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

// Pi's createLocalBashOperations().exec and a built-in tool's execute without an extension context (createBashTool(cwd).execute(id, params)) run in the
// extension's own process, so their outcome cannot depend on any other extension. A fused extension shares its process with the host and with every other
// fused extension, so the SDK has the host answer such a call directly; a call carried over the connection of whichever fused extension is running first would
// end with that extension, as it did when a neighbour shut down while a command with a timeout ran ("extension stopped while waiting for ... response").

// probeWhileANeighbourStops runs the builtin_probe case in a fused extension, whose command creates started before it sleeps, and stops a fused extension that
// started before it once the command is running. It returns the probe's report.
func probeWhileANeighbourStops(t *testing.T, probe func(dir, started string) map[string]any) map[string]any {
	t.Helper()
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
	args, err := json.Marshal(probe(dir, started))
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
	return got
}

func TestConformance_FusedLocalExecIsNotCarriedByAnotherExtension(t *testing.T) {
	got := probeWhileANeighbourStops(t, func(dir, started string) map[string]any {
		return map[string]any{
			"kind": "local", "shell": "bash", "factoryCwd": dir, "timeout": 1,
			"command": "touch '" + started + "'; exec sleep 30",
		}
	})
	if got["error"] != "timeout:1" {
		t.Fatalf("a command with a 1 second timeout, while another fused extension stopped, reported %v; want {\"error\":\"timeout:1\"}, as Pi's exec rejects", got)
	}
}

func TestConformance_FusedBuiltinToolWithoutContextIsNotCarriedByAnotherExtension(t *testing.T) {
	got := probeWhileANeighbourStops(t, func(dir, started string) map[string]any {
		return map[string]any{
			"kind": "delegate", "tool": "bash", "factoryCwd": dir, "noContext": true,
			"params": map[string]any{"command": "touch '" + started + "'; exec sleep 30", "timeout": 1},
		}
	})
	if msg, _ := got["error"].(string); !strings.Contains(msg, "Command timed out after 1 seconds") {
		t.Fatalf("a bash tool without a context and a 1 second timeout, while another fused extension stopped, reported %v; want Pi's \"Command timed out after 1 seconds\"", got)
	}
}
