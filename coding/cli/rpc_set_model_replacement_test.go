package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Pi 1.1.0 answers a set_model of the same input batch as a new_session with success whichever comes first: set_model's `await modelRuntime.checkAuth`
// resumes in the microtasks of its batch, while the replacement starts after the batch (rpc-mode.ts:470-478, agent-session.ts:2477-2480). Measured 3 of 3:
// new_session,set_model answers 2, 1 and set_model,new_session answers 1, 2, every answer a success.
func TestRPCSetModelAnswersSuccessWhenANewSessionIsInItsBatch(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	setModel := `{"id":"M","type":"set_model","provider":"test-faux","modelId":"faux-1"}`
	newSession := `{"id":"N","type":"new_session"}`
	for _, tc := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{"new_session first", []string{newSession, setModel}, []string{"M", "N"}},
		{"set_model first", []string{setModel, newSession}, []string{"M", "N"}},
	} {
		for run := range 3 {
			dir := t.TempDir()
			current := filepath.Join(dir, "current.jsonl")
			if err := os.WriteFile(current, source, 0o600); err != nil {
				t.Fatal(err)
			}
			p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
			if _, err := io.WriteString(p.stdin, tc.lines[0]+"\n"+tc.lines[1]+"\n"); err != nil {
				t.Fatal(err)
			}
			var order []string
			failures := map[string]any{}
			p.await("two responses", func(record rpcRecord) bool {
				if record["type"] != "response" {
					return false
				}
				id := responseID(t, record)
				order = append(order, id)
				if record["success"] != true {
					failures[id] = record["error"]
				}
				return len(order) == 2
			})
			if len(failures) != 0 {
				t.Fatalf("%s run %d: failed answers %v, want every answer a success", tc.name, run, failures)
			}
			if !slices.Equal(order, tc.want) {
				t.Fatalf("%s run %d: answers = %v, want %v", tc.name, run, order, tc.want)
			}
			p.closeAndWait("after the answers")
		}
	}
}
