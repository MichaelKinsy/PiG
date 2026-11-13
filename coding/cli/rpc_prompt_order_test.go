package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Two prompts of one batch (D629). PiG's second prompt finds the Session busy in BeginRunStart, where the first marked it, and is rejected for
// naming no streamingBehavior; its error answers before the first prompt's "started", and one run follows. Pi 1.5.2 (agent-session.ts:2162-2196
// and :1943-1988) awaits _preparePrompt in prompt() (:2191), so both prompts pass its isStreaming check (:2227) before either reaches _startRun
// (:2195), which marks the Session busy (:1944) without a check: both answer "started" (:1960), the second's agent.prompt throws (:1969), its
// error is dropped and an agent_settled is emitted for it (:1986). TestRPCPromptsWrittenInOneChunkDivergeFromPi records Pi's side.
func TestRPCPromptsOfOneBatchAnswerInRequestOrder(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for run := range 5 {
		dir := t.TempDir()
		current := filepath.Join(dir, "current.jsonl")
		if err := os.WriteFile(current, source, 0o600); err != nil {
			t.Fatal(err)
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		if _, err := io.WriteString(p.stdin, `{"id":"p","type":"prompt","message":"hi"}`+"\n"+`{"id":"p2","type":"prompt","message":"hi2"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		order, rejected := awaitBatchPromptOrder(p)
		if want := []string{"p2", "p", "agent_start", "agent_end"}; !slices.Equal(order, want) {
			t.Fatalf("run %d: order = %v, want %v", run, order, want)
		}
		if rejected != "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message." {
			t.Fatalf("run %d: p2 error = %q", run, rejected)
		}
		p.closeAndWait("after the run")
	}
}

// awaitBatchPromptOrder records the responses and run events up to the first agent_end, and p2's error.
func awaitBatchPromptOrder(p *rpcProcess) (order []string, rejected string) {
	p.await("the run's end", func(record rpcRecord) bool {
		switch record["type"] {
		case "response":
			order = append(order, record["id"].(string))
			if record["id"] == "p2" && record["success"] == false {
				rejected, _ = record["error"].(string)
			}
		case "agent_start", "agent_end":
			order = append(order, record["type"].(string))
		}
		return record["type"] == "agent_end"
	})
	return order, rejected
}

// The prompts of one batch whose compaction check finds a compaction due (a Session whose last response filled 126k of faux-1's 128k window, slow
// summary). The first prompt is busy from BeginRunStart on while its compaction runs (Pi 1.5.2's _startRun, agent-session.ts:1944-1948), so
// PiG rejects the second at once and its error answers first (D629).
func TestRPCPromptsWithDueCompactionsAnswerInRequestOrder(t *testing.T) {
	session := strings.ReplaceAll(largeRPCSession(), `"totalTokens":2,`, `"totalTokens":126000,`)
	for run := range 3 {
		dir := t.TempDir()
		current := filepath.Join(dir, "current.jsonl")
		if err := os.WriteFile(current, []byte(session), 0o600); err != nil {
			t.Fatal(err)
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "TEST_FAUX_SLOW_COMPACTION=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		if _, err := io.WriteString(p.stdin, `{"id":"p","type":"prompt","message":"hi"}`+"\n"+`{"id":"p2","type":"prompt","message":"hi2"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		order, rejected := awaitBatchPromptOrder(p)
		if want := []string{"p2", "p", "agent_start", "agent_end"}; !slices.Equal(order, want) || rejected == "" {
			t.Fatalf("run %d: order = %v (p2 error %q), want %v with p2 rejected", run, order, rejected, want)
		}
		p.closeAndWait("after the run")
	}
}
