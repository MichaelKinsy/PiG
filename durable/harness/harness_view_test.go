// Ports packages/durable/test/harness-view.test.ts.
//
// Go mappings forced by language mechanics. An entry record is a value, so the case that entries which stay in a window keep their
// object identity has no Go counterpart; a WeakRef is a weak.Pointer and gc() is runtime.GC.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type viewFrame struct {
	value ConversationView
	ops   []durable.Op
}

var viewMounted = []string{"pi.agent", "pi.inbox", "pi.live", "pi.provider", "pi.usage"}

type recording struct {
	initial ConversationView
	mu      sync.Mutex
	frames  []viewFrame
	watch   ConversationWatch
}

func (r *recording) snapshot() []viewFrame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.frames)
}

func (r *recording) stop(t *testing.T) {
	t.Helper()
	if _, err := r.watch.Stop(); err != nil {
		t.Fatal(err)
	}
}

// record starts a watch of conversation that records its acquisition revision and every delivered frame (harness-view.test.ts:24).
func record(t *testing.T, conversation Conversation, window ...*ConversationWindow) *recording {
	t.Helper()
	watch := must(conversation.Watch(testContext, windowArg(window)))
	recorded := &recording{initial: watch.Value(), watch: watch}
	watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
		value := change.Value
		ops := change.Ops
		recorded.mu.Lock()
		defer recorded.mu.Unlock()
		recorded.frames = append(recorded.frames, viewFrame{value: value, ops: ops})
		return nil
	})
	return recorded
}

// fresh returns a freshly built view of conversation, for comparing with an advanced one (harness-view.test.ts:36).
func fresh(t *testing.T, conversation Conversation, window ...*ConversationWindow) ConversationView {
	t.Helper()
	watch := must(conversation.Watch(testContext, windowArg(window)))
	value := watch.Value()
	must(watch.Stop())
	return value
}

// windowArg is the optional window argument of a helper: nil takes the defaults.
func windowArg(window []*ConversationWindow) *ConversationWindow {
	if len(window) > 0 {
		return window[0]
	}
	return nil
}

// committed is the view as committed state defines it, read without any mount: the newest size entries, the entry before them, and the
// built-in documents (harness-view.test.ts committed).
func committed(t *testing.T, harness Harness, conversation Conversation, record durable.ConversationRecord, size int) ConversationView {
	t.Helper()
	docs := ViewDocs{}
	for _, token := range []durable.AnyDocToken{AgentDoc, LiveDoc, InboxDoc, ProviderDoc, UsageDoc} {
		value := must(harness.SnapshotErased(testContext, token, conversation.Id()))
		if value != nil {
			docs = ViewDocsOf(docs, token.AnyDefinition().Kind, value)
		}
	}
	newest := slices.Clone(must(conversation.Entries(testContext, durable.EntryQuery{}, size+1, nil)).Items)
	var before *durable.EntryId
	if len(newest) > size {
		before = new(newest[size].Id)
		newest = newest[:size]
	}
	slices.Reverse(newest)
	return ConversationView{Conversation: record, Entries: newest, Before: before, Docs: docs}
}

