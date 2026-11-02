package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// storedLive is the pi.live value Storage holds, which a commit held in memory (PROGRESS_COMMIT) is not in.
func (r *rig) storedLive(conv session.ID) string {
	r.t.Helper()
	record, err := r.mem.FindDocument(session.Address{Kind: turn.LiveDef.Kind, Scope: session.Scope{Kind: session.ScopeConversation, Owner: conv}}, session.Current)
	if err != nil || record == nil {
		r.t.Fatalf("find pi.live: %v %v", record, err)
	}
	id, _ := record.Get("id")
	doc, err := r.mem.Document(session.ID(id.(float64)), session.Current)
	if err != nil {
		r.t.Fatal(err)
	}
	return jv.Stringify(doc.Value)
}

// harness-live-deltas.test.ts, harness-deferred-progress.test.ts: the attempt a generation shows is presentation, stored like progress
// with the next commit instead of by a commit of its own.
func TestGenerationAttemptIsStoredWithTheNextCommit(t *testing.T) {
	r := newRig(t)
	conv, _ := r.start("hi")
	r.resume()
	if len(r.requests) != 1 {
		t.Fatalf("requests = %d", len(r.requests))
	}
	if live := r.doc(turn.LiveDef, conv); !strings.Contains(live, `"generation":{"attempt":1}`) {
		t.Fatalf("live while the request is in flight = %s, want the attempt shown", live)
	}
	if stored := r.storedLive(conv); strings.Contains(stored, `"generation"`) {
		t.Fatalf("stored live = %s, want the attempt held in memory", stored)
	}
	r.finish(assistantMessage("stop", "done"))
	if stored := r.storedLive(conv); stored != `{}` {
		t.Fatalf("stored live after the answer = %s", stored)
	}
}

// harness-live-deltas.test.ts "the running status is deferred like progress, so it is stored with the result": the tool slot shows
// running at once, and the commit of the result stores it done.
func TestToolRunningStatusIsStoredWithTheNextCommit(t *testing.T) {
	r := newRig(t)
	r.toolRound(`{"text":"x"}`)
	run := r.toolRuns[0]
	if live := r.doc(turn.LiveDef, 1); !strings.Contains(live, `"status":"running"`) {
		t.Fatalf("live while the tool runs = %s", live)
	}
	if stored := r.storedLive(1); strings.Contains(stored, `"status":"running"`) || !strings.Contains(stored, `"status":"pending"`) {
		t.Fatalf("stored live = %s, want the slot pending: its running status is held in memory", stored)
	}
	run.then(mustParse(t, `{"output":[{"type":"text","text":"ok"}]}`), nil)
	r.run()
	// The result's commit stored the slot's change with its own, and the next generation took the round over.
	if stored := r.storedLive(1); strings.Contains(stored, `"running"`) || strings.Contains(stored, `"pending"`) {
		t.Fatalf("stored live after the result = %s, want no slot left running", stored)
	}
}

// harness-tools-recovery.test.ts "reruns a safe tool whose running slot a crash after its intent lost, or a sibling stored, and shows it
// running": the intent is durable but the slot's running status was only in memory, so the rerun shows it again.
func TestSafeToolRerunShowsTheSlotRunning(t *testing.T) {
	r := newRig(t)
	tool := echoTool()
	tool.Replay = "safe"
	r.agent.Tools = []turn.Tool{tool}
	r.start("hi")
	r.resume()
	r.finish(toolUseWith("echo", `{"text":"x"}`))
	if len(r.toolRuns) != 1 {
		t.Fatalf("tool runs = %d", len(r.toolRuns))
	}
	if stored := r.storedLive(1); !strings.Contains(stored, `"status":"pending"`) {
		t.Fatalf("stored live before the crash = %s, want the slot pending", stored)
	}
	// The process dies here: a new Session opens the same Storage, without what was held in memory.
	r.restart()
	if len(r.toolRuns) != 1 {
		t.Fatalf("safe tool runs after the restart = %d (reports %v)", len(r.toolRuns), r.reports)
	}
	if live := r.doc(turn.LiveDef, 1); !strings.Contains(live, `"status":"running"`) {
		t.Fatalf("live while the rerun runs = %s, want the slot running again", live)
	}
	r.toolRuns[0].then(mustParse(t, `{"output":[{"type":"text","text":"again"}]}`), nil)
	r.run()
	if got := lastEntryOf(r, "pi.tool-result"); !strings.Contains(got, `"text":"again"`) {
		t.Fatalf("result = %s", got)
	}
}
