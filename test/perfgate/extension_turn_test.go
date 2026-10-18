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
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
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

// The cost each RPC turn adds with ten TypeScript extensions loaded (buffon rpc-ext10), host side: every turn dispatches before_agent_start and the turn_end boundary to each extension, with the context preview Pi hands turn_end handlers.
//
// A turn late in a long session costs what an early one does: the cost of turns 13 to 50 per turn over that of turns 3 to 12 (allocs_growth) stays near 1. Work that projects or encodes the whole history on each turn raises it.
func TestRPCExtensionTurnProcessGate(t *testing.T) {
	rpcBenchTurnGate(t, "process.rpc_ext10_turn", writeBenchExtensions(t), 50)
}

// The same turns with no extension loaded: the difference from process.rpc_ext10_turn is what the ten extensions cost per turn, and the difference of the allocs_growth rows is what history costs the extensions.
func TestRPCNoExtensionTurnProcessGate(t *testing.T) {
	rpcBenchTurnGate(t, "process.rpc_ext0_turn", nil, 50)
}

// allocCounts are cumulative heap allocations: objects and bytes.
type allocCounts struct{ objects, bytes uint64 }

// allocSnapshot signals the memory probe (memProbeBinary) of the process pid and returns the allocation counts it wrote as allocs-<n>.json in dir.
func allocSnapshot(t *testing.T, pid int, dir string, n int) allocCounts {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("allocs-%d.json", n))
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			values := map[string]uint64{}
			if err := json.Unmarshal(data, &values); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			return allocCounts{values["/gc/heap/allocs:objects"], values["/gc/heap/allocs:bytes"]}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the probe wrote no %s", path)
	return allocCounts{}
}

// rpcBenchTurnGate gates the allocations of one RPC turn with extensions loaded: the difference between 12 and 2 turns, per turn. With late turns, it also gates the per-turn allocations between 12 and late turns relative to that cost.
//
// The gated counts are differences between processes, so what a process allocates before its turns must not differ between processes. Three
// inputs made it depend on the machine's load, and each is removed here:
//   - Collections: a collection empties every sync.Pool, which then refills, and the number and timing of collections follow the load.
//     The measured processes run with GOGC=off.
//   - The background catalog refresh RPC mode starts (Pi's main.ts unless offline) publishes the model catalog to the extensions, once or
//     not at all depending on whether it finishes before they connect. The measured processes run with PIG_OFFLINE=1; the turns use the
//     faux provider and no network.
//   - The model catalog publications of startup still race the extensions' connections: with the machine loaded, about one process in
//     twenty encodes the catalog fewer times (36,000 fewer allocations in the arm64 container with eight busy processes beside it), and
//     one in thirty did once more or once less on CI. A process's turns are therefore counted from a snapshot of its allocation counters
//     taken once its extensions have registered their commands, by the memory probe binary, and not from its start: the startup
//     publications are in the count before the snapshot and the turns' own work after it, in every process.
//
// Each point is still the median of three processes. Wave 15 failed allocs_growth at 1.22 where the code measures 1.06; the counts no longer move with load.
func rpcBenchTurnGate(t *testing.T, metric string, extensions []string, late int) {
	home := newPigHome(t)
	home.env = append(home.env, "GOGC=off", "PIG_OFFLINE=1")
	args := []string{"--no-session", "--mode", "rpc", "--model", "test-faux/faux-1"}
	for _, path := range extensions {
		args = append(args, "-e", path)
	}
	probe, err := memProbeBinary()
	if err != nil {
		t.Fatal(err)
	}
	// measure runs n turns in a process and returns what they allocated: the exit record's counts, which include the exit itself and are the same in every process, less the snapshot's.
	measure := func(n int) allocCounts {
		snapshots := t.TempDir()
		var before allocCounts
		record := home.runBinary(probe, []string{"PIG_PERFGATE_SNAPSHOT_DIR=" + snapshots}, args, func(pid int, stdin io.Writer, stdout *bufio.Reader) {
			awaitBenchCommands(t, len(extensions), stdin, stdout)
			before = allocSnapshot(t, pid, snapshots, 1)
			rpcTurns(t, n)(pid, stdin, stdout)
		})
		return allocCounts{record.AllocObjects - before.objects, record.AllocBytes - before.bytes}
	}
	// median returns the counts of the one of three processes with the median number of allocations.
	median := func(n int) allocCounts {
		counts := []allocCounts{measure(n), measure(n), measure(n)}
		slices.SortFunc(counts, func(a, b allocCounts) int { return cmp.Compare(a.objects, b.objects) })
		return counts[1]
	}
	measure(0)
	const few, many = 2, 12
	small := median(few)
	large := median(many)
	perTurn := float64(many - few)
	t.Logf("perfgate: %s %d turns %+v; %d turns %+v", metric, few, small, many, large)
	early := counterDelta(large.objects, small.objects) / perTurn
	perfgate.Check(t, metric+".allocs", early)
	perfgate.Check(t, metric+".bytes", counterDelta(large.bytes, small.bytes)/perTurn)
	if late == 0 {
		return
	}
	longer := median(late)
	lateTurn := counterDelta(longer.objects, large.objects) / float64(late-many)
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