// jsonOf is value's JSON form, the form frame operations apply to.
func jsonOf(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// orderedJSONOf is value's JSON form with objects as ordered *delta.JsonObject, as JSON.parse gives a replica.
func orderedJSONOf(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := delta.DecodeJson(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func expectSameJSON(t *testing.T, got, want any) {
	t.Helper()
	if left, right := jsonOf(t, got), jsonOf(t, want); !reflect.DeepEqual(left, right) {
		t.Fatalf("got  %v\nwant %v", left, right)
	}
}

// replay applies every frame's operations from initial, checking each delivered revision on the way (harness-view.test.ts:54).
func replay(t *testing.T, initial ConversationView, frames []viewFrame) any {
	t.Helper()
	value := orderedJSONOf(t, initial)
	for _, frame := range frames {
		ops := orderedJSONOf(t, frame.ops).([]any)
		converted := make([]durable.Op, len(ops))
		for i, op := range ops {
			converted[i] = op.([]any)
		}
		next, err := delta.ApplyImmutable(value, converted)
		if err != nil {
			t.Fatal(err)
		}
		value = next
		if want := jsonOf(t, frame.value); !reflect.DeepEqual(jsonOf(t, value), want) {
			t.Fatalf("replayed %v\ndelivered %v", value, want)
		}
	}
	return value
}

// drained lets watch callbacks, which run after the commit, catch up: a line job queued now runs after every commit already on the line has published, and the deliveries those publications scheduled then drain.
func drained(harness Harness) {
	impl := harness.(*harnessImpl)
	_, _ = impl.ReadOnLine(func() (any, error) { return nil, nil })
	impl.WaitDeliveries()
}

// sameEntries is upstream's toBe over entry arrays: the same backing array and length.
func sameEntries(a, b []durable.EntryRecord) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

type touchCounter struct {
	mu    sync.Mutex
	count int
}

func (c *touchCounter) value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// touches counts the commits that touch conversation's mounted state (harness-view.test.ts:70).
func touches(harness Harness, conversation Conversation) *touchCounter {
	counter := &touchCounter{}
	harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		touched := slices.ContainsFunc(publication.Changes, func(change durable.CommitChange) bool {
			switch typed := change.(type) {
			case durable.EntryWrite:
				return typed.Value.ConversationId == conversation.Id()
			case durable.DocumentChange:
				return typed.ConversationId != nil && *typed.ConversationId == conversation.Id() && slices.Contains(viewMounted, typed.Record.Kind) && len(typed.Ops) > 0
			}
			return false
		})
		if touched {
			counter.mu.Lock()
			counter.count++
			counter.mu.Unlock()
		}
	})
	return counter
}

// lastView is the newest revision of a recording: its last frame's value, or the acquisition revision before any frame.
func lastView(frames []viewFrame, initial ConversationView) ConversationView {
	if len(frames) == 0 {
		return initial
	}
	return frames[len(frames)-1].value
}

func noteEntry(t *testing.T, conversation Conversation, kind string, head ...durable.EntryId) durable.EntryRecord {
	t.Helper()
	return commitValue(t, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
		draft := durable.EntryDraft{Kind: kind}
		if len(head) > 0 {
			draft.Head = &head[0]
		}
		return tx.AppendEntry(conversation.Id(), draft)
	})
}

