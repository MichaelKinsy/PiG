// Ports the "durable code mode" tests of Pi 1.2.0 packages/durable/test/codemode.test.ts.

package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func expectText(t *testing.T, got string, isError bool, want string, wantError bool) {
	t.Helper()
	if got != want || isError != wantError {
		t.Fatalf("result = %q (error %v), want %q (error %v)", got, isError, want, wantError)
	}
}

func TestDurableCodeMode(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/codemode.test.ts:193 "runs a script that calls tools as nested calls, and journals each result before delivering it"
	t.Run("runs a script that calls tools as nested calls, and journals each result before delivering it", func(t *testing.T) {
		f := codemodeChat(t)
		f.holds.block.Store(false)
		f.setup.faux.SetResponses(nil)
		f.setup.faux.SetResponses(append([]ai.FauxResponseStep{}, script(`const a = await tools.echo({ text: "a" }); const b = await tools.echo({ text: "b" }); return [a, b];`), done()))
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		if _, err := submitGo(t, root).Wait(testContext); err != nil {
			t.Fatal(err)
		}
		text, isError := resultText(t, root)
		expectText(t, text, isError, `["echo a","echo b"]`, false)
		if got := nestedCount(t, opened); got != 2 {
			t.Fatalf("%d nested calls, want 2", got)
		}
		if err := opened.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:207 "resumes a script after a restart without running its finished calls again"
	t.Run("resumes a script after a restart without running its finished calls again", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `const a = await tools.echo({ text: "a" });
			const held = await tools.hold({ id: 1 });
			const b = await tools.echo({ text: "b" });
			return [a, held, b, Date.now() > 0];`)
		text, isError := resultText(t, root)
		expectText(t, text, isError, `["echo a","held 1","echo b",true]`, false)
		if got := f.echoCounts(); !maps.Equal(got, map[string]int{"a": 1, "b": 1}) {
			t.Fatalf("echo runs %v, want a 1 and b 1", got)
		}
		// The VM ran twice; hold, replay-safe, reran once after the restart.
		if starts, holds := f.vm.starts.Load(), f.holds.runs.Load(); starts != 2 || holds != 2 {
			t.Fatalf("%d VM starts and %d hold runs, want 2 and 2", starts, holds)
		}
		if err := opened.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:224 "replays results in the order they arrived, not the order the calls were made"
	t.Run("replays results in the order they arrived, not the order the calls were made", func(t *testing.T) {
		f := codemodeChat(t)
		code := `const order = [];
			await Promise.all([tools.slow({ id: 1 }).then(() => order.push(1)), tools.slow({ id: 2 }).then(() => order.push(2))]);
			await tools.echo({ text: order.join(",") });
			await tools.hold({});
			return order;`
		// 2 finishes first, then 1. Pi's "finished" is also "arrived": the result's callback runs in the next microtask, before
		// the test can release 1. PiG's results arrive through the call's event loop, so 1 is released once 2's result is in
		// the journal (with one processor, 2's tool can return and 1's result still post first).
		arrived := newGate()
		f.onFirstOpen = func(opened harness.Harness) {
			unsubscribe := opened.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
				for _, change := range publication.Changes {
					document, ok := change.(durable.DocumentChange)
					if !ok || document.Record.Kind != "pi.codemode.journal" || document.Value == nil {
						continue
					}
					journal, err := durable.FromJsonValue[journalState](document.Value)
					if err == nil && slices.ContainsFunc(journal.Entries, func(entry journalEntry) bool { return entry.ID == 2 }) {
						arrived.resolve()
					}
				}
			})
			t.Cleanup(unsubscribe)
		}
		f.slowGate(2).resolve()
		go func() {
			if arrived.wait(t.Context()) == nil {
				f.slowGate(1).resolve()
			}
		}()
		opened, root := f.restart(t, code)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "[2,1]", false)
		if got := f.echoCounts(); !maps.Equal(got, map[string]int{"2,1": 1}) {
			t.Fatalf("echo runs %v, want only 2,1 once", got)
		}
		if err := opened.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:240 "journals globals with their results and hands a replay the recorded ones"
	t.Run("journals globals with their results and hands a replay the recorded ones", func(t *testing.T) {
		counter := 0
		f := codemodeChat(t, Global{Name: "next", Execute: func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
			counter++
			return json.RawMessage(jsNumber(float64(counter))), nil
		}})
		opened, root := f.restart(t, `const n = await next(); await tools.hold({}); return [n, await next()];`)
		// The first next() ran once and was replayed from the journal; the second ran after the restart.
		text, isError := resultText(t, root)
		expectText(t, text, isError, "[1,2]", false)
		if counter != 2 {
			t.Fatalf("counter = %d, want 2", counter)
		}
		if err := opened.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:254 "keeps the script's clock and randomness across a restart"
	t.Run("keeps the script's clock and randomness across a restart", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `const before = [Date.now(), Math.random()];
			await tools.hold({});
			await tools.echo({ text: String(before[0]) + "/" + String(before[1]) });
			return "ok";`)
		text, _ := resultText(t, root)
		if text != "ok" {
			t.Fatalf("result = %q, want ok", text)
		}
		// One echo: the same arguments, so the rerun admitted no second call.
		counts := f.echoCounts()
		if len(counts) != 1 {
			t.Fatalf("echo runs %v, want one text once", counts)
		}
		for _, n := range counts {
			if n != 1 {
				t.Fatalf("echo runs %v, want one text once", counts)
			}
		}
		if err := opened.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:269 "ends the call when a resumed script made a different number of calls than before"
	t.Run("ends the call when a resumed script made a different number of calls than before", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `await tools.echo({ text: "a" }); await tools.hold({});`, func(opened harness.Harness, task durable.TaskId) {
			editJournal(t, opened, task, func(journal durable.Draft[journalState]) {
				mustSet(t, journal.Array("entries").Object(0).Set("issuedAfter", 7))
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "after result 1 it had made 2 calls, where it had made 7") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:286 "ends the call at once when a resumed script waits without making a call it made before"
	t.Run("ends the call at once when a resumed script waits without making a call it made before", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `await tools.echo({ text: "a" }); await tools.hold({});`, func(opened harness.Harness, task durable.TaskId) {
			editJournal(t, opened, task, func(journal durable.Draft[journalState]) {
				mustSet(t, journal.Array("entries").Object(0).Set("id", 99))
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "it waits without making call 99") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		// hold, admitted before the restart, resumed on its own; no call was admitted by the replay.
		if got := nestedCount(t, opened); got != 2 {
			t.Fatalf("%d nested calls, want 2", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:305 "ends the call when a resumed script calls a global with other arguments"
	t.Run("ends the call when a resumed script calls a global with other arguments", func(t *testing.T) {
		f := codemodeChat(t, Global{Name: "next", Execute: func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
			return json.RawMessage("1"), nil
		}})
		opened, root := f.restart(t, `await next(); await tools.hold({});`, func(opened harness.Harness, task durable.TaskId) {
			editJournal(t, opened, task, func(journal durable.Draft[journalState]) {
				mustSet(t, journal.Array("entries").Object(0).Object("global").Set("args", "other"))
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "call 1 is next") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:318 "admits no call of a diverging replay, even one the script never made before"
	t.Run("admits no call of a diverging replay, even one the script never made before", func(t *testing.T) {
		f := codemodeChat(t)
		// With k set, the replay makes three extra calls after result 1, which the live run did not make; the count in
		// result 1's acknowledgment shows the divergence only after they were issued.
		opened, root := f.restart(t, `await tools.echo({ text: "a" });
			const extra = load("k") ?? 0;
			for (let i = 0; i < extra; i++) tools.echo({ text: "extra" + i });
			await tools.hold({});`,
			func(opened harness.Harness, task durable.TaskId) {
				editRun(t, opened, task, func(run durable.Draft[runState]) {
					mustSet(t, run.Object("start").Set("store", map[string]any{"k": 3.0}))
				})
			},
			func(opened harness.Harness, task durable.TaskId) {
				eventually(t, func() bool { return issuedAfterIs(t, opened, task, 2) })
			})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "after result 1 it had made 5 calls, where it had made 2") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := f.echoCounts(); !maps.Equal(got, map[string]int{"a": 1}) {
			t.Fatalf("echo runs %v, want only a", got)
		}
		if got := nestedCount(t, opened); got != 2 {
			t.Fatalf("%d nested calls, want 2", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:347 "ends the call when a resumed script ends where it went on before"
	t.Run("ends the call when a resumed script ends where it went on before", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `await tools.echo({ text: "a" }); if (load("k")) return "early"; await tools.hold({});`,
			func(opened harness.Harness, task durable.TaskId) {
				editRun(t, opened, task, func(run durable.Draft[runState]) {
					mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
				})
			},
			// The live run acknowledged result 1 before the restart: it went on after it.
			func(opened harness.Harness, task durable.TaskId) {
				eventually(t, func() bool { return issuedAfterIs(t, opened, task, 2) })
			})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "it ended after result 1, where it went on before") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:370 "ends the call when a resumed script ends before calls the live run made, even without a recorded count"
	t.Run("ends the call when a resumed script ends before calls the live run made, even without a recorded count", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `await tools.echo({ text: "a" }); if (load("k")) return "early"; await tools.echo({ text: "b" }); await tools.hold({});`,
			func(opened harness.Harness, task durable.TaskId) {
				editRun(t, opened, task, func(run durable.Draft[runState]) {
					mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
				})
				// As if the live run crashed before recording result 1's count.
				editJournal(t, opened, task, func(journal durable.Draft[journalState]) {
					journal.Array("entries").Object(0).Delete("issuedAfter")
				})
			})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "it ended after result 1, where it went on before") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:390 "ends the call when a resumed script makes a call an earlier attempt admitted with other arguments"
	t.Run("ends the call when a resumed script makes a call an earlier attempt admitted with other arguments", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `await tools.echo({ text: load("k") ?? "a" }); await tools.hold({});`, func(opened harness.Harness, task durable.TaskId) {
			editRun(t, opened, task, func(run durable.Draft[runState]) {
				mustSet(t, run.Object("start").Set("store", map[string]any{"k": "other"}))
			})
			// As if the restart came before the first journal commit: the rerun runs live from the start.
			editJournal(t, opened, task, func(journal durable.Draft[journalState]) {
				journal.Array("entries").SetLen(0)
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "different path") || !strings.Contains(text, "call 1 is tools.echo") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := f.echoCounts(); !maps.Equal(got, map[string]int{"a": 1}) {
			t.Fatalf("echo runs %v, want only a", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:412 "records a count the live run never recorded as soon as a replay acknowledges it"
	t.Run("records a count the live run never recorded as soon as a replay acknowledges it", func(t *testing.T) {
		f := codemodeChat(t)
		path := filepath.Join(t.TempDir(), "session.sqlite")
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`await tools.echo({ text: "a" }); await tools.hold({});`), done()})
		opened, root := f.setup.open(t, openSqlite(t, path))
		id := submitGo(t, root).Id()
		opened.Resume()
		mustWait(t, f.holds.reached)
		mustClose(t, opened)
		// As if result 1's count was never recorded; hold blocks again after the restart.
		f.holds.reached = newGate()
		opened, _ = f.setup.open(t, openSqlite(t, path))
		task := scriptTask(t, opened)
		editJournal(t, opened, task, func(journal durable.Draft[journalState]) {
			journal.Array("entries").Object(0).Delete("issuedAfter")
		})
		opened.Resume()
		// The replay acknowledges result 1 and goes live; the script waits on hold, and the count is already recorded.
		mustWait(t, f.holds.reached)
		eventually(t, func() bool { return issuedAfterIs(t, opened, task, 2) })
		mustClose(t, opened)
		f.holds.block.Store(false)
		opened, _ = f.setup.open(t, openSqlite(t, path))
		opened.Resume()
		if settled := must(must(opened.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("submission %s, want done", settled.Status)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:441 "lists the script's tool calls in its result's details"
	t.Run("lists the script's tool calls in its result's details", func(t *testing.T) {
		f := codemodeChat(t)
		addTool(t, f.setup.registry, tool("fails", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			isError := true
			result := textResult("permission denied")
			result.IsError = &isError
			return result, nil
		}, durable.ReplayUnsafe))
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`await tools.echo({ text: "a" }); try { await tools.fails({}); } catch {} tools.hold({ id: 9 }); return 1;`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		calls := detailsOf(t, root).Calls
		// A call still running, or not yet launched, when the script ended shows as cancelled.
		want := []NestedCall{
			{ID: 1, Name: "echo", Args: `{"text":"a"}`, Status: "ok"},
			{ID: 2, Name: "fails", Args: "{}", Status: "error", Error: "permission denied"},
			{ID: 3, Name: "hold", Args: `{"id":9}`, Status: "cancelled"},
		}
		if len(calls) != len(want) {
			t.Fatalf("calls = %+v, want %d", calls, len(want))
		}
		for i := range want {
			if calls[i].DurationMs != nil && i != 0 && calls[i].Status == "cancelled" {
				t.Fatalf("a cancelled call has a duration: %+v", calls[i])
			}
			calls[i].DurationMs = nil
			if calls[i] != want[i] {
				t.Fatalf("call %d = %+v, want %+v", i, calls[i], want[i])
			}
		}
		if first := detailsOf(t, root).Calls[0]; first.DurationMs == nil || *first.DurationMs < 0 {
			t.Fatalf("the first call has no duration: %+v", first)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:467 "stores no outcome when the host closes the sandbox, and replays the script after a restart"
	t.Run("stores no outcome when the host closes the sandbox, and replays the script after a restart", func(t *testing.T) {
		f := codemodeChat(t)
		path := filepath.Join(t.TempDir(), "session.sqlite")
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`await tools.echo({ text: "a" }); await tools.hold({}); return "done";`), done()})
		opened, root := f.setup.open(t, openSqlite(t, path))
		id := submitGo(t, root).Id()
		opened.Resume()
		mustWait(t, f.holds.reached)
		task := scriptTask(t, opened)
		// A host shutting down closes the sandbox before the Harness.
		if err := f.sandbox.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if run, err := durable.Snapshot[runState](testContext, opened, RunDoc, task); err != nil || run == nil || run.Result != nil {
			t.Fatalf("run document = %+v (%v), want no result", run, err)
		}
		mustClose(t, opened)
		// The next process has a new sandbox.
		f.holds.block.Store(false)
		f.replaceSandbox(t)
		opened, root = f.setup.open(t, openSqlite(t, path))
		opened.Resume()
		if settled := must(must(opened.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("submission %s, want done", settled.Status)
		}
		text, isError := resultText(t, root)
		expectText(t, text, isError, "done", false)
		if got := f.echoCounts(); !maps.Equal(got, map[string]int{"a": 1}) {
			t.Fatalf("echo runs %v, want a once", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:497 "aborts a call still running when the script ends"
	t.Run("aborts a call still running when the script ends", func(t *testing.T) {
		f := codemodeChat(t)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`tools.hold({}); await tools.echo({ text: "a" }); return 1;`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "1", false)
		var hold *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		for _, task := range toolTasks(t, opened) {
			if input := toolInput(task); input.Kind == harness.ToolTaskNested && input.Call.Name == "hold" {
				hold = &task
			}
		}
		if hold == nil {
			t.Fatal("no nested hold task")
		}
		// Pi: hold ran once. D184 allows 0 here, and only here: the script can end and abort hold before its tool started when
		// hold's goroutine stalls between two steps of its task while echo's runs through (0.15% of runs at 8 CPUs, 0.8% at 3,
		// GOMAXPROCS 2, 6 processes). The task is terminal aborted either way, which the next assertion checks.
		if runs := f.holds.runs.Load(); runs != 0 && runs != 1 {
			t.Fatalf("hold ran %d times, want 0 or 1 (D184)", runs)
		}
		if hold.State.Status != durable.TaskTerminal || hold.State.Outcome == nil || hold.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("hold state = %+v, want terminal aborted", hold.State)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:515 "admits no call once the script has ended, even one it made before ending"
	t.Run("admits no call once the script has ended, even one it made before ending", func(t *testing.T) {
		f := codemodeChat(t)
		f.holds.block.Store(false)
		storage := newHeldStorage()
		// Hold the fingerprint commit, and so the admission of the script's call, until the script has ended.
		f.vm.mu.Lock()
		f.vm.onStart = func(run *cm.Run) {
			held := storage.holdCommits()
			go func() {
				run.Result()
				held.resolve()
			}()
		}
		f.vm.mu.Unlock()
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`tools.echo({ text: "late" }); return 1;`), done()})
		opened, root := f.setup.open(t, storage)
		mustSubmitDone(t, root)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "1", false)
		if got := f.echoCounts(); len(got) != 0 {
			t.Fatalf("echo runs %v, want none", got)
		}
		if got := nestedCount(t, opened); got != 0 {
			t.Fatalf("%d nested calls, want 0", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:533 "checks a rerun's calls against an earlier attempt's before admitting any later call"
	t.Run("checks a rerun's calls against an earlier attempt's before admitting any later call", func(t *testing.T) {
		f := codemodeChat(t)
		// With k set, call 1 has other arguments and call 2 is another tool than before; call 3 would be new.
		opened, root := f.restart(t, `const k = load("k");
			tools.echo({ text: k ?? "a" });
			if (k) tools.echo({ text: "new" });
			await tools.hold({});`, func(opened harness.Harness, task durable.TaskId) {
			editRun(t, opened, task, func(run durable.Draft[runState]) {
				mustSet(t, run.Object("start").Set("store", map[string]any{"k": "x"}))
			})
			// As if the restart came before the first journal commit.
			editJournal(t, opened, task, func(journal durable.Draft[journalState]) { journal.Array("entries").SetLen(0) })
		})
		text, isError := resultText(t, root)
		// The first run already made more calls than before.
		if !isError || !strings.Contains(text, "its first run made 3 calls, where it had made 2") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := nestedCount(t, opened); got != 2 {
			t.Fatalf("%d nested calls, want 2", got)
		}
		if got := f.echoCounts(); !maps.Equal(got, map[string]int{"a": 1}) {
			t.Fatalf("echo runs %v, want only a", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:559 "ends the call when a rerun makes a global call where an earlier attempt made a tool call"
	t.Run("ends the call when a rerun makes a global call where an earlier attempt made a tool call", func(t *testing.T) {
		f := codemodeChat(t, Global{Name: "pick", Execute: constantGlobal("1")})
		opened, root := f.restart(t, `if (load("k")) { await pick(); await tools.echo({ text: "new" }); } else { await tools.hold({}); }`, func(opened harness.Harness, task durable.TaskId) {
			editRun(t, opened, task, func(run durable.Draft[runState]) {
				mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "call 1 is pick(), but was tools.hold({})") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := f.echoCounts(); len(got) != 0 {
			t.Fatalf("echo runs %v, want none", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:577 "ends the call when a rerun's first run makes fewer calls than an earlier attempt admitted"
	t.Run("ends the call when a rerun's first run makes fewer calls than an earlier attempt admitted", func(t *testing.T) {
		// Live, the first run admits hold 1 and hold 2. With k set, it makes only hold 1, and after its result a global and a
		// new tool call: the global's ID is hold 2's key, which no check after the release would see.
		f := codemodeChat(t, Global{Name: "pick", Execute: constantGlobal("1")})
		opened, root := f.restart(t, `const k = load("k");
			const first = tools.hold({ id: 1 });
			if (!k) tools.hold({ id: 2 });
			await first;
			await pick();
			await tools.echo({ text: "new" });`,
			func(opened harness.Harness, task durable.TaskId) {
				editRun(t, opened, task, func(run durable.Draft[runState]) {
					mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
				})
			},
			func(opened harness.Harness, task durable.TaskId) {
				eventually(t, func() bool {
					journal := journalOf(t, opened, task)
					return journal != nil && journal.FirstIssued != nil && *journal.FirstIssued == 2
				})
			})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "its first run made 1 calls, where it had made 2") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := f.echoCounts(); len(got) != 0 {
			t.Fatalf("echo runs %v, want none", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:605 "ends the call when a rerun without journal entries ends in its first run, where it went on before"
	t.Run("ends the call when a rerun without journal entries ends in its first run, where it went on before", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `if (load("k")) { store("x", 1); return "early"; } await tools.hold({});`,
			func(opened harness.Harness, task durable.TaskId) {
				editRun(t, opened, task, func(run durable.Draft[runState]) {
					mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
				})
			},
			func(opened harness.Harness, task durable.TaskId) {
				eventually(t, func() bool {
					journal := journalOf(t, opened, task)
					return journal != nil && journal.FirstIssued != nil && *journal.FirstIssued == 1
				})
			})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "it ended after result 0, where it went on before") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:625 "checks every call a rerun holds before launching any of them"
	t.Run("checks every call a rerun holds before launching any of them", func(t *testing.T) {
		// Live, call 1 is a global that never finishes and call 2 is hold; with k set, call 1 is a new tool call and call 2
		// holds with other arguments. Checking call 1 alone would launch it before call 2's conflict shows.
		f := codemodeChat(t, Global{Name: "forever", Execute: func(ctx context.Context, _ json.RawMessage, _ durable.ToolExecutionApi) (json.RawMessage, error) {
			return json.RawMessage("null"), abortedBy(ctx)
		}})
		opened, root := f.restart(t, `const k = load("k");
			if (k) tools.echo({ text: "new" }); else forever();
			await tools.hold({ id: k ? 2 : 1 });`, func(opened harness.Harness, task durable.TaskId) {
			editRun(t, opened, task, func(run durable.Draft[runState]) {
				mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, `call 2 is tools.hold({"id":2}), but was tools.hold({"id":1})`) {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := f.echoCounts(); len(got) != 0 {
			t.Fatalf("echo runs %v, want none", got)
		}
		if got := nestedCount(t, opened); got != 1 {
			t.Fatalf("%d nested calls, want 1", got)
		}
		mustClose(t, opened)
	})

	// codemode.ts:783 earlier() names the call an earlier attempt made as `tools.${name}(${JSON.stringify(made.arguments)})`,
	// the arguments in the member order the script wrote, not sorted.
	t.Run("names the earlier call with its arguments in the order the script wrote them", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `const k = load("k"); await tools.hold({ zeta: 1, alpha: k ? 2 : 1 });`, func(opened harness.Harness, task durable.TaskId) {
			editRun(t, opened, task, func(run durable.Draft[runState]) {
				mustSet(t, run.Object("start").Set("store", map[string]any{"k": true}))
			})
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, `call 1 is tools.hold({"zeta":1,"alpha":2}), but was tools.hold({"zeta":1,"alpha":1})`) {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:656 "keeps a global's undefined result undefined, live and replayed"
	t.Run("keeps a global's undefined result undefined, live and replayed", func(t *testing.T) {
		f := codemodeChat(t, Global{Name: "nothing", Execute: func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
			return nil, nil
		}})
		opened, root := f.restart(t, `const before = await nothing(); await tools.hold({}); return [before === undefined, (await nothing()) === undefined];`)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "[true,true]", false)
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:666 "rejects a failed tool call with its error text and structured output"
	t.Run("rejects a failed tool call with its error text and structured output", func(t *testing.T) {
		f := codemodeChat(t)
		addTool(t, f.setup.registry, tool("fails", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			isError := true
			result := textResult("permission denied")
			result.IsError = &isError
			return result, nil
		}, durable.ReplayUnsafe))
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`try { await tools.fails({}); } catch (e) { return [e.message, e.structuredOutput]; }`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		text, isError := resultText(t, root)
		expectText(t, text, isError, `["permission denied","permission denied"]`, false)
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:686 "resolves a schema tool's error result that carries its value, and rejects one without with its text"
	t.Run("resolves a schema tool's error result that carries its value, and rejects one without with its text", func(t *testing.T) {
		f := codemodeChat(t)
		exit := map[string]any{"type": "object", "properties": map[string]any{"output": map[string]any{"type": "string"}, "exitCode": map[string]any{"type": "number"}}, "required": []any{"output", "exitCode"}}
		empty := map[string]any{"type": "object", "properties": map[string]any{}}
		isError := true
		addTool(t, f.setup.registry, harness.DefineTool(durable.ToolRegistration{
			ToolSchema:             ai.ToolSchema{Name: "run", Description: "run", Parameters: empty},
			StructuredOutputSchema: exit,
			// Like bash on a nonzero exit: an error result with its value.
			Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "no matches"}}, StructuredOutput: map[string]any{"output": "no matches", "exitCode": 1}, HasStructuredOutput: true, IsError: &isError}, nil
			},
		}))
		addTool(t, f.setup.registry, harness.DefineTool(durable.ToolRegistration{
			ToolSchema:             ai.ToolSchema{Name: "denied", Description: "denied", Parameters: empty},
			StructuredOutputSchema: exit,
			Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "permission denied"}}, IsError: &isError}, nil
			},
		}))
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`const r = await tools.run({});
			let e; try { await tools.denied({}); } catch (error) { e = [error.message, error.structuredOutput ?? null]; }
			return [r.exitCode, r.output, e];`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		text, failed := resultText(t, root)
		expectText(t, text, failed, `[1,"no matches",["permission denied",null]]`, false)
		// Both calls are error rows, also the one whose value the script got (it has no error text: an error diagnostic only comes from a result without a value).
		var rows []string
		for _, row := range detailsOf(t, root).Calls {
			rows = append(rows, row.Name+" "+row.Status+" "+row.Error)
		}
		if want := []string{"run error ", "denied error permission denied"}; !slices.Equal(rows, want) {
			t.Fatalf("rows %q, want %q", rows, want)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:732 "resolves a schema-less tool with its text, as its declaration says, also when it returned images"
	t.Run("resolves a schema-less tool with its text, as its declaration says, also when it returned images", func(t *testing.T) {
		f := codemodeChat(t)
		const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
		addTool(t, f.setup.registry, harness.DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "picture", Description: "picture", Parameters: map[string]any{"type": "object", "properties": map[string]any{"caption": map[string]any{"type": "boolean"}}, "required": []any{"caption"}}},
			Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				var output []ai.ToolResultMessageContent
				if caption, _ := args.(map[string]any)["caption"].(bool); caption {
					output = append(output, ai.TextContent{Text: "a cat"})
				}
				return durable.ToolExecutionResult{Output: append(output, ai.ImageContent{Data: png, MimeType: "image/png"})}, nil
			},
		}))
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`return [await tools.picture({ caption: true }), await tools.picture({ caption: false })];`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		text, failed := resultText(t, root)
		expectText(t, text, failed, `["a cat",""]`, false)
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:760 "delivers a result only after its journal commit"
	t.Run("delivers a result only after its journal commit", func(t *testing.T) {
		storage := newHeldStorage()
		var mu sync.Mutex
		var seen []string
		note := func(name string) {
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
		}
		seenNow := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(seen)
		}
		f := codemodeChat(t,
			Global{Name: "first", Execute: func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
				note("first")
				// The next commit is this result's journal commit: it never lands.
				storage.failNextCommit(errors.New("lost"))
				return json.RawMessage("1"), nil
			}},
			Global{Name: "second", Execute: func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
				note("second")
				return json.RawMessage("2"), nil
			}})
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`await first(); await second();`), done()})
		opened, root := f.setup.open(t, storage)
		submitGo(t, root)
		eventually(t, func() bool { return slices.Contains(seenNow(), "first") })
		time.Sleep(100 * time.Millisecond)
		if got := seenNow(); !slices.Equal(got, []string{"first"}) {
			t.Fatalf("seen %v, want only first", got)
		}
		_ = opened.Close(testContext)
	})

	// Pi e17e2e10 packages/durable/test/codemode.test.ts:671 "drops a global's result that arrives after the script finished"
	t.Run("drops a global's result that arrives after the script finished", func(t *testing.T) {
		// A Go call joins what it started before it returns, so the pending global cannot outlive the call as a JavaScript promise
		// does: it resolves, with a value rather than an error, as the call stops it, once the script has finished.
		returned := make(chan struct{})
		f := codemodeChat(t,
			Global{Name: "slowly", Execute: func(ctx context.Context, _ json.RawMessage, _ durable.ToolExecutionApi) (json.RawMessage, error) {
				<-ctx.Done()
				defer close(returned)
				return json.RawMessage(`"late"`), nil
			}},
			Global{Name: "quickly", Execute: constantGlobal(`"quick"`)})
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`const pending = slowly(); await quickly(); return 1;`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "1", false)
		select {
		case <-returned:
		case <-time.After(15 * time.Second):
			t.Fatal("the pending global never resolved")
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:791 "refuses to resume when the sandbox's fingerprint or the code mode version changed"
	t.Run("refuses to resume when the sandbox's fingerprint or the code mode version changed", func(t *testing.T) {
		for _, field := range []string{"fingerprint", "version"} {
			t.Run(field, func(t *testing.T) {
				f := codemodeChat(t)
				opened, root := f.restart(t, `await tools.hold({}); await tools.echo({ text: "late" });`, func(opened harness.Harness, task durable.TaskId) {
					editRun(t, opened, task, func(run durable.Draft[runState]) {
						if field == "fingerprint" {
							mustSet(t, run.Object("start").Set("fingerprint", "another sandbox"))
						} else {
							mustSet(t, run.Object("start").Set("version", 0))
						}
					})
				})
				text, isError := resultText(t, root)
				if !isError || !strings.Contains(text, "cannot resume") {
					t.Fatalf("result = %q (error %v)", text, isError)
				}
				if got := f.echoCounts(); len(got) != 0 {
					t.Fatalf("echo runs %v, want none", got)
				}
				mustClose(t, opened)
			})
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:812 "times a script out across a restart, without admitting new calls"
	t.Run("times a script out across a restart, without admitting new calls", func(t *testing.T) {
		f := codemodeChat(t)
		opened, root := f.restart(t, `// @options: {"timeout_ms": 300}
			await tools.hold({}); await tools.echo({ text: "late" });`, func(harness.Harness, durable.TaskId) {
			time.Sleep(400 * time.Millisecond)
		})
		text, isError := resultText(t, root)
		if !isError || !strings.Contains(text, "deadline passed") {
			t.Fatalf("result = %q (error %v)", text, isError)
		}
		if got := f.echoCounts(); len(got) != 0 {
			t.Fatalf("echo runs %v, want none", got)
		}
		if starts := f.vm.starts.Load(); starts != 1 {
			t.Fatalf("%d VM starts, want 1", starts)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:827 "returns a decided outcome after a restart without running the script again"
	t.Run("returns a decided outcome after a restart without running the script again", func(t *testing.T) {
		f := codemodeChat(t)
		path := filepath.Join(t.TempDir(), "session.sqlite")
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`return "first"`), done()})
		// Stop after execute() returned, so after the outcome is stored, and before the result commits.
		var blocked atomic.Bool
		blocked.Store(true)
		reached := newGate()
		if err := f.setup.registry.Install(&durable.Extension{Name: "hooks:stop", Hooks: []durable.HookRegistration{{Task: harness.ToolTask.AnyDefinition().Name, Handlers: &harness.ToolHooks{
			AfterTool: func(ctx context.Context, call harness.ToolHookCall, _ durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
				if call.Name != ToolName || !blocked.Load() {
					return nil, nil
				}
				reached.resolve()
				return nil, abortedBy(ctx)
			},
		}}}}); err != nil {
			t.Fatal(err)
		}
		opened, root := f.setup.open(t, openSqlite(t, path))
		id := submitGo(t, root).Id()
		opened.Resume()
		mustWait(t, reached)
		task := scriptTask(t, opened)
		if run, err := durable.Snapshot[runState](testContext, opened, RunDoc, task); err != nil || run == nil || run.Result == nil {
			t.Fatalf("run document = %+v (%v), want a result", run, err)
		}
		mustClose(t, opened)
		blocked.Store(false)
		opened, root = f.setup.open(t, openSqlite(t, path))
		opened.Resume()
		if settled := must(must(opened.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("submission %s, want done", settled.Status)
		}
		text, isError := resultText(t, root)
		expectText(t, text, isError, "first", false)
		if starts := f.vm.starts.Load(); starts != 1 {
			t.Fatalf("%d VM starts, want 1", starts)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:857 "keeps store() values per conversation, as of a fork's entry"
	t.Run("keeps store() values per conversation, as of a fork's entry", func(t *testing.T) {
		f := codemodeChat(t)
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`store("k", 1); return load("k")`), done(), script(`return [load("k") ?? null]`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		if _, err := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")})).Wait(testContext); err != nil {
			t.Fatal(err)
		}
		var firstUser durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "pi.user" {
				firstUser = entry
				break
			}
		}
		if _, err := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("second")})).Wait(testContext); err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, entry := range resultEntries(t, root) {
			texts = append(texts, resultMessageText(entry))
		}
		if !slices.Equal(texts, []string{"1", "[1]"}) {
			t.Fatalf("results %v, want 1 and [1]", texts)
		}
		// A fork at the first input is from before the first script stored anything.
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`return [load("k") ?? null]`), done()})
		fork := must(root.Fork(testContext, firstUser.Id, harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		if _, err := must(fork.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("fork")})).Wait(testContext); err != nil {
			t.Fatal(err)
		}
		forkResults := resultEntries(t, fork)
		if got := resultMessageText(forkResults[len(forkResults)-1]); got != "[null]" {
			t.Fatalf("fork result = %q, want [null]", got)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:882 "journals results that finish together in one commit"
	t.Run("journals results that finish together in one commit", func(t *testing.T) {
		// One processor is where Go's scheduler hands it between the call's loop and its commit ahead of the finished results,
		// so without pump's yield every result commits alone (102 journal commits).
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
		release := newGate()
		var waiting atomic.Int32
		f := codemodeChat(t, Global{Name: "wait", Execute: func(ctx context.Context, _ json.RawMessage, _ durable.ToolExecutionApi) (json.RawMessage, error) {
			if waiting.Add(1) == 50 {
				release.resolve()
			}
			return json.RawMessage("null"), release.wait(ctx)
		}})
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`return (await Promise.all(Array.from({ length: 50 }, () => wait()))).length`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		var journalCommits atomic.Int32
		// The order of the journal and run commits: no commit that changes only the journal lands after the outcome (the run
		// document's last commit).
		var orderMu sync.Mutex
		var lastJournalOnly, lastRun, commits int
		unsubscribe := opened.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			orderMu.Lock()
			defer orderMu.Unlock()
			commits++
			journal, run := false, false
			for _, change := range publication.Changes {
				if document, ok := change.(durable.DocumentChange); ok {
					switch document.Record.Kind {
					case "pi.codemode.journal":
						journalCommits.Add(1)
						journal = true
					case "pi.codemode.run":
						run = true
					}
				}
			}
			if run {
				lastRun = commits
			} else if journal {
				lastJournalOnly = commits
			}
		})
		defer unsubscribe()
		mustSubmitDone(t, root)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "50", false)
		// The journal's creation and a few groups, not one commit per result (51).
		if got := journalCommits.Load(); got >= 10 {
			t.Fatalf("%d journal commits, want fewer than 10", got)
		}
		orderMu.Lock()
		if lastJournalOnly >= lastRun {
			t.Errorf("a journal commit (%d) landed after the outcome commit (%d)", lastJournalOnly, lastRun)
		}
		orderMu.Unlock()
		mustClose(t, opened)
	})

	// PiG addition (no Pi case). Pi passes the arguments as the script gave them and the nested call's validation turns a value that is not an object
	// into an error result. ExecuteTool takes a map, so the call is answered with the same validation message and no nested
	// call is made: a TypeScript dynamic-typing mechanic (a Go map cannot hold another value), agreed with the owner of
	// ExecuteTool.
	t.Run("rejects a tool call whose arguments are not an object without making the call", func(t *testing.T) {
		f := codemodeChat(t)
		f.holds.block.Store(false)
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`try { await tools.echo([1]); } catch (e) { return e.message; }`), done()})
		opened, root := f.setup.open(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")))
		mustSubmitDone(t, root)
		text, isError := resultText(t, root)
		expectText(t, text, isError, "Validation failed for tool \"echo\":\n  - root: must be object\n\nReceived arguments:\n[\n  1\n]", false)
		if got := nestedCount(t, opened); got != 0 {
			t.Fatalf("%d nested calls, want 0", got)
		}
		mustClose(t, opened)
	})
}
