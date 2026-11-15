package extensionconformance

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sessionGraphCollectorEnv marks the child run of TestSessionGraphRowsUnderAggressiveCollector: the Python fixture reads it
// (testdata/session_graph_python.py) and sets the interpreter's collector to run on every container allocation.
const sessionGraphCollectorEnv = "PIG_SESSION_GRAPH_COLLECTOR"

// sessionGraphCollectorTests are the session graph rows the aggressive-collector run repeats.
const sessionGraphCollectorTests = `^(TestSessionGraph[A-Za-z]*AcrossSDKs|TestPackedSessionGraphModelRuntimeAcrossSDKs)$`

// TestSessionGraphRowsUnderAggressiveCollector runs the session graph rows of the SDKs that have a collector once more with
// the collector as aggressive as it goes (Go: GOGC=1 in the test process and in every extension process it starts, and the test
// process collecting in a loop for the fused extension that runs in it; Python:
// gc.set_threshold(1) in the fixture). A session graph object is released on the host when the language's wrapper is
// collected, and handles travel as strings: a wrapper nothing keeps alive while a later call names its handle makes the host
// answer "Unknown or released session graph object", and only a collector that runs early shows it (release 0.5.2 to 0.7.0).
// A red names the SDK. The Rust SDK drops deterministically (Drop at scope end): its guard is the unit test
// extensions/sdk-rs/tests/session_graph_lifetime_wire.rs. Node runs Pi's own classes in the extension process (no wire handle)
// and the Lisp SDK never releases a graph object.
func TestSessionGraphRowsUnderAggressiveCollector(t *testing.T) {
	if os.Getenv(sessionGraphCollectorEnv) != "" {
		t.Skip("this is the child run")
	}
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts SDK subprocesses)")
	}
	for _, sdk := range []struct {
		name  string
		cells []string
	}{
		{"go", []string{"go-fused", "go-strict", "go-packed"}},
		{"python", []string{"python", "python-packed"}},
	} {
		t.Run(sdk.name, func(t *testing.T) {
			start := time.Now()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run", sessionGraphCollectorTests+"/^("+strings.Join(sdk.cells, "|")+")$",
				"-test.count=1", "-test.timeout=20m", "-test.v")
			cmd.Env = append(os.Environ(), "GOGC=1", sessionGraphCollectorEnv+"=1")
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			text := out.String()
			if err != nil {
				t.Fatalf("%s rows under an aggressive collector: %v\n%s", sdk.name, err, tail(text, 60))
			}
			// A run that matched no row passes too: each cell must have run and passed, and none may have skipped.
			for _, cell := range sdk.cells {
				if !cellOutcome(text, "PASS", cell) || cellOutcome(text, "SKIP", cell) {
					t.Fatalf("%s rows under an aggressive collector: cell %s did not run and pass\n%s", sdk.name, cell, tail(text, 60))
				}
			}
			t.Logf("%s: %v", sdk.name, time.Since(start))
		})
	}
}

// collectContinuously runs the collector in a loop until the test ends. The fused Go extension runs in this process, where
// GOGC=1 alone collects too rarely once the heap of a whole run is large (a base run of the rows stayed green 10 of 10).
func collectContinuously(t testing.TB) {
	t.Helper()
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
				runtime.GC()
			}
		}
	}()
	t.Cleanup(func() {
		close(done)
		<-stopped
	})
}

// cellOutcome reports whether the run's output holds a row of the cell with the outcome ("PASS" or "SKIP").
func cellOutcome(text, outcome, cell string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "--- "+outcome+": ") && strings.Contains(line, "/"+cell+" (") {
			return true
		}
	}
	return false
}

// tail is the last n lines of text.
func tail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
