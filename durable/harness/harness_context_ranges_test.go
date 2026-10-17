// Ports the task-runtime context read tests of packages/durable/test/harness-context.test.ts: "extends a task's context
// read with only newer entries", "keeps a conversation's context read across its tasks and for the retention period
// once idle", "derives the same view from extended reads as from whole reads", "keeps returned views and kept ranges
// apart", "freezes kept entries deeply even when storage freezes them shallowly", "does not keep a read that finishes
// after its task ended", "drops an idle conversation's context read on a timer when nothing else runs", and "drops a
// conversation's context read once idle with zero retention".
//
// Go has no frozen objects: a kept range is shared by every read of the conversation, so a view a task handler receives
// is a deep copy, and the two freezing tests assert that a handler's changes to its view never reach a later read.

package harness

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// countingStorage counts the entry rows its scans return. While hold is set, the next range scan (more than one row;
// bounds probes read one row, on the Session line) waits for it.
type countingStorage struct {
	durable.Storage
	scan *scanCount
}

type scanCount struct {
	rows atomic.Int64
	mu   sync.Mutex
	hold chan struct{}
	// held is closed when a scan starts waiting for hold.
	held chan struct{}
}

// holdNext makes the next range scan wait for release; the returned channel is closed once it waits.
func (count *scanCount) holdNext(release chan struct{}) <-chan struct{} {
	count.mu.Lock()
	defer count.mu.Unlock()
	count.hold, count.held = release, make(chan struct{})
	return count.held
}

func (s countingStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	if limit > 1 {
		s.scan.mu.Lock()
		release, held := s.scan.hold, s.scan.held
		s.scan.hold = nil
		s.scan.mu.Unlock()
		if release != nil {
			close(held)
			<-release
		}
	}
	page, err := s.Storage.ScanEntries(ctx, query, limit, cursor)
	s.scan.rows.Add(int64(len(page.Items)))
	return page, err
}

type countingSetup struct {
	opened tkOpened
	scan   *scanCount
	first  durable.EntryRecord
	clock  *testClock
}

// testClock is a Harness clock the test advances.
type testClock struct {
	mu  sync.Mutex
	now float64
}

func (clock *testClock) Now() float64 {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) advance(ms float64) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now += ms
}

// openCounting opens a Harness over counting MemoryStorage with a 21-entry root transcript.
func openCounting(t *testing.T, tasks []durable.AnyTask, settings *HarnessSettings, wall ...bool) countingSetup {
	t.Helper()
	scan := &scanCount{}
	clock := &testClock{now: 1_000}
	options := tkOptions{
		storage:  countingStorage{Storage: storage.NewMemoryStorage(), scan: scan},
		now:      clock.Now,
		settings: func() *HarnessSettings { return settings },
	}
	if len(wall) > 0 && wall[0] {
		options.now = nil
	}
	opened := tkOpenRoot(t, tasks, options)
	appendTo := func(model ai.Message) durable.EntryRecord {
		entry, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{model}})
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	first := appendTo(user("first"))
	for index := range 20 {
		appendTo(assistant("old " + string(rune('a'+index))))
	}
	return countingSetup{opened: opened, scan: scan, first: first, clock: clock}
}

func ctxCommit(ctx context.Context, runtime stepRuntime, change func(tx durable.Tx) error) error {
	return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
		return nil, change(tx)
	})
}

func ctxContextOf(t *testing.T, conversation Conversation, options ...durable.ContextOptions) durable.ContextView {
	t.Helper()
	var option *durable.ContextOptions
	if len(options) > 0 {
		option = &options[0]
	}
	view, err := conversation.Context(testContext, option)
	if err != nil {
		t.Error(err)
	}
	return view
}

func ctxRunTask(t *testing.T, setup countingSetup, task durable.AnyTask) {
	t.Helper()
	id := tkStart(t, setup.opened.root, task)
	setup.opened.harness.Resume()
	outcome := tkWaitOutcome(t, setup.opened.harness, id)
	if outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("task outcome %+v, want completed", outcome)
	}
}

// ctxProbe is a task that reads its conversation's context once, named by its position in names.
func ctxProbe(name string, read func(runtime stepRuntime)) durable.AnyTask {
	return tkOneStep(name, func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		read(runtime)
		return tkComplete(ctx, runtime)
	}).Erased()
}

