package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/MichaelKinsy/PiG/coding"
)

const testFauxRPCOraclePath = "../../coding/testdata/rpc33-observation/providers/test-faux/pi.json"

// TestTestFauxRPCObservation runs the real `pig --mode rpc` binary against ai/test_faux.go and compares its assistant message records with the records of Pi's real `--mode rpc` process running the paired fixture test/parity/testdata/test-faux-provider.ts.
// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363; test/parity/testdata/test-faux-provider.ts:streamTestFaux.
// PIG_TEST_FAUX_RPC_RUNS repeats every scenario; the recorded determinism run used 200.
func TestTestFauxRPCObservation(t *testing.T) {
	data, err := os.ReadFile(testFauxRPCOraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Pi      string `json:"pi"`
		Results []struct {
			Scenario struct {
				Name   string `json:"name"`
				Prompt string `json:"prompt"`
			} `json:"scenario"`
			RPC [][]struct {
				Kind   string          `json:"kind"`
				Record json.RawMessage `json:"record"`
			} `json:"rpc"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Pi != coding.UpstreamVersion {
		t.Fatalf("test-faux oracle pins Pi %s; current Pi %s requires a fresh oracle", oracle.Pi, coding.UpstreamVersion)
	}
	runs := 3
	if value := os.Getenv("PIG_TEST_FAUX_RPC_RUNS"); value != "" {
		if _, err := fmt.Sscan(value, &runs); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range oracle.Results {
		t.Run(result.Scenario.Name, func(t *testing.T) {
			t.Parallel()
			var want []json.RawMessage
			for _, entry := range result.RPC[0] {
				if entry.Kind == "serialize" {
					want = append(want, entry.Record)
				}
			}
			if len(want) < 2 {
				t.Fatalf("oracle holds %d assistant records", len(want))
			}
			wantJSON := fauxRPCComparable(t, want)
			for run := range runs {
				home := t.TempDir()
				cwd := t.TempDir()
				if err := os.WriteFile(filepath.Join(cwd, "parity-read-target.txt"), []byte("hello\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				p := startRPCProcessAt(t, cwd, []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session", "--model", "test-faux/faux-1")
				p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": result.Scenario.Prompt})
				var got []json.RawMessage
				p.await("agent_settled", func(record rpcRecord) bool {
					raw, err := json.Marshal(record)
					if err != nil {
						t.Fatal(err)
					}
					var head struct {
						Type    string `json:"type"`
						Message *struct {
							Role string `json:"role"`
						} `json:"message"`
					}
					if err := json.Unmarshal(raw, &head); err != nil {
						t.Fatal(err)
					}
					if (head.Type == "message_start" || head.Type == "message_end") && head.Message != nil && head.Message.Role == "assistant" || head.Type == "message_update" {
						got = append(got, raw)
					}
					return head.Type == "agent_settled"
				})
				if gotJSON := fauxRPCComparable(t, got); !bytes.Equal(wantJSON, gotJSON) {
					t.Fatalf("run %d differs from Pi:\n%s", run, gotextdiff.ToUnified("Pi", "Go", string(wantJSON), myers.ComputeEdits(span.URIFromPath(t.Name()), string(wantJSON), string(gotJSON))))
				}
				p.closeAndWait("test-faux RPC process")
			}
		})
	}
}
