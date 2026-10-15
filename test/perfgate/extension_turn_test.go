// SPDX-License-Identifier: MIT

// The ceilings are measured on linux; other platforms allocate differently on their file, terminal and process paths.

//go:build linux

package perfgate

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// benchExtensionCount is the extension count of pi-arena's rpc-ext10 workload: each extension registers a tool, a command and handlers for session_start, before_agent_start, tool_call and turn_end.
const benchExtensionCount = 10

// writeBenchExtensions writes benchExtensionCount trivial TypeScript extensions and returns their paths.
func writeBenchExtensions(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, benchExtensionCount)
	for i := range paths {
		n := fmt.Sprintf("%02d", i+1)
		source := `export default function (pi: any) {
  let events = 0;
  pi.registerTool({
    name: "bench_tool_` + n + `",
    label: "Bench tool ` + n + `",
    description: "Return a fixed string.",
    parameters: { type: "object", properties: { value: { type: "string" } }, additionalProperties: false },
    async execute() {
      return { content: [{ type: "text", text: "bench ` + n + ` ok" }], details: { events } };
    },
  });
  pi.registerCommand("bench-` + n + `", { description: "Bench command ` + n + `", handler: async () => {} });
  for (const name of ["session_start", "before_agent_start", "tool_call", "turn_end"]) {
    pi.on(name, async () => {
      events += 1;
    });
  }
}
`
		paths[i] = filepath.Join(dir, "bench-ext-"+n+".ts")
		if err := os.WriteFile(paths[i], []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

// awaitBenchCommands asks for the command list until all count bench extensions' commands are listed: RPC mode answers before its extensions finish loading.
func awaitBenchCommands(t *testing.T, count int, stdin io.Writer, stdout *bufio.Reader) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		if _, err := fmt.Fprintf(stdin, `{"id":"c%d","type":"get_commands"}`+"\n", attempt); err != nil {
			t.Fatal(err)
		}
		for {
			line, err := stdout.ReadBytes('\n')
			if err != nil {
				t.Fatalf("RPC output ended before the get_commands response: %v", err)
			}
			var response struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Data    struct {
					Commands []struct {
						Name string `json:"name"`
					} `json:"commands"`
				} `json:"data"`
			}
			if json.Unmarshal(line, &response) != nil || response.Type != "response" || response.Command != "get_commands" {
				continue
			}
			loaded := 0
			for _, command := range response.Data.Commands {
				if strings.HasPrefix(command.Name, "bench-") {
					loaded++
				}
			}
			if loaded == count {
				return
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the %d bench extensions did not all load within a minute", count)
}

// The cost each RPC turn adds with ten TypeScript extensions loaded (pi-arena rpc-ext10), host side: every turn dispatches before_agent_start and the turn_end boundary to each extension, with the context preview Pi hands turn_end handlers.
//
// A turn late in a long session costs what an early one does: the cost of turns 13 to 50 per turn over that of turns 3 to 12 (allocs_growth) stays near 1. Work that projects or encodes the whole history on each turn raises it.
func TestRPCExtensionTurnProcessGate(t *testing.T) {
	rpcBenchTurnGate(t, "process.rpc_ext10_turn", writeBenchExtensions(t), 50)
}

// The same turns with no extension loaded: the difference from process.rpc_ext10_turn is what the ten extensions cost per turn, and the difference of the allocs_growth rows is what history costs the extensions.
func TestRPCNoExtensionTurnProcessGate(t *testing.T) {
	rpcBenchTurnGate(t, "process.rpc_ext0_turn", nil, 50)
}

// rpcBenchTurnGate gates the allocations of one RPC turn with extensions loaded: the difference between 12 and 2 turns, per turn. With late turns, it also gates the per-turn allocations between 12 and late turns relative to that cost.
//
// The gated counts are differences between processes, so a process's startup must allocate the same amount every time. Three inputs made
// it depend on the machine's load, and each is removed or outvoted here:
//   - Collections: a collection empties every sync.Pool, which then refills, and the number and timing of collections follow the load.
//     The measured processes run with GOGC=off.
//   - The background catalog refresh RPC mode starts (Pi's main.ts unless offline) publishes the model catalog to the extensions, once or
//     not at all depending on whether it finishes before they connect. The measured processes run with PIG_OFFLINE=1; the turns use the
//     faux provider and no network.
//   - The model catalog publications of startup still race the extensions' connections: about one process in thirty encodes the catalog
//     once more or once less (about 8,400 allocations). Each point is the median of three processes, so one such process cannot move it.
//
// Wave 15 failed allocs_growth at 1.22 where the code measures 1.05; with these inputs fixed the counts no longer move with load.
func rpcBenchTurnGate(t *testing.T, metric string, extensions []string, late int) {
	home := newPigHome(t)
	home.env = append(home.env, "GOGC=off", "PIG_OFFLINE=1")
	args := []string{"--no-session", "--mode", "rpc", "--model", "test-faux/faux-1"}
	for _, path := range extensions {
		args = append(args, "-e", path)
	}
	turns := func(n int) func(int, io.Writer, *bufio.Reader) {
		return func(pid int, stdin io.Writer, stdout *bufio.Reader) {
			awaitBenchCommands(t, len(extensions), stdin, stdout)
			rpcTurns(t, n)(pid, stdin, stdout)
		}
	}
	// median runs n turns in three processes and returns the record of the one with the median allocation count.
	median := func(n int) profiling.ProcessMetrics {
		records := []profiling.ProcessMetrics{home.run(args, turns(n)), home.run(args, turns(n)), home.run(args, turns(n))}
		slices.SortFunc(records, func(a, b profiling.ProcessMetrics) int { return cmp.Compare(a.AllocObjects, b.AllocObjects) })
		return records[1]
	}
	home.run(args, turns(0))
	const few, many = 2, 12
	small := median(few)
	large := median(many)
	perTurn := float64(many - few)
	t.Logf("perfgate: %s %d turns %+v; %d turns %+v", metric, few, small, many, large)
	early := counterDelta(large.AllocObjects, small.AllocObjects) / perTurn
	perfgate.Check(t, metric+".allocs", early)
	perfgate.Check(t, metric+".bytes", counterDelta(large.AllocBytes, small.AllocBytes)/perTurn)
	if late == 0 {
		return
	}
	longer := median(late)
	lateTurn := counterDelta(longer.AllocObjects, large.AllocObjects) / float64(late-many)
	t.Logf("perfgate: %s %d turns %+v; per turn after %d turns %.0f allocs, after %d turns %.0f allocs", metric, late, longer, few, early, many, lateTurn)
	perfgate.Check(t, metric+".allocs_growth", math.Round(lateTurn/early*100)/100)
}

// The same turns at one and three extensions, so a per-extension term and a fixed term cannot hide behind each other.
func TestRPCExtensionTurnProcessGateByExtensionCount(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("ext%d", count), func(t *testing.T) {
			rpcBenchTurnGate(t, fmt.Sprintf("process.rpc_ext%d_turn", count), writeBenchExtensions(t)[:count], 0)
		})
	}
}
