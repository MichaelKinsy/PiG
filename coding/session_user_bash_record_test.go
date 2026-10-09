package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Interactive `!cmd` results go through recordBashResult (agent-session.ts): when idle the bashExecution message is
// persisted and enters the live context the next prompt sends; while a run streams it is queued and flushed after the
// run, so it cannot split a tool call from its result. PiG's interactive mode appended to the inner Session directly,
// so the next prompt never carried the output and a streaming append landed mid-run.
func TestRecordUserBashResultEntersContextAndDefersWhileStreaming(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.RecordUserBashResult("echo idle", icodingagent.BashResult{Output: "idle-out", ExitCode: new(0)}, false); err != nil {
		t.Fatal(err)
	}
	msgs := sess.Agent().Messages()
	if len(msgs) != 1 || msgs[0].Custom == nil || msgs[0].Custom["role"] != agent.RoleBashExecution || msgs[0].Custom["output"] != "idle-out" {
		t.Fatalf("an idle user bash result is not in the live context: %+v", msgs)
	}
	if !hasBashEntry(sess) {
		t.Fatal("an idle user bash result was not persisted")
	}

	sess.mu.Lock()
	err = sess.RecordUserBashResult("echo streaming", icodingagent.BashResult{Output: "streaming-out", ExitCode: new(0)}, false)
	entries := len(sess.Inner().GetEntries())
	if err == nil {
		err = sess.flushPendingBashLocked()
	}
	sess.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sess.Inner().GetEntries()); got != entries+1 {
		t.Fatalf("a user bash result during a run was persisted before the run ended: entries %d before flush, %d after", entries, got)
	}
	if got := len(sess.Agent().Messages()); got != 2 {
		t.Fatalf("the flushed user bash result is not in the live context: %d messages", got)
	}
}