func TestConversationView(t *testing.T) {
	t.Run("hydrates the newest entries and the built-in documents", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:89
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("hello")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		must(submission.Wait(testContext))
		view := fresh(t, root)
		expectSameJSON(t, view.Conversation, map[string]any{"id": float64(root.Id())})
		expectSameJSON(t, view.Entries, allEntries(t, root))
		if view.Before != nil {
			t.Fatalf("before = %d, want none", *view.Before)
		}
		keys := view.Docs.Keys()
		// Pi's docs record takes the mounted documents in mount order (view.ts:48-54, 161-166); its test sorts the keys (:97) to compare
		// the set, and the unsorted keys are that order.
		expectStrings(t, keys, []string{"pi.agent", "pi.live", "pi.inbox", "pi.provider", "pi.usage"})
		slices.Sort(keys)
		expectStrings(t, keys, viewMounted)
		expectSameJSON(t, viewDoc(view, "pi.live"), map[string]any{})
		expectSameJSON(t, viewDoc(view, "pi.inbox"), map[string]any{"items": []any{}})
		closeHarness(t, harness)
	})

	t.Run("publishes one frame per touching commit, whose operations rebuild every revision", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:103
		setup := chatSetup(t)
		release := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(release, "a longer answer")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		recorded := record(t, root)
		touching := touches(harness, root)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		waitFor(t, func() bool {
			return slices.ContainsFunc(recorded.snapshot(), func(frame viewFrame) bool {
				_, ok := viewDoc(frame.value, "pi.live").Get("generation")
				return ok
			})
		})
		release.resolve()
		must(submission.Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		drained(harness)
		frames := recorded.snapshot()
		if len(frames) != touching.value() {
			t.Fatalf("%d frames, %d touching commits", len(frames), touching.value())
		}
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation, len(lastView(frames, recorded.initial).Entries)))
		containsOp := func(ops []durable.Op, match func(op any) bool) bool {
			return slices.ContainsFunc(jsonOf(t, ops).([]any), match)
		}
		if !containsOp(frames[0].ops, func(op any) bool {
			values := op.([]any)
			if len(values) != 5 || values[0] != "p" || !reflect.DeepEqual(values[1], []any{"entries"}) || values[2] != float64(0) || values[3] != float64(0) {
				return false
			}
			inserted := values[4].([]any)
			return len(inserted) == 1 && inserted[0].(map[string]any)["kind"] == "pi.user"
		}) {
			t.Fatalf("first frame %v has no user entry splice", frames[0].ops)
		}
		// Document operations keep their exact shape under the mount path.
		var all []durable.Op
		for _, frame := range frames {
			all = append(all, frame.ops...)
		}
		want := []any{"s", []any{"docs", "pi.live", "generation"}, map[string]any{"attempt": float64(1)}}
		if !containsOp(all, func(op any) bool { return reflect.DeepEqual(op, want) }) {
			t.Fatalf("ops %v lack %v", jsonOf(t, all), want)
		}
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("shares unchanged parts between revisions and skips commits that touch nothing mounted", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:133
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		recorded := record(t, root)
		noteEntry(t, other, "note")
		if err := root.Configure(testContext, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel(ai.ThinkingHigh))}); err != nil {
			t.Fatal(err)
		}
		noteEntry(t, root, "note")
		drained(harness)
		frames := recorded.snapshot()
		if len(frames) != 2 {
			t.Fatalf("%d frames", len(frames))
		}
		expectSameJSON(t, frames[0].ops, []any{[]any{"s", []any{"docs", "pi.agent", "thinkingLevel"}, "high"}})
		// Pi applies the frame's operations to the docs object in place (view.ts:199-216), so an update keeps the mount order.
		expectStrings(t, frames[0].value.Docs.Keys(), []string{"pi.agent", "pi.live", "pi.inbox", "pi.provider", "pi.usage"})
		if !sameEntries(frames[0].value.Entries, recorded.initial.Entries) {
			t.Fatal("entries were not shared")
		}
		if viewDoc(frames[0].value, "pi.live") != viewDoc(recorded.initial, "pi.live") {
			t.Fatal("pi.live was not shared")
		}
		if frames[1].value.Docs != frames[0].value.Docs {
			t.Fatal("docs were not shared")
		}
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("keeps head markers in place: a summary and a reset are appended entries", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:171
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		noteEntry(t, root, "a")
		b := noteEntry(t, root, "b")
		noteEntry(t, root, "c")
		recorded := record(t, root)
		// Each awaited commit of Pi's test lets the delivery microtask finish before the next one; a commit made while a call is in flight is folded into the next call.
		summary := noteEntry(t, root, "summary", b.Id)
		drained(harness)
		noteEntry(t, root, "d")
		drained(harness)
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		drained(harness)
		frames := recorded.snapshot()
		var kinds [][]string
		for _, frame := range frames {
			kinds = append(kinds, entryKinds(frame.value.Entries))
		}
		expectSameJSON(t, kinds, [][]string{{"a", "b", "c", "summary"}, {"a", "b", "c", "summary", "d"}, {"a", "b", "c", "summary", "d", "pi.reset"}})
		expectSameJSON(t, frames[0].ops, []any{[]any{"p", []any{"entries"}, 3, 0, []any{summary}}})
		// The summary's head tells a renderer where the entries it summarizes end.
		if head := frames[0].value.Entries[3].Head; head == nil || *head != b.Id {
			t.Fatalf("summary head = %v, want %d", head, b.Id)
		}
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation, 6))
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("shows a fork's inherited entries in ID order, its head markers in place", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:190
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		a := noteEntry(t, root, "a")
		b := noteEntry(t, root, "b")
		fork := must(root.Fork(testContext, b.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		recorded := record(t, fork)
		noteEntry(t, fork, "summary", b.Id)
		drained(harness)
		var ids []durable.EntryId
		for _, entry := range recorded.initial.Entries {
			ids = append(ids, entry.Id)
		}
		expectIds(t, ids, []durable.EntryId{a.Id, b.Id})
		frames := recorded.snapshot()
		expectStrings(t, entryKinds(frames[0].value.Entries), []string{"a", "b", "summary"})
		expectSameJSON(t, frames[0].value, committed(t, harness, fork, recorded.initial.Conversation, 3))
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("shows a fork's inherited entries and follows only the fork's own commits", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:199
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		first := noteEntry(t, root, "first")
		fork := must(root.Fork(testContext, first.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		recorded := record(t, fork)
		expectStrings(t, entryKinds(recorded.initial.Entries), []string{"first"})
		expectSameJSON(t, recorded.initial.Conversation.Parent, map[string]any{"conversationId": float64(root.Id()), "at": float64(first.Id)})
		noteEntry(t, root, "parent")
		noteEntry(t, fork, "child")
		drained(harness)
		var kinds [][]string
		for _, frame := range recorded.snapshot() {
			kinds = append(kinds, entryKinds(frame.value.Entries))
		}
		expectSameJSON(t, kinds, [][]string{{"first", "child"}})
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("unmounts a retired document and mounts its recreation whole", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:214
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		recorded := record(t, root)
		commitValue(t, root, func(tx durable.Tx) (any, error) { return nil, tx.RetireDoc(LiveDoc, root.Id()) })
		commitValue(t, root, func(tx durable.Tx) (any, error) {
			live, err := durable.TxDoc(tx, LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			return nil, live.Set("tools", []any{})
		})
		drained(harness)
		frames := recorded.snapshot()
		var ops [][]durable.Op
		for _, frame := range frames {
			ops = append(ops, frame.ops)
		}
		expectSameJSON(t, ops, []any{
			[]any{[]any{"d", []any{"docs", "pi.live"}}},
			[]any{[]any{"s", []any{"docs", "pi.live"}, map[string]any{"tools": []any{}}}},
		})
		if frames[0].value.Docs.Has("pi.live") {
			t.Fatal("pi.live is still mounted")
		}
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("folds undelivered commits into one call whose ops reach the newest view", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:231
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		watch := must(root.Watch(testContext, nil))
		for range 101 {
			noteEntry(t, root, "note")
		}
		var mu sync.Mutex
		var frames []viewFrame
		watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
			value := change.Value
			ops := change.Ops
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, viewFrame{value: value, ops: ops})
			return nil
		})
		drained(harness)
		mu.Lock()
		got := slices.Clone(frames)
		mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("%d frames", len(got))
		}
		if len(got[0].ops) == 0 || durable.Op(got[0].ops[0])[0] == "r" {
			t.Fatalf("ops %v, want folded ops, not a root replacement", got[0].ops)
		}
		// The default window took 100 appends, then dropped its oldest 51 at the 101st, down to 50.
		expectSameJSON(t, got[0].value, committed(t, harness, root, got[0].value.Conversation, 50))
		must(watch.Stop())
		closeHarness(t, harness)
	})

	t.Run("keeps watches of one conversation independent and remounts after the last one detaches", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:249
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		kept := record(t, root)
		recorded := record(t, root)
		noteEntry(t, root, "one")
		drained(harness)
		keptFrames := func() []viewFrame { return kept.snapshot() }
		expectStrings(t, entryKinds(keptFrames()[len(keptFrames())-1].value.Entries), []string{"one"})
		recorded.stop(t)
		if frames := recorded.snapshot(); len(frames) != 1 {
			t.Fatalf("%d frames", len(frames))
		}
		noteEntry(t, root, "two")
		drained(harness)
		last := keptFrames()[len(keptFrames())-1].value
		expectStrings(t, entryKinds(last.Entries), []string{"one", "two"})
		kept.stop(t)
		// No observer is left, so the mount was dropped: a new observer builds a new revision from committed state.
		rebuilt := fresh(t, root)
		expectSameJSON(t, rebuilt, last)
		if sameEntries(rebuilt.Entries, last.Entries) || rebuilt.Docs == last.Docs {
			t.Fatal("rebuilt view shares the dropped mount's revision")
		}
		closeHarness(t, harness)
	})

	t.Run("ends watches at close and rejects later acquisition", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:270
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		watch := must(root.Watch(testContext, nil))
		closeHarness(t, harness)
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchEnded || end.Error != nil {
			t.Fatalf("end %+v", end)
		}
		if entries := watch.Value().Entries; len(entries) != 0 {
			t.Fatalf("entries %v", entries)
		}
		if _, err := root.Watch(testContext, nil); err == nil {
			t.Fatal("watch after close")
		}
	})

	t.Run("rejects an acquisition cancelled or closed while it waits for the Session line", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:281
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		hold := func() (func(), <-chan error) {
			release := deferred()
			blocking := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				_, err := root.Commit(testContext, func(durable.Tx) (any, error) {
					close(started)
					return nil, release.wait(context.Background())
				})
				blocking <- err
			}()
			<-started
			return release.resolve, blocking
		}
		release, blocking := hold()
		cancelled, cancel := cancelledContext(errors.New("cancelled"))
		var acquiring *future[struct{}]
		queueOnLine(t, harness, func() <-chan struct{} {
			acquiring = asyncErr(func() error {
				_, err := root.Watch(cancelled, nil)
				return err
			})
			return acquiring.done
		})
		cancel()
		release()
		if err := <-blocking; err != nil {
			t.Fatal(err)
		}
		_, err := acquiring.wait()
		expectError(t, err, "cancelled")

		release, blocking = hold()
		var closedWhileQueued *future[struct{}]
		queueOnLine(t, harness, func() <-chan struct{} {
			closedWhileQueued = asyncErr(func() error {
				_, err := root.Watch(testContext, nil)
				return err
			})
			return closedWhileQueued.done
		})
		closing := asyncErr(func() error { return harness.Close(testContext) })
		release()
		if err := <-blocking; err != nil {
			t.Fatal(err)
		}
		_, err = closedWhileQueued.wait()
		expectError(t, err, "Harness is closed")
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("shares one mount between concurrent observers and isolates a failing listener", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:307
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		failing := must(root.Watch(testContext, nil))
		recorded := record(t, root)
		shared := must(root.Watch(testContext, nil))
		// Each observer has its own window; the documents are the mount's.
		expectSameJSON(t, failing.Value(), shared.Value())
		if failing.Value().Docs != shared.Value().Docs {
			t.Fatal("observers do not share the mount's documents")
		}
		must(shared.Stop())
		failing.Start(func(context.Context, chord.WatchChange[ConversationView]) error { return errors.New("listener failed") })
		noteEntry(t, root, "one")
		noteEntry(t, root, "two")
		drained(harness)
		<-failing.Closed()
		if end := failing.End(); end.Reason != durable.WatchListenerError {
			t.Fatalf("end %+v", end)
		}
		if frames := recorded.snapshot(); len(frames) != 2 {
			t.Fatalf("%d frames", len(frames))
		}
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("publishes one frame for a commit that appends several entries and edits a document", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:326
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		type other struct {
			N int `json:"n"`
		}
		Other := durable.DefineDoc(durable.DocDefinition[other]{
			CommonDocDefinition: durable.CommonDocDefinition[other]{Kind: "app.other", Version: 1, Initial: func() other { return other{} }, CheckpointWhen: func(other, []durable.Op, durable.CheckpointInfo) bool { return false }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
		})
		recorded := record(t, root)
		commitValue(t, root, func(tx durable.Tx) (any, error) {
			if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "a"}); err != nil {
				return nil, err
			}
			live, err := durable.TxDoc(tx, LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			if err := live.Set("tools", []any{}); err != nil {
				return nil, err
			}
			_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "b"})
			return nil, err
		})
		// A document that is not mounted publishes nothing.
		commitValue(t, root, func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc(tx, Other, root.Id())
			if err != nil {
				return nil, err
			}
			return nil, draft.Set("n", 1)
		})
		drained(harness)
		frames := recorded.snapshot()
		if len(frames) != 1 {
			t.Fatalf("%d frames", len(frames))
		}
		expectStrings(t, entryKinds(frames[0].value.Entries), []string{"a", "b"})
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation, len(lastView(frames, recorded.initial).Entries)))
		recorded.stop(t)
		closeHarness(t, harness)
	})
}

