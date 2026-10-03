// Ports packages/durable/test/harness-view.test.ts.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type viewFrame struct {
	value ConversationView
	ops   []durable.Op
}

var viewMounted = []string{"pi.agent", "pi.inbox", "pi.live", "pi.usage"}

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
func record(t *testing.T, conversation Conversation) *recording {
	t.Helper()
	watch := must(conversation.Watch(testContext))
	recorded := &recording{initial: watch.Value(), watch: watch}
	watch.Start(func(_ context.Context, value ConversationView, ops []durable.Op) error {
		recorded.mu.Lock()
		defer recorded.mu.Unlock()
		recorded.frames = append(recorded.frames, viewFrame{value: value, ops: ops})
		return nil
	})
	return recorded
}

// fresh returns a freshly built view of conversation, for comparing with an advanced one (harness-view.test.ts:36).
func fresh(t *testing.T, conversation Conversation) ConversationView {
	t.Helper()
	state := must(conversation.ViewState(testContext))
	value := state.Value()
	state.Dispose()
	return value
}

// committed is the view as committed state defines it, read without any mount (harness-view.test.ts:44).
func committed(t *testing.T, harness Harness, conversation Conversation, record durable.ConversationRecord) ConversationView {
	t.Helper()
	docs := map[string]durable.JsonObject{}
	for _, token := range []durable.AnyDocToken{AgentDoc, LiveDoc, InboxDoc, UsageDoc} {
		value := must(harness.SnapshotErased(testContext, token, conversation.Id()))
		if value != nil {
			docs[token.AnyDefinition().Kind] = value
		}
	}
	return ConversationView{Conversation: record, Entries: must(conversation.Context(testContext)).Entries, Docs: docs}
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

func expectSameJSON(t *testing.T, got, want any) {
	t.Helper()
	if left, right := jsonOf(t, got), jsonOf(t, want); !reflect.DeepEqual(left, right) {
		t.Fatalf("got  %v\nwant %v", left, right)
	}
}

// replay applies every frame's operations from initial, checking each delivered revision on the way (harness-view.test.ts:54).
func replay(t *testing.T, initial ConversationView, frames []viewFrame) any {
	t.Helper()
	value := jsonOf(t, initial)
	for _, frame := range frames {
		ops := jsonOf(t, frame.ops).([]any)
		converted := make([]durable.Op, len(ops))
		for i, op := range ops {
			converted[i] = op.([]any)
		}
		next, err := delta.ApplyImmutable(value, converted)
		if err != nil {
			t.Fatal(err)
		}
		value = next
		if want := jsonOf(t, frame.value); !reflect.DeepEqual(value, want) {
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

func sameMap[K comparable, V any](a, b map[K]V) bool {
	return reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer()
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
	t.Run("hydrates the active entries and the built-in documents", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("hello")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		must(submission.Wait(testContext))
		view := fresh(t, root)
		expectSameJSON(t, view.Conversation, map[string]any{"id": float64(root.Id())})
		expectSameJSON(t, view.Entries, allEntries(t, root))
		keys := make([]string, 0, len(view.Docs))
		for key := range view.Docs {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		expectStrings(t, keys, viewMounted)
		expectSameJSON(t, view.Docs["pi.live"], map[string]any{})
		expectSameJSON(t, view.Docs["pi.inbox"], map[string]any{"items": []any{}})
		closeHarness(t, harness)
	})

	t.Run("publishes one frame per touching commit, whose operations rebuild every revision", func(t *testing.T) {
		setup := chatSetup(t)
		release := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(release, "a longer answer")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		recorded := record(t, root)
		touching := touches(harness, root)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		waitFor(t, func() bool {
			return slices.ContainsFunc(recorded.snapshot(), func(frame viewFrame) bool {
				_, ok := frame.value.Docs["pi.live"]["generation"]
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
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation))
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
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		recorded := record(t, root)
		noteEntry(t, other, "note")
		if err := root.Configure(testContext, AgentChange{ThinkingLevel: SetTo(ai.ThinkingHigh)}); err != nil {
			t.Fatal(err)
		}
		noteEntry(t, root, "note")
		drained(harness)
		frames := recorded.snapshot()
		if len(frames) != 2 {
			t.Fatalf("%d frames", len(frames))
		}
		expectSameJSON(t, frames[0].ops, []any{[]any{"s", []any{"docs", "pi.agent", "thinkingLevel"}, "high"}})
		if !sameEntries(frames[0].value.Entries, recorded.initial.Entries) {
			t.Fatal("entries were not shared")
		}
		if !sameMap(frames[0].value.Docs["pi.live"], recorded.initial.Docs["pi.live"]) {
			t.Fatal("pi.live was not shared")
		}
		if !sameMap(frames[1].value.Docs, frames[0].value.Docs) {
			t.Fatal("docs were not shared")
		}
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("cuts the entries at a head marker, keeping the entries from its head", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		noteEntry(t, root, "a")
		b := noteEntry(t, root, "b")
		noteEntry(t, root, "c")
		recorded := record(t, root)
		summary := noteEntry(t, root, "summary", b.Id)
		noteEntry(t, root, "d")
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		drained(harness)
		frames := recorded.snapshot()
		var kinds [][]string
		for _, frame := range frames {
			kinds = append(kinds, entryKinds(frame.value.Entries))
		}
		expectSameJSON(t, kinds, [][]string{{"summary", "b", "c"}, {"summary", "b", "c", "d"}, {"pi.reset"}})
		expectSameJSON(t, frames[0].ops, []any{[]any{"p", []any{"entries"}, 0, 1, []any{summary}}})
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation))
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("keeps only mounted entries for a raw head write that targets before the active range", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		old := noteEntry(t, root, "old")
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		recorded := record(t, root)
		noteEntry(t, root, "summary", old.Id)
		drained(harness)
		// Model context now starts at old again, but the mount never held it (spec §12); a rebuilt mount shows it.
		expectStrings(t, entryKinds(recorded.snapshot()[0].value.Entries), []string{"summary"})
		recorded.stop(t)
		expectStrings(t, entryKinds(fresh(t, root).Entries), []string{"summary", "old"})
		closeHarness(t, harness)
	})

	t.Run("cuts a fork's view into its inherited entries", func(t *testing.T) {
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
		expectStrings(t, entryKinds(frames[0].value.Entries), []string{"summary", "b"})
		expectSameJSON(t, frames[0].value, committed(t, harness, fork, recorded.initial.Conversation))
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("shows a fork's inherited entries and follows only the fork's own commits", func(t *testing.T) {
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
		if _, present := frames[0].value.Docs["pi.live"]; present {
			t.Fatal("pi.live is still mounted")
		}
		recorded.stop(t)
		closeHarness(t, harness)
	})

	t.Run("replaces undelivered frames with the newest view after 100 pending frames", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		watch := must(root.Watch(testContext))
		for range 101 {
			noteEntry(t, root, "note")
		}
		var mu sync.Mutex
		var frames []viewFrame
		watch.Start(func(_ context.Context, value ConversationView, ops []durable.Op) error {
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
		expectSameJSON(t, got[0].ops, []any{[]any{"r", got[0].value}})
		if len(got[0].value.Entries) != 101 {
			t.Fatalf("%d entries", len(got[0].value.Entries))
		}
		must(watch.Stop())
		closeHarness(t, harness)
	})

	t.Run("keeps states and watches of one conversation independent and remounts after the last one detaches", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		state := must(root.ViewState(testContext))
		recorded := record(t, root)
		noteEntry(t, root, "one")
		drained(harness)
		expectStrings(t, entryKinds(state.Value().Entries), []string{"one"})
		recorded.stop(t)
		if frames := recorded.snapshot(); len(frames) != 1 {
			t.Fatalf("%d frames", len(frames))
		}
		noteEntry(t, root, "two")
		drained(harness)
		expectStrings(t, entryKinds(state.Value().Entries), []string{"one", "two"})
		last := state.Value()
		state.Dispose()
		// No observer is left, so the mount was dropped: a new observer builds a new revision from committed state.
		rebuilt := fresh(t, root)
		expectSameJSON(t, rebuilt, last)
		if sameEntries(rebuilt.Entries, last.Entries) || sameMap(rebuilt.Docs, last.Docs) {
			t.Fatal("rebuilt view shares the dropped mount's revision")
		}
		closeHarness(t, harness)
	})

	t.Run("ends states and watches at close and rejects later acquisition", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		watch := must(root.Watch(testContext))
		state := must(root.ViewState(testContext))
		closeHarness(t, harness)
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchSessionClosed || end.Error != nil {
			t.Fatalf("end %+v", end)
		}
		if entries := state.Value().Entries; len(entries) != 0 {
			t.Fatalf("entries %v", entries)
		}
		if _, err := root.Watch(testContext); err == nil {
			t.Fatal("watch after close")
		}
		if _, err := root.ViewState(testContext); err == nil {
			t.Fatal("viewState after close")
		}
	})

	t.Run("rejects an acquisition cancelled or closed while it waits for the Session line", func(t *testing.T) {
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
				_, err := root.Watch(cancelled)
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
				_, err := root.Watch(testContext)
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
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		failing := must(root.Watch(testContext))
		recorded := record(t, root)
		shared := must(root.ViewState(testContext))
		if !sameEntries(failing.Value().Entries, shared.Value().Entries) || !sameMap(failing.Value().Docs, shared.Value().Docs) {
			t.Fatal("observers do not share one mount revision")
		}
		shared.Dispose()
		failing.Start(func(context.Context, ConversationView, []durable.Op) error { return errors.New("listener failed") })
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
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		type other struct {
			N int `json:"n"`
		}
		Other := durable.DefineDoc(durable.DocDefinition[other]{
			CommonDocDefinition: durable.CommonDocDefinition[other]{Kind: "app.other", Version: 1},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
			Initial:             func() other { return other{} },
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
		expectSameJSON(t, replay(t, recorded.initial, frames), committed(t, harness, root, recorded.initial.Conversation))
		recorded.stop(t)
		closeHarness(t, harness)
	})
}
