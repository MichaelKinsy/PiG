// SPDX-License-Identifier: MIT

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/profiling"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// runRPCTurns starts the built binary in RPC mode against the faux provider, sends turns prompts one at a time (each waits for agent_settled), closes stdin and returns the process's runtime/metrics at exit.
func runRPCTurns(ctx context.Context, t *testing.T, binary, home, agentDir string, turns int) profiling.ProcessMetrics {
	t.Helper()
	profileDir := t.TempDir()
	command := exec.CommandContext(ctx, binary, "--mode", "rpc", "--model", "test-faux/faux-1", "--no-extensions")
	command.Dir = t.TempDir()
	command.Env = append(measuredEnvironment(os.Environ()), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_PROFILE=metrics", "PIG_PROFILE_DIR="+profileDir, "GOMAXPROCS=1",
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReaderSize(stdout, 1<<20)
	for turn := range turns {
		if _, err := fmt.Fprintf(stdin, "{\"type\":\"prompt\",\"message\":\"reply with exactly: ok (%d)\"}\n", turn); err != nil {
			t.Fatal(err)
		}
		for settled := false; !settled; {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("turn %d: %v", turn, err)
			}
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Type == "agent_settled" {
				settled = true
			}
		}
	}
	_ = stdin.Close()
	_, _ = io.Copy(io.Discard, reader)
	if err := command.Wait(); err != nil {
		t.Fatalf("pig rpc: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(profileDir, "pig-*-metrics.json"))
	if len(files) != 1 {
		t.Fatalf("want one metrics file, got %v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var stats profiling.ProcessMetrics
	if err := json.Unmarshal(data, &stats); err != nil || stats.AllocBytes == 0 {
		t.Fatalf("stats %q: %v", data, err)
	}
	return stats
}

// Per-turn allocation budgets, from the difference between a 12-turn and a 2-turn RPC session of the built binary against the faux provider, divided by the 10 extra turns: what one more turn of a session costs once the process is warm, with the history of the turns before it. Each session is the smallest of three, built with -pgo=off (see compilePigBinary). Measured 313-316 KB and 3,337-3,379 objects over six runs; the same gate on the tree before the stringify change measured 365-369 KB and 3,473-3,504. They only ratchet down: a change that lowers the measurement lowers its budget in the same commit.
const (
	extraTurnAllocBytesBudget   = 322_000
	extraTurnAllocObjectsBudget = 3_450
)

func TestExtraTurnAllocationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	binary := buildPigBinaryForSignalTest(t)
	ctx := testbudget.Context(t)
	home, agentDir := t.TempDir(), t.TempDir()
	runRPCTurns(ctx, t, binary, home, agentDir, 1) // stages the embedded docs and SDKs
	const shortSession, longSession = 2, 12
	smallest := func(turns int) profiling.ProcessMetrics {
		best := runRPCTurns(ctx, t, binary, home, agentDir, turns)
		for range 2 {
			next := runRPCTurns(ctx, t, binary, home, agentDir, turns)
			best.AllocBytes = min(best.AllocBytes, next.AllocBytes)
			best.AllocObjects = min(best.AllocObjects, next.AllocObjects)
		}
		return best
	}
	short, long := smallest(shortSession), smallest(longSession)
	bytesPerTurn := (long.AllocBytes - short.AllocBytes) / (longSession - shortSession)
	objectsPerTurn := (long.AllocObjects - short.AllocObjects) / (longSession - shortSession)
	t.Logf("%d-turn session %d bytes, %d objects; %d-turn session %d bytes, %d objects; one more turn %d bytes, %d objects", shortSession, short.AllocBytes, short.AllocObjects, longSession, long.AllocBytes, long.AllocObjects, bytesPerTurn, objectsPerTurn)
	if bytesPerTurn > extraTurnAllocBytesBudget || objectsPerTurn > extraTurnAllocObjectsBudget {
		t.Errorf("one more turn allocated %d bytes in %d objects, budget %d bytes in %d objects", bytesPerTurn, objectsPerTurn, extraTurnAllocBytesBudget, extraTurnAllocObjectsBudget)
	}
}
