// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// streamPrompt makes the faux provider stream 24 text deltas, one message_update each.
const streamPrompt = "TUI_LIVE_STREAM"

// writeEventExtensions writes count TypeScript extensions that each register a command and subscribe to event with an empty handler.
func writeEventExtensions(t *testing.T, count int, event string) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, count)
	for i := range paths {
		n := fmt.Sprintf("%02d", i+1)
		source := `export default function (pi: any) {
  pi.registerCommand("bench-` + n + `", { description: "Bench command ` + n + `", handler: async () => {} });
  pi.on("` + event + `", () => {});
}
`
		paths[i] = filepath.Join(dir, "bench-ext-"+n+".ts")
		if err := os.WriteFile(paths[i], []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

// streamedTurns drives turns prompts that stream, each to its agent_settled event, and returns the number of message_update events RPC mode wrote.
func streamedTurns(t *testing.T, turns int, updates *int) func(int, io.Writer, *bufio.Reader) {
	return func(_ int, stdin io.Writer, stdout *bufio.Reader) {
		for i := range turns {
			if _, err := fmt.Fprintf(stdin, `{"id":"p%d","type":"prompt","message":%q}`+"\n", i, streamPrompt); err != nil {
				t.Fatal(err)
			}
			for {
				line, err := stdout.ReadBytes('\n')
				if err != nil {
					t.Fatalf("turn %d: RPC output ended before agent_settled: %v", i, err)
				}
				if bytes.Contains(line, []byte(`"type":"message_update"`)) {
					*updates++
				}
				var event struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(line, &event) == nil && event.Type == "agent_settled" {
					break
				}
			}
		}
	}
}

// messageUpdateTurn is what one streamed turn allocates in the host and the message_update events it dispatched.
type messageUpdateTurn struct {
	counts  allocCounts
	updates int
}

// streamedTurnCost returns the median over three processes of what one streamed turn allocates with extensions loaded: the difference of a four-turn and a one-turn process, per turn.
func streamedTurnCost(t *testing.T, extensions []string) messageUpdateTurn {
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
	measure := func(n int) messageUpdateTurn {
		snapshots := t.TempDir()
		var before allocCounts
		var updates int
		record := home.runBinary(probe, []string{"PIG_PERFGATE_SNAPSHOT_DIR=" + snapshots}, args, func(pid int, stdin io.Writer, stdout *bufio.Reader) {
			awaitBenchCommands(t, len(extensions), stdin, stdout)
			before = allocSnapshot(t, pid, snapshots, 1)
			streamedTurns(t, n, &updates)(pid, stdin, stdout)
		})
		return messageUpdateTurn{allocCounts{record.AllocObjects - before.objects, record.AllocBytes - before.bytes}, updates}
	}
	median := func(n int) messageUpdateTurn {
		runs := []messageUpdateTurn{measure(n), measure(n), measure(n)}
		slices.SortFunc(runs, func(a, b messageUpdateTurn) int { return cmp.Compare(a.counts.objects, b.counts.objects) })
		return runs[1]
	}
	measure(0)
	const few, many = 1, 4
	small, large := median(few), median(many)
	return messageUpdateTurn{
		counts:  allocCounts{(large.counts.objects - small.counts.objects) / (many - few), (large.counts.bytes - small.counts.bytes) / (many - few)},
		updates: (large.updates - small.updates) / (many - few),
	}
}

// What one message_update costs the host per subscribing handler, over a streamed reply to ten TypeScript extensions' worth of runtime: the turn with count extensions that subscribe to message_update, less the turn with as many that subscribe to agent_end (the same startup, state and one event per run), per update and subscriber.
func TestRPCMessageUpdateProcessGate(t *testing.T) {
	for _, count := range []int{1, 4} {
		t.Run(fmt.Sprintf("ext%d", count), func(t *testing.T) {
			control := streamedTurnCost(t, writeEventExtensions(t, count, "agent_end"))
			subscribed := streamedTurnCost(t, writeEventExtensions(t, count, "message_update"))
			if subscribed.updates <= 0 {
				t.Fatalf("no message_update events seen")
			}
			handlers := float64(subscribed.updates * count)
			allocs := (float64(subscribed.counts.objects) - float64(control.counts.objects)) / handlers
			byteCost := (float64(subscribed.counts.bytes) - float64(control.counts.bytes)) / handlers
			t.Logf("perfgate: process.rpc_message_update ext%d: %d updates per turn; control %+v, subscribed %+v", count, subscribed.updates, control.counts, subscribed.counts)
			perfgate.Check(t, fmt.Sprintf("process.rpc_message_update_ext%d.allocs", count), allocs)
			perfgate.Check(t, fmt.Sprintf("process.rpc_message_update_ext%d.bytes", count), byteCost)
		})
	}
}
