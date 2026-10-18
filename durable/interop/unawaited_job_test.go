package interop

// Evidence for D181: the commit order of an unawaited job.
//
// A job that runs beside the tool call that created it commits against the generation chain in an order Pi settles by
// microtask hops and PiG's reference harness settles by goroutine scheduling (it has no hop model). Run after run PiG's
// order is Pi's, and now and then the job's commits land earlier or later than Pi's (shapes: the job's first phase before
// the tool call's settle; the job's terminal after the generation's next turn, after the answer). The test forces the
// order that has an observable consequence: the job's terminal commit lands after the answer, which a channel holds back
// until the first turn has settled (no sleeps, no race). It shows what that order does and does not change.
//
//   - Every settled read equals Pi's content: the raw tables of the whole scripted session differ from Pi Durable's only in
//     commit sequence numbers (entries.commit_seq, document_revisions.seq), and the Harness API view is equal.
//   - The settled reads of the same session with the job awaited, where the job's terminal commit precedes the answer in
//     both runtimes, are equal too: task receipts, tasks, Inspect, entries, context, documents (pi.inbox included), as-of
//     reads at every entry and the WatchEvents sequence.
//   - The one read that differs is the transient one: when Submission.Wait returns, the job is still live (Inspect lists
//     it, GetTask shows it running), where Pi's order has it terminal.
//
// The hold fixes the order in either runtime, so this test does not turn red when the engine orders the job like Pi. It
// turns red when the order has any other effect: a content difference in a settled read, a seq difference outside
// entries.commit_seq and document_revisions.seq, or a different transient.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// stripSeq drops every commit sequence number of a decoded dump: what is left is the content the session wrote.
func stripSeq(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if key == "commit_seq" || key == "seq" || key == "next_seq" {
				continue
			}
			out[key] = stripSeq(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = stripSeq(item)
		}
		return out
	}
	return value
}

// onlySeqDifferences fails unless every difference between two dumps is a commit sequence number.
func onlySeqDifferences(t *testing.T, piDump, goDump any) (seqDifferences int) {
	t.Helper()
	var all []string
	differences("$", piDump, goDump, 10_000, &all)
	for _, difference := range all {
		path, _, _ := strings.Cut(difference, ": ")
		if !strings.HasSuffix(path, ".commit_seq") && !strings.HasSuffix(path, ".seq") && !strings.HasSuffix(path, ".next_seq") {
			t.Errorf("a difference that is not a commit sequence number: %s", difference)
		}
	}
	return len(all)
}

// session is what one scripted run lets a user read.
type session struct {
	afterHello map[string]any // the reads the moment the first turn's Submission.Wait returned
	settled    map[string]any // the reads once every task settled, before the Harness closed
	events     []string       // the WatchEvents sequence of the root conversation, in delivery order
}

// run runs scenario 2 over path with the job unawaited or awaited, its terminal commit held back or not, and records the
// reads. hold, when set, is closed by the test once the first turn settled and its transient reads were taken.
func run(t *testing.T, path string, unawaited, hold bool) *session {
	t.Helper()
	unawaitedJob = unawaited
	var held chan struct{}
	if hold {
		held = make(chan struct{})
	}
	holdJobTerminal = held
	var stream *harness.AgentEventStream
	out := &session{}
	var mu sync.Mutex
	hooks.afterOpen = func(t *testing.T, opened harness.Harness) {
		stream = must(harness.WatchEvents(background, opened, rootOf(t, opened).Id()))
		stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
			mu.Lock()
			defer mu.Unlock()
			for _, event := range batch {
				out.events = append(out.events, event.EventType())
			}
			return nil
		})
	}
	hooks.afterHello = func(t *testing.T, opened harness.Harness) {
		out.afterHello = transientReads(t, opened)
		if held != nil {
			close(held)
		}
	}
	hooks.beforeClose = func(t *testing.T, opened harness.Harness) { out.settled = settledReads(t, opened) }
	t.Cleanup(func() {
		unawaitedJob, holdJobTerminal = false, nil
		hooks.afterOpen, hooks.afterHello, hooks.beforeClose = nil, nil, nil
	})
	runScenario2(t, path)
	// Closing the Harness ends the watch after the deliveries queued before it.
	<-stream.Closed()
	mu.Lock()
	defer mu.Unlock()
	out.events = slices.Clone(out.events)
	return out
}

// transientReads are the reads of a user whose Submission.Wait just returned: the live tasks and the job's record.
func transientReads(t *testing.T, opened harness.Harness) map[string]any {
	t.Helper()
	reads := map[string]any{}
	live := []string{}
	for _, task := range must(opened.Inspect(background)).Tasks {
		live = append(live, task.Record.Kind)
		if task.Record.Kind == "app.job" {
			reads["job"] = task.Record
			reads["jobRecord"] = must(opened.GetTask(background, task.Record.Id))
		}
	}
	reads["liveKinds"] = live
	return reads
}