func TestTaskContextRanges(t *testing.T) {
	t.Run("extends a task's context read with only newer entries", func(t *testing.T) {
		var setup countingSetup
		reads := tkOneStep("test.context-reads", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			root := setup.opened.root
			write := func(draft durable.EntryDraft) {
				if err := ctxCommit(ctx, runtime, func(tx durable.Tx) error {
					_, err := tx.AppendEntry(root.Id(), draft)
					return err
				}); err != nil {
					t.Error(err)
				}
			}
			read := func(at *durable.EntryId) (durable.ContextView, int64) {
				before := setup.scan.rows.Load()
				var options *durable.ContextOptions
				if at != nil {
					options = &durable.ContextOptions{At: at}
				}
				view, err := runtime.Context(ctx, root.Id(), options)
				if err != nil {
					t.Error(err)
				}
				return view, setup.scan.rows.Load() - before
			}
			expectSame := func(what string, got durable.ContextView) {
				if want := ctxContextOf(t, root); !reflect.DeepEqual(got, want) {
					t.Errorf("%s: the task's view differs from the conversation's\n got %s\nwant %s", what, jsonText(t, got), jsonText(t, want))
				}
			}
			initial, _ := read(nil)
			expectSame("initial", initial)

			// Three new entries: the bounds probe reads one row, the extension the three new ones.
			write(durable.EntryDraft{Kind: "message", Model: []ai.Message{user("new")}})
			write(durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "display only"}})
			write(durable.EntryDraft{Kind: "message", Model: []ai.Message{assistant("answer")}})
			extended, rows := read(nil)
			if rows != 4 {
				t.Errorf("the extension scanned %d rows, want 4", rows)
			}
			expectSame("extended", extended)

			// A newer edit of an older entry applies to the extended range.
			write(durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: setup.first.Id, Action: durable.EditOmit}}})
			edited, rows := read(nil)
			if rows != 2 {
				t.Errorf("the edit read scanned %d rows, want 2", rows)
			}
			expectSame("edited", edited)
			if slices.Contains(describeMessages(edited.Messages), "user:first") {
				t.Error("the omitted entry is still in the context")
			}

			// An earlier cutoff reuses the range.
			cutoff, rows := read(new(extended.Entries[len(extended.Entries)-1].Id))
			if rows != 0 {
				t.Errorf("the cutoff read scanned %d rows, want 0", rows)
			}
			if !reflect.DeepEqual(cutoff, extended) {
				t.Error("the cutoff view differs from the extended one")
			}

			// A new head marker changes the range: read it whole.
			write(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("fresh")}})
			reset, _ := read(nil)
			expectSame("reset", reset)
			expectStrings(t, describeMessages(reset.Messages), []string{"user:fresh"})
			return tkComplete(ctx, runtime)
		})
		setup = openCounting(t, []durable.AnyTask{reads.Erased()}, nil)
		ctxRunTask(t, setup, reads.Erased())
		mustClose(t, setup.opened.harness)
	})

	t.Run("keeps a conversation's context read across its tasks and for the retention period once idle", func(t *testing.T) {
		var setup countingSetup
		rows := &syncMap[int64]{}
		read := func(name string, load func() (durable.ContextView, error)) {
			before := setup.scan.rows.Load()
			view, err := load()
			if err != nil {
				t.Error(err)
			}
			rows.put(name, setup.scan.rows.Load()-before)
			if want := ctxContextOf(t, setup.opened.root); !reflect.DeepEqual(view, want) {
				t.Errorf("%s: the task's view differs from the conversation's", name)
			}
		}
		child := tkOneStep("test.context-child", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			if err := ctxCommit(ctx, runtime, func(tx durable.Tx) error {
				_, err := tx.AppendEntry(setup.opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("from child")}})
				return err
			}); err != nil {
				return err
			}
			read("child", func() (durable.ContextView, error) { return runtime.Context(ctx, setup.opened.root.Id(), nil) })
			return tkComplete(ctx, runtime)
		})
		type parentState struct {
			Phase string `json:"phase"`
		}
		type parentRuntime = durable.TaskRuntime[durable.JsonValue, parentState, durable.JsonValue, any]
		type parentRecord = durable.RunningTask[durable.JsonValue, parentState, durable.JsonValue]
		parent := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, parentState, durable.JsonValue, any]{
			Name:    "test.context-parent",
			Version: 1,
			Initial: func(durable.JsonValue) parentState { return parentState{Phase: "start"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, parentState, durable.JsonValue, any]{
				"start": func(ctx context.Context, _ parentRecord, runtime parentRuntime) error {
					read("parent", func() (durable.ContextView, error) { return runtime.Context(ctx, setup.opened.root.Id(), nil) })
					return runtime.Commit(ctx, func(tx durable.Tx, _ parentRecord) (*durable.NextTaskState[parentState, durable.JsonValue], error) {
						id, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: runtime.TaskId()}, ConversationId: new(setup.opened.root.Id())})
						if err != nil {
							return nil, err
						}
						return waitingState[parentState, durable.JsonValue](parentState{Phase: "after"}, []durable.TaskId{id}, durable.JoinAllSettled), nil
					})
				},
				"after": func(ctx context.Context, _ parentRecord, runtime parentRuntime) error {
					read("after", func() (durable.ContextView, error) { return runtime.Context(ctx, setup.opened.root.Id(), nil) })
					return runtime.Commit(ctx, func(durable.Tx, parentRecord) (*durable.NextTaskState[parentState, durable.JsonValue], error) {
						return completed[parentState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ parentRecord, runtime parentRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, parentRecord) (*durable.NextTaskState[parentState, durable.JsonValue], error) {
					return abortedWith[parentState, durable.JsonValue]("test"), nil
				})
			},
		})
		var probeName atomic.Value
		probe := ctxProbe("test.context-probe", func(runtime stepRuntime) {
			read(probeName.Load().(string), func() (durable.ContextView, error) {
				return runtime.Context(context.Background(), setup.opened.root.Id(), nil)
			})
		})
		setup = openCounting(t, []durable.AnyTask{child.Erased(), parent.Erased(), probe}, nil)
		id := tkStart(t, setup.opened.root, parent.Erased())
		setup.opened.harness.Resume()
		tkWaitOutcome(t, setup.opened.harness, id)
		waitForIdle(t, setup.opened.root)
		run := func(name string) {
			probeName.Store(name)
			tkWaitOutcome(t, setup.opened.harness, tkStart(t, setup.opened.root, probe))
			waitForIdle(t, setup.opened.root)
		}
		run("idle")
		// The retention period starts in the pass after the task's terminal state is published.
		scheduler := setup.opened.harness.(*harnessImpl).tasks
		pollUntil(t, func() bool { return scheduler.idleContextCount() == 1 })
		setup.clock.advance(600_000)
		run("expired")
		// Bounds probes read one row each. The child scans its own new entry; the waiting parent's later invocation and
		// the probe within the retention period scan nothing new. After it, the probe reads the whole transcript again.
		got := rows.all()
		want := map[string]int64{"parent": 22, "child": 2, "after": 1, "idle": 1, "expired": 23}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v", got, want)
		}
		mustClose(t, setup.opened.harness)
	})

	t.Run("derives the same view from extended reads as from whole reads", func(t *testing.T) {
		var setup countingSetup
		// Deterministic pseudo-random transcript (32-bit LCG, high bits): calls and results in any order, missing and
		// stray results, excluded stop reasons, system entries, notes, edits of earlier entries, and head markers.
		var seed uint32 = 7
		random := func(n int) int {
			seed = seed*1_664_525 + 1_013_904_223
			return int(uint64(seed) * uint64(n) >> 32)
		}
		seen := map[string]int{}
		steps := tkOneStep("test.context-steps", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			root := setup.opened.root
			var written []durable.EntryRecord
			var calls []string
			next := 0
			label := func(prefix string) string {
				next++
				return prefix + string(rune('0'+(next-1)/100%10)) + string(rune('0'+(next-1)/10%10)) + string(rune('0'+(next-1)%10))
			}
			draft := func() durable.EntryDraft {
				pick := random(14)
				if pick < 3 {
					seen["user"]++
					return durable.EntryDraft{Kind: "message", Model: []ai.Message{user(label("u"))}}
				}
				if pick < 5 {
					ids := make([]string, random(3))
					for index := range ids {
						ids[index] = label("c")
					}
					calls = append(calls, ids...)
					stopReasons := []ai.StopReason{"aborted", "error", "deferred"}
					var stopReason ai.StopReason
					if index := random(9); index < len(stopReasons) {
						stopReason = stopReasons[index]
						seen[string(stopReason)]++
					} else {
						seen["assistant"]++
					}
					return durable.EntryDraft{Kind: "message", Model: []ai.Message{assistant(label("a"), assistantOptions{calls: ids, stopReason: stopReason})}}
				}
				if pick < 8 {
					matched := len(calls) > 0 && random(5) > 0
					var id string
					if matched {
						seen["result"]++
						index := random(len(calls))
						id = calls[index]
						calls = slices.Delete(calls, index, index+1)
					} else {
						seen["stray result"]++
						id = label("stray")
					}
					return durable.EntryDraft{Kind: "message", Model: []ai.Message{toolResult(id)}}
				}
				if pick < 9 {
					seen["system"]++
					return durable.EntryDraft{Kind: "message", Model: []ai.Message{system(ai.PromptSection{Name: "s", Value: new(label("v"))})}}
				}
				if pick < 10 {
					seen["note"]++
					next++
					return durable.EntryDraft{Kind: "note", Data: map[string]any{"n": float64(next - 1)}}
				}
				var target *durable.EntryRecord
				if index := random(len(written)); index < len(written) {
					target = &written[index]
				}
				if pick < 12 && target != nil {
					if random(2) == 0 {
						seen["omit"]++
						return durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: target.Id, Action: durable.EditOmit}}}
					}
					seen["replace"]++
					return durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: target.Id, Action: durable.EditReplace, Messages: []ai.Message{user(label("r"))}}}}
				}
				if pick < 13 && target != nil {
					seen["head"]++
					return durable.EntryDraft{Kind: "summary", Head: new(target.Id), Model: []ai.Message{user(label("h"))}}
				}
				seen["user"]++
				return durable.EntryDraft{Kind: "message", Model: []ai.Message{user(label("u"))}}
			}
			appendTo := func(conversationId durable.ConversationId) {
				err := ctxCommit(ctx, runtime, func(tx durable.Tx) error {
					for range 1 + random(3) {
						entry, err := tx.AppendEntry(conversationId, draft())
						if err != nil {
							return err
						}
						written = append(written, entry)
					}
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}
			same := func(what string, got durable.ContextView, err error, conversation Conversation, options ...durable.ContextOptions) bool {
				if err != nil {
					t.Errorf("%s: %v", what, err)
					return false
				}
				if want := ctxContextOf(t, conversation, options...); !reflect.DeepEqual(got, want) {
					t.Errorf("%s: the extended view differs from the whole read\n got %s\nwant %s", what, jsonText(t, got), jsonText(t, want))
					return false
				}
				return true
			}
			for step := range 150 {
				appendTo(root.Id())
				view, err := runtime.Context(ctx, root.Id(), nil)
				if !same("step "+string(rune('0'+step/100))+string(rune('0'+step/10%10))+string(rune('0'+step%10)), view, err, root) {
					return nil
				}
				if step%10 == 9 {
					// Overlapping reads, one with an earlier cutoff, share the kept range.
					at := written[random(len(written))].Id
					var whole, cut durable.ContextView
					var wholeErr, cutErr error
					var reads sync.WaitGroup
					reads.Go(func() { whole, wholeErr = runtime.Context(ctx, root.Id(), nil) })
					reads.Go(func() { cut, cutErr = runtime.Context(ctx, root.Id(), &durable.ContextOptions{At: &at}) })
					reads.Wait()
					if !same("overlapping whole read", whole, wholeErr, root) || !same("overlapping cut read", cut, cutErr, root, durable.ContextOptions{At: &at}) {
						return nil
					}
				}
			}
			// A fork sees its parent's entries through the fork point and extends with its own.
			fork, err := root.Fork(ctx, written[random(len(written))].Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				return err
			}
			for range 20 {
				view, err := runtime.Context(ctx, fork.Id(), nil)
				if !same("fork", view, err, fork) {
					return nil
				}
				appendTo(fork.Id())
			}
			return tkComplete(ctx, runtime)
		})
		setup = openCounting(t, []durable.AnyTask{steps.Erased()}, nil)
		ctxRunTask(t, setup, steps.Erased())
		for _, category := range []string{"user", "assistant", "aborted", "error", "deferred", "result", "stray result", "system", "note", "omit", "replace", "head"} {
			if seen[category] == 0 {
				t.Errorf("the transcript never had a %s entry", category)
			}
		}
		mustClose(t, setup.opened.harness)
	})

	t.Run("starts the retention period of another, idle conversation's read, and keeps none with zero retention", func(t *testing.T) {
		for _, retention := range []int{600_000, 0} {
			var setup countingSetup
			var forkId atomic.Int64
			rowsOf := &syncMap[int64]{}
			reader := tkOneStep("test.context-other", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				for _, name := range []string{"first", "second"} {
					before := setup.scan.rows.Load()
					if _, err := runtime.Context(ctx, durable.ConversationId(forkId.Load()), nil); err != nil {
						return err
					}
					rowsOf.put(name, setup.scan.rows.Load()-before)
				}
				return tkComplete(ctx, runtime)
			})
			setup = openCounting(t, []durable.AnyTask{reader.Erased()}, &HarnessSettings{ContextRetentionMs: new(float64(retention))})
			fork, err := setup.opened.root.Fork(testContext, setup.first.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				t.Fatal(err)
			}
			forkId.Store(int64(fork.Id()))
			for range 5 {
				if _, err := durable.Commit(testContext, fork, func(tx durable.Tx) (durable.EntryRecord, error) {
					return tx.AppendEntry(fork.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("fork")}})
				}); err != nil {
					t.Fatal(err)
				}
			}
			ctxRunTask(t, setup, reader.Erased())
			want := map[string]int64{"first": 7, "second": 1}
			if retention == 0 {
				want["second"] = 7
			}
			if got := rowsOf.all(); !reflect.DeepEqual(got, want) {
				t.Errorf("retention %d: rows = %v, want %v", retention, got, want)
			}
			mustClose(t, setup.opened.harness)
		}
	})

	// scheduler.ts #contextRetentionMs: a settings source that throws is reported and counts as a retention of 0, so the
	// read still succeeds, an idle conversation's read is not kept, and idle conversations drop their kept reads.
	t.Run("reports a settings source that panics and treats its retention as zero", func(t *testing.T) {
		var panicking atomic.Bool
		failure := errors.New("settings unavailable")
		var forkId atomic.Int64
		var readErrs []error
		var readMu sync.Mutex
		probe := tkOneStep("test.context-settings-panic", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			for _, conversationId := range []durable.ConversationId{runtime.ConversationId(), durable.ConversationId(forkId.Load())} {
				_, err := runtime.Context(ctx, conversationId, nil)
				readMu.Lock()
				readErrs = append(readErrs, err)
				readMu.Unlock()
			}
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{probe.Erased()}, tkOptions{settings: func() *HarnessSettings {
			if panicking.Load() {
				panic(failure)
			}
			return nil
		}})
		var last durable.EntryRecord
		for _, text := range []string{"first", "second"} {
			entry, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user(text)}})
			})
			if err != nil {
				t.Fatal(err)
			}
			last = entry
		}
		fork, err := opened.root.Fork(testContext, last.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			t.Fatal(err)
		}
		forkId.Store(int64(fork.Id()))
		scheduler := opened.harness.(*harnessImpl).tasks
		// With the default retention both reads are kept: the root's once it is idle, the idle fork's at once.
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, probe.Erased()))
		waitForIdle(t, opened.root)
		pollUntil(t, func() bool { return scheduler.idleContextCount() == 2 })

		panicking.Store(true)
		before := len(opened.reports.all())
		readMu.Lock()
		readErrs = nil
		readMu.Unlock()
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, probe.Erased()))
		waitForIdle(t, opened.root)
		pollUntil(t, func() bool { return scheduler.keptContextCount() == 0 })
		readMu.Lock()
		if !reflect.DeepEqual(readErrs, []error{nil, nil}) {
			t.Errorf("context reads with a panicking settings source returned %v, want success", readErrs)
		}
		readMu.Unlock()
		reports := opened.reports.all()[before:]
		if len(reports) == 0 {
			t.Fatal("the panicking settings source was not reported")
		}
		for _, report := range reports {
			if !errors.Is(report, failure) {
				t.Errorf("report %v, want the settings failure", report)
			}
		}
		panicking.Store(false)
		mustClose(t, opened.harness)
	})

	t.Run("keeps returned views and kept ranges apart", func(t *testing.T) {
		var setup countingSetup
		mutate := tkOneStep("test.context-mutate", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			view, err := runtime.Context(ctx, setup.opened.root.Id(), nil)
			if err != nil {
				return err
			}
			view.Messages = view.Messages[:0]
			view.Entries = view.Entries[:len(view.Entries)-1]
			damageView(view)
			again, err := runtime.Context(ctx, setup.opened.root.Id(), nil)
			if err != nil {
				return err
			}
			if want := ctxContextOf(t, setup.opened.root); !reflect.DeepEqual(again, want) {
				t.Error("a handler's changes to its view reached a later read")
			}
			return tkComplete(ctx, runtime)
		})
		setup = openCounting(t, []durable.AnyTask{mutate.Erased()}, nil)
		// A tool call and its result: damageView changes what they reach.
		for _, model := range []ai.Message{
			ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "calling"}, ai.ToolCall{ID: "c1", Name: "lookup", Arguments: ai.JsonObject{"n": 1.0}}}, StopReason: ai.StopReasonToolUse, Timestamp: 2},
			ai.ToolResultMessage{ToolCallID: "c1", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "found"}}, Timestamp: 3},
		} {
			if _, err := durable.Commit(testContext, setup.opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(setup.opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{model}})
			}); err != nil {
				t.Fatal(err)
			}
		}
		ctxRunTask(t, setup, mutate.Erased())
		mustClose(t, setup.opened.harness)
	})

	t.Run("does not keep a read that finishes after its task ended", func(t *testing.T) {
		var setup countingSetup
		scans := make(chan struct{})
		probeStart := make(chan struct{})
		late := make(chan error, 1)
		lateTask := tkOneStep("test.context-late", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			waiting := setup.scan.holdNext(scans)
			go func() {
				_, err := runtime.Context(context.WithoutCancel(ctx), setup.opened.root.Id(), nil)
				late <- err
			}()
			// The read has passed the runtime's ended check, as a synchronous call in the handler has.
			<-waiting
			return tkComplete(ctx, runtime)
		})
		rows := &syncValue[int64]{}
		probe := ctxProbe("test.context-probe", func(runtime stepRuntime) {
			<-probeStart
			before := setup.scan.rows.Load()
			if _, err := runtime.Context(context.Background(), setup.opened.root.Id(), nil); err != nil {
				t.Error(err)
			}
			rows.put(setup.scan.rows.Load() - before)
		})
		setup = openCounting(t, []durable.AnyTask{lateTask.Erased(), probe}, &HarnessSettings{ContextRetentionMs: new(float64(0))})
		lateId := tkStart(t, setup.opened.root, lateTask.Erased())
		setup.opened.harness.Resume()
		tkWaitOutcome(t, setup.opened.harness, lateId)
		// The task's invocation ends when its handler returns, just after the outcome is published.
		scheduler := setup.opened.harness.(*harnessImpl).tasks
		pollUntil(t, func() bool { return scheduler.invocationCount() == 0 })
		// The conversation is busy again when the late read finishes; its range must not be kept.
		probeId := tkStart(t, setup.opened.root, probe)
		close(scans)
		if err := <-late; err != nil {
			t.Fatalf("the late read failed: %v", err)
		}
		close(probeStart)
		tkWaitOutcome(t, setup.opened.harness, probeId)
		if got, _ := rows.get(); got != 22 {
			t.Errorf("the probe scanned %d rows, want 22", got)
		}
		mustClose(t, setup.opened.harness)
	})

	t.Run("drops an idle conversation's context read on a timer when nothing else runs", func(t *testing.T) {
		rowsOf := &syncMap[int64]{}
		var setup countingSetup
		var name atomic.Value
		probe := ctxProbe("test.context-probe", func(runtime stepRuntime) {
			before := setup.scan.rows.Load()
			if _, err := runtime.Context(context.Background(), setup.opened.root.Id(), nil); err != nil {
				t.Error(err)
			}
			rowsOf.put(name.Load().(string), setup.scan.rows.Load()-before)
		})
		// The Harness clock is the wall clock: the timer runs against it.
		setup = openCounting(t, []durable.AnyTask{probe}, &HarnessSettings{ContextRetentionMs: new(float64(500))}, true)
		scheduler := setup.opened.harness.(*harnessImpl).tasks
		run := func(which string) {
			name.Store(which)
			tkWaitOutcome(t, setup.opened.harness, tkStart(t, setup.opened.root, probe))
			waitForIdle(t, setup.opened.root)
		}
		if scheduler.expiryPending() {
			t.Fatal("a timer is pending before any read")
		}
		run("first")
		// The conversation turns idle when the task's terminal state is published; the retention period and its timer
		// start in the pass that follows.
		pollUntil(t, func() bool { return scheduler.keptContextCount() == 1 && scheduler.expiryPending() })
		pollUntil(t, func() bool { return scheduler.keptContextCount() == 0 && !scheduler.expiryPending() })
		run("second")
		if got, want := rowsOf.all(), map[string]int64{"first": 22, "second": 22}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v", got, want)
		}
		mustClose(t, setup.opened.harness)
		if scheduler.keptContextCount() != 0 || scheduler.expiryPending() {
			t.Error("close left a kept context or a pending timer")
		}
	})

	t.Run("drops a conversation's context read once idle with zero retention", func(t *testing.T) {
		rowsOf := &syncMap[int64]{}
		var setup countingSetup
		var name atomic.Value
		probe := ctxProbe("test.context-probe", func(runtime stepRuntime) {
			before := setup.scan.rows.Load()
			if _, err := runtime.Context(context.Background(), setup.opened.root.Id(), nil); err != nil {
				t.Error(err)
			}
			rowsOf.put(name.Load().(string), setup.scan.rows.Load()-before)
		})
		setup = openCounting(t, []durable.AnyTask{probe}, &HarnessSettings{ContextRetentionMs: new(float64(0))})
		for _, which := range []string{"first", "second"} {
			name.Store(which)
			tkWaitOutcome(t, setup.opened.harness, tkStart(t, setup.opened.root, probe))
			waitForIdle(t, setup.opened.root)
		}
		if got, want := rowsOf.all(), map[string]int64{"first": 22, "second": 22}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v", got, want)
		}
		mustClose(t, setup.opened.harness)
	})
}