// notes appends one entry of kind per commit, with its index as data, and returns the entries.
func notes(t *testing.T, conversation Conversation, count int, kind ...string) []durable.EntryRecord {
	t.Helper()
	entryKind := "note"
	if len(kind) > 0 {
		entryKind = kind[0]
	}
	entries := make([]durable.EntryRecord, 0, count)
	for index := range count {
		entries = append(entries, commitValue(t, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: entryKind, Data: index})
		}))
	}
	return entries
}

func entryIdsOf(entries []durable.EntryRecord) []durable.EntryId {
	ids := make([]durable.EntryId, len(entries))
	for index, entry := range entries {
		ids[index] = entry.Id
	}
	return ids
}

func expectBefore(t *testing.T, view ConversationView, want durable.EntryId) {
	t.Helper()
	if view.Before == nil || *view.Before != want {
		t.Fatalf("before = %v, want %d", view.Before, want)
	}
}

func TestConversationViewWindow(t *testing.T) {
	t.Run("starts with the newest `min` entries and the entry before them, or the whole short transcript", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:376
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		written := notes(t, root, 10)
		view := fresh(t, root, &ConversationWindow{Min: new(4)})
		expectIds(t, entryIdsOf(view.Entries), entryIdsOf(written[6:]))
		expectBefore(t, view, written[5].Id)
		// The default window: the newest 50, here all ten, starting the transcript.
		all := fresh(t, root)
		expectIds(t, entryIdsOf(all.Entries), entryIdsOf(written))
		if all.Before != nil {
			t.Fatalf("before = %d, want none", *all.Before)
		}
		if _, present := jsonOf(t, all).(map[string]any)["before"]; present {
			t.Fatal("a window that starts the transcript carries a before member")
		}
		closeHarness(t, harness)
	})

	t.Run("takes appends up to `max`, then drops its oldest in one splice down to `min`", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:390
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		written := notes(t, root, 4)
		recorded := record(t, root, &ConversationWindow{Min: new(3), Max: new(5)})
		expectIds(t, entryIdsOf(recorded.initial.Entries), entryIdsOf(written[1:]))
		appended := notes(t, root, 3)
		drained(harness)
		frames := recorded.snapshot()
		var sizes []int
		for _, frame := range frames {
			sizes = append(sizes, len(frame.value.Entries))
		}
		expectSameJSON(t, sizes, []int{4, 5, 3})
		e5, e6, e7 := appended[0], appended[1], appended[2]
		expectSameJSON(t, frames[0].ops, []any{[]any{"p", []any{"entries"}, 3, 0, []any{e5}}})
		expectSameJSON(t, frames[2].ops, []any{
			[]any{"p", []any{"entries"}, 5, 0, []any{e7}},
			[]any{"p", []any{"entries"}, 0, 3, []any{}},
			[]any{"s", []any{"before"}, written[3].Id},
		})
		expectIds(t, entryIdsOf(frames[2].value.Entries), []durable.EntryId{e5.Id, e6.Id, e7.Id})
		expectBefore(t, frames[2].value, written[3].Id)
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation, 3))
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("keeps the newest `min` of a commit that appends more than `max` at once", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:418
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		notes(t, root, 2)
		recorded := record(t, root, &ConversationWindow{Min: new(2), Max: new(3)})
		burst := commitValue(t, root, func(tx durable.Tx) ([]durable.EntryRecord, error) {
			var entries []durable.EntryRecord
			for range 5 {
				entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "burst"})
				if err != nil {
					return nil, err
				}
				entries = append(entries, entry)
			}
			return entries, nil
		})
		drained(harness)
		frames := recorded.snapshot()
		if len(frames) != 1 {
			t.Fatalf("%d frames, want 1", len(frames))
		}
		expectIds(t, entryIdsOf(frames[0].value.Entries), entryIdsOf(burst[3:]))
		expectBefore(t, frames[0].value, burst[2].Id)
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation, 2))
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("starts at `from` when the entries since fit in `max`, otherwise at the newest `min`", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:436
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		written := notes(t, root, 30)
		window := func(from durable.EntryId) *ConversationWindow {
			return &ConversationWindow{Min: new(5), Max: new(10), From: &from}
		}
		// Ten entries since `from`: all of them, with the entry before `from`.
		within := fresh(t, root, window(written[20].Id))
		expectIds(t, entryIdsOf(within.Entries), entryIdsOf(written[20:]))
		expectBefore(t, within, written[19].Id)
		// Fewer than `min` since `from`: only those.
		short := fresh(t, root, window(written[28].Id))
		expectIds(t, entryIdsOf(short.Entries), entryIdsOf(written[28:]))
		expectBefore(t, short, written[27].Id)
		// Resuming after the newest entry: an empty window right after it.
		resumed := fresh(t, root, window(written[29].Id+1))
		if len(resumed.Entries) != 0 {
			t.Fatalf("resumed entries %v, want none", resumed.Entries)
		}
		expectBefore(t, resumed, written[29].Id)
		// Eleven since `from`: more than `max`, so the newest `min`; `before` shows the client a gap.
		beyond := fresh(t, root, window(written[19].Id))
		expectIds(t, entryIdsOf(beyond.Entries), entryIdsOf(written[25:]))
		expectBefore(t, beyond, written[24].Id)
		// From the transcript's start.
		whole := fresh(t, root, &ConversationWindow{Min: new(40), From: &written[0].Id})
		expectIds(t, entryIdsOf(whole.Entries), entryIdsOf(written))
		if whole.Before != nil {
			t.Fatalf("before = %d, want none", *whole.Before)
		}
		closeHarness(t, harness)
	})

	t.Run("advances a resumed window by appends from its start", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:468
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		written := notes(t, root, 5)
		recorded := record(t, root, &ConversationWindow{From: new(written[4].Id + 1)})
		later := notes(t, root, 2)
		drained(harness)
		frames := recorded.snapshot()
		last := frames[len(frames)-1].value
		expectIds(t, entryIdsOf(last.Entries), entryIdsOf(later))
		expectBefore(t, last, written[4].Id)
		expectSameJSON(t, replay(t, recorded.initial, frames), last)
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("uses `from` only for the first value: a `from` past the newest entry starts empty and takes every append", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:481
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		written := notes(t, root, 3)
		from := written[2].Id + 1000
		recorded := record(t, root, &ConversationWindow{From: &from})
		if len(recorded.initial.Entries) != 0 {
			t.Fatalf("initial entries %v, want none", recorded.initial.Entries)
		}
		expectBefore(t, recorded.initial, written[2].Id)
		// An entry below `from` is still appended: the window continues from its attachment.
		next := notes(t, root, 1)[0]
		if next.Id >= from {
			t.Fatalf("entry %d is not below from %d", next.Id, from)
		}
		drained(harness)
		frames := recorded.snapshot()
		last := frames[len(frames)-1].value
		expectSameJSON(t, last.Entries, []durable.EntryRecord{next})
		expectBefore(t, last, written[2].Id)
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("rejects window bounds that are not integers or put `max` below `min`", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:499
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		for name, window := range map[string]*ConversationWindow{
			"min 0":         {Min: new(0)},
			"max below min": {Min: new(10), Max: new(9)},
		} {
			if _, err := root.Watch(testContext, window); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		}
		recorder := &callRecorder[ConversationView]{adopting: func() bool { return false }}
		if _, err := harness.ObserveConversation(testContext, root.Id(), recorder, &ConversationWindow{Max: new(0)}); err == nil {
			t.Fatal("observeConversation accepted a max of 0")
		}
		// `max` may equal `min`.
		must(must(root.Watch(testContext, &ConversationWindow{Min: new(3), Max: new(3)})).Stop())
		closeHarness(t, harness)
	})

	t.Run("gives each observer its own window over one mount", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:511
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		written := notes(t, root, 6)
		changes := &viewRecording{t: t}
		observation := must(harness.ObserveConversation(testContext, root.Id(), changes, &ConversationWindow{Min: new(2), Max: new(3)}))
		wide := record(t, root, &ConversationWindow{Min: new(5)})
		expectIds(t, entryIdsOf(observation.Value().Entries), entryIdsOf(written[4:]))
		expectIds(t, entryIdsOf(wide.initial.Entries), entryIdsOf(written[1:]))
		next := notes(t, root, 1)[0]
		drained(harness)
		narrow := changes.All()
		expectIds(t, entryIdsOf(narrow[0].Entries), entryIdsOf(append(slices.Clone(written[4:]), next)))
		wideFrames := wide.snapshot()
		expectIds(t, entryIdsOf(wideFrames[0].value.Entries), entryIdsOf(append(slices.Clone(written[1:]), next)))
		if narrow[0].Docs != wideFrames[0].value.Docs {
			t.Fatal("observers do not share the mount's documents")
		}
		observation.Detach()
		wide.stop(t)
		closeHarness(t, harness)
	})

	t.Run("bounds an overflow replacement by `max`", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:535
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		watch := must(root.Watch(testContext, &ConversationWindow{Min: new(5), Max: new(6)}))
		// Past 10,000 pending operations, the next call is one root replacement: here an append per commit, and a splice and a
		// `before` every other commit.
		for range 6000 {
			noteEntry(t, root, "note")
		}
		var mu sync.Mutex
		var frames []viewFrame
		watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, viewFrame{value: change.Value, ops: change.Ops})
			return nil
		})
		drained(harness)
		mu.Lock()
		got := slices.Clone(frames)
		mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("%d frames, want 1", len(got))
		}
		expectSameJSON(t, got[0].ops, []any{[]any{"r", got[0].value}})
		if len(got[0].value.Entries) > 6 {
			t.Fatalf("%d entries, want at most 6", len(got[0].value.Entries))
		}
		must(watch.Stop())
		closeHarness(t, harness)
	})

	t.Run("lets a long-lived watch drop the entries that left its window", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view.test.ts:561
		// On SQLite, which keeps no entry objects, so only the watch can hold the first one.
		harness, root := openChat(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")), chatSetup(t))
		watch := must(root.Watch(testContext, &ConversationWindow{Min: new(5), Max: new(10)}))
		var mu sync.Mutex
		var first weak.Pointer[durable.EntryRecord]
		var seen bool
		count := 0
		watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
			mu.Lock()
			defer mu.Unlock()
			count = len(change.Value.Entries)
			if !seen && len(change.Value.Entries) > 0 {
				first = weak.Make(&change.Value.Entries[0])
				seen = true
			}
			return nil
		})
		notes(t, root, 1)
		waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return seen })
		notes(t, root, 200)
		drained(harness)
		mu.Lock()
		if count > 10 {
			t.Fatalf("%d entries, want at most 10", count)
		}
		mu.Unlock()
		for round := 0; round < 3 && first.Value() != nil; round++ {
			drained(harness)
			runtime.GC()
		}
		if first.Value() != nil {
			t.Fatal("the watch still holds the first entry, which left its window")
		}
		must(watch.Stop())
		closeHarness(t, harness)
	})
}