// settledReads are the public reads of a Harness whose tasks all settled.
func settledReads(t *testing.T, opened harness.Harness) map[string]any {
	t.Helper()
	root := rootOf(t, opened)
	reads := map[string]any{}
	ascending := durable.ScanAscending
	entries := must(root.Entries(background, durable.EntryQuery{Order: &ascending}, 100, nil)).Items
	reads["entries"] = entries
	reads["context"] = must(root.Context(background, nil))
	reads["inspect"] = must(opened.Inspect(background))
	reads["idle"] = fmt.Sprint(root.WaitForIdle(background))
	tasks, receipts := map[string]any{}, map[string]any{}
	for id := durable.TaskId(1); id < 64; id++ {
		record := must(opened.GetTask(background, id))
		if record == nil {
			continue
		}
		tasks[strconv.Itoa(int(id))] = record
		receipts[strconv.Itoa(int(id))] = must(opened.WaitForTask(background, id))
	}
	reads["tasks"], reads["receipts"] = tasks, receipts
	reads["scanTasks"] = must(durable.Commit(background, opened, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{}, 1000, nil)
	}))
	reads["docs"] = map[string]any{
		"notes": must(durable.Snapshot[notes](background, opened, notesDoc, root.Id())),
		"plan":  must(durable.Snapshot[plan](background, opened, planDoc, root.Id())),
		"agent": must(durable.Snapshot[harness.AgentState](background, opened, harness.AgentDoc, root.Id())),
		"live":  must(durable.Snapshot[harness.LiveState](background, opened, harness.LiveDoc, root.Id())),
		"usage": must(durable.Snapshot[harness.UsageState](background, opened, harness.UsageDoc, root.Id())),
		"inbox": must(durable.Snapshot[harness.InboxState](background, opened, harness.InboxDoc, root.Id())),
	}
	asOf := map[string]any{}
	for _, entry := range entries {
		asOf[strconv.Itoa(int(entry.Id))] = map[string]any{
			"plan":    try(durable.SnapshotAsOf[plan](background, opened, planDoc, entry.Id, root.Id())),
			"agent":   try(durable.SnapshotAsOf[harness.AgentState](background, opened, harness.AgentDoc, entry.Id, root.Id())),
			"live":    try(durable.SnapshotAsOf[harness.LiveState](background, opened, harness.LiveDoc, entry.Id, root.Id())),
			"usage":   try(durable.SnapshotAsOf[harness.UsageState](background, opened, harness.UsageDoc, entry.Id, root.Id())),
			"context": must(root.Context(background, &durable.ContextOptions{At: &entry.Id})),
		}
	}
	reads["asOf"] = asOf
	return reads
}

func TestUnawaitedJobCommittedAfterTheAnswerChangesOnlyOrder(t *testing.T) {
	dir := t.TempDir()
	piStore := filepath.Join(dir, "pi.sqlite")
	nodeWith(t, []string{"PIG_INTEROP_UNAWAITED_JOB=1"}, "scenario2.mjs", piStore)
	piRaw, piAPI := decode(t, node(t, "rawdump.mjs", piStore)), decode(t, node(t, "apidump.mjs", piStore))

	// The forced order: the job's terminal commit follows the answer.
	forcedStore := filepath.Join(dir, "forced.sqlite")
	forced := run(t, forcedStore, true, true)

	// The transient read: the answer settled and the job is still live.
	if !slices.Contains(forced.afterHello["liveKinds"].([]string), "app.job") {
		t.Fatalf("when Submission.Wait returned, Inspect lists %v, want the job live", forced.afterHello["liveKinds"])
	}
	record := forced.afterHello["jobRecord"].(*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue])
	if record.State.Status == durable.TaskTerminal {
		t.Fatalf("when Submission.Wait returned, the job is %v, want it not terminal", record.State.Status)
	}

	// The same session with the job awaited: its terminal commit precedes the answer, as in Pi, and nothing is live then.
	controlStore := filepath.Join(dir, "control.sqlite")
	control := run(t, controlStore, false, false)
	if live := control.afterHello["liveKinds"].([]string); len(live) != 0 {
		t.Fatalf("with the job awaited, Inspect lists %v when Submission.Wait returned, want none", live)
	}

	// Settled reads: the same in both orders.
	expectSame(t, "settled reads, job awaited (first) and job after the answer (second)", viaJSON(t, control.settled), viaJSON(t, forced.settled))
	if !slices.Equal(control.events, forced.events) {
		t.Errorf("WatchEvents sequence, job awaited %v, job after the answer %v", control.events, forced.events)
	}

	// Pi's content: the raw tables differ from Pi Durable's in commit sequence numbers only, and the API view not at all.
	goRaw := rawDump(t, forcedStore)
	expectSame(t, "raw tables without commit sequence numbers", stripSeq(piRaw), stripSeq(goRaw))
	if n := onlySeqDifferences(t, piRaw, goRaw); n == 0 {
		t.Errorf("the job's terminal commit follows the answer, so the commit sequence numbers must differ from Pi's, where it precedes it")
	}
	expectSame(t, "Harness API view", piAPI, apiDump(t, forcedStore))
}

// try is a read's value, or its error as text: a document that retains no history rejects an as-of read the same way for
// every order.
func try[T any](value T, err error) any {
	if err != nil {
		return "error: " + err.Error()
	}
	return value
}