func waitForIdle(t *testing.T, conversation Conversation) {
	t.Helper()
	if err := conversation.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
}

// pollUntil waits up to ten seconds for check; unlike eventually it has room for a real timer.
func pollUntil(t *testing.T, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if check() {
			return
		}
	}
	t.Fatal("condition was not reached")
}

// syncMap collects values by name from task handlers.
type syncMap[T any] struct {
	mu     sync.Mutex
	values map[string]T
}

func (m *syncMap[T]) put(name string, value T) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]T{}
	}
	m.values[name] = value
}

func (m *syncMap[T]) all() map[string]T {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.values)
}

// keptContextCount is how many conversations keep a context range.
func (s *TaskScheduler) keptContextCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.contexts)
}

// expiryPending reports whether the idle-context timer is pending.
func (s *TaskScheduler) expiryPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiry != nil
}

// invocationCount is how many task invocations are running.
func (s *TaskScheduler) invocationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.invocations)
}

// idleContextCount is how many kept contexts have started their retention period.
func (s *TaskScheduler) idleContextCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, kept := range s.contexts {
		if kept.idleSince != nil {
			count++
		}
	}
	return count
}

// extendRange derives a range again, instead of extending it, when an added entry is the target of an edit the range
// holds or carries an edit or a head marker itself (context.ts extendRange).
func TestExtendRangeDerivesAgainWhenAnAddedEntryChangesWhatIsDerived(t *testing.T) {
	entry := func(id durable.EntryId, text string) durable.EntryRecord {
		return durable.EntryRecord{Id: id, Kind: "message", Model: []ai.Message{user(text)}}
	}
	edit := durable.EntryRecord{Id: 2, Kind: "edit", Edits: []durable.ContextEdit{{Target: 3, Action: durable.EditReplace, Messages: []ai.Message{user("edited")}}}}
	previous := deriveRange(ContextBounds{Tail: 2}, []durable.EntryRecord{entry(1, "one"), edit})
	summary := durable.EntryRecord{Id: 4, Kind: "summary", Head: new(durable.EntryId(1)), Model: []ai.Message{user("summary")}}
	for name, c := range map[string]struct {
		added durable.EntryRecord
		head  *durable.EntryRecord
	}{
		"the target of an earlier edit": {entry(3, "three"), nil},
		"an edit":                       {durable.EntryRecord{Id: 3, Kind: "edit", Edits: []durable.ContextEdit{{Target: 1, Action: durable.EditOmit}}}, nil},
		"a head marker":                 {summary, &summary},
	} {
		bounds := ContextBounds{Head: c.head, Tail: c.added.Id}
		got := extendRange(previous, bounds, []durable.EntryRecord{c.added})
		want := deriveRange(bounds, []durable.EntryRecord{entry(1, "one"), edit, c.added})
		if !reflect.DeepEqual(got.view, want.view) {
			t.Errorf("%s: extended view %s, want the whole derivation %s", name, jsonText(t, got.view), jsonText(t, want.view))
		}
	}
}
