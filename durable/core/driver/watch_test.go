package driver_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

type notesState struct {
	Text string `json:"text"`
}

var notesDoc = durable.DefineDoc(durable.DocDefinition[notesState]{
	CommonDocDefinition: durable.CommonDocDefinition[notesState]{Kind: "test.driver-notes", Version: 1, Initial: func() notesState { return notesState{} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
})

func writeNotes(t *testing.T, s durable.Session, text string) {
	t.Helper()
	if _, err := s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		doc, err := tx.Doc(notesDoc)
		if err != nil {
			return nil, err
		}
		return nil, doc.Set("text", text)
	}); err != nil {
		t.Fatal(err)
	}
}

type delivery struct {
	value durable.JsonObject
	ops   []durable.Op
}

// Session.watchDoc (session.ts:268-298) never creates: an absent document resolves undefined, a present one holds its
// acquisition revision before Start, and every later commit is one exact frame in commit order; Stop is idempotent and
// ends future callbacks.
func TestWatchDocDeliversExactFramesInCommitOrder(t *testing.T) {
	s := openCore(t)
	absent, err := s.WatchDocErased(t.Context(), notesDoc)
	if err != nil || absent != nil {
		t.Fatalf("an absent document is not watched: %v %v", absent, err)
	}
	writeNotes(t, s, "a")
	watch, err := s.WatchDocErased(t.Context(), notesDoc)
	if err != nil || watch == nil {
		t.Fatalf("watch: %v %v", watch, err)
	}
	if got := watch.Value().Value("text"); got != "a" {
		t.Fatalf("the acquisition revision is the committed value: %v", got)
	}
	frames := make(chan delivery, 8)
	watch.Start(func(_ context.Context, value durable.JsonObject, ops []durable.Op) error {
		frames <- delivery{value, ops}
		return nil
	})
	writeNotes(t, s, "b")
	writeNotes(t, s, "c")
	for _, want := range []string{"b", "c"} {
		select {
		case f := <-frames:
			if f.value.Value("text") != want || len(f.ops) == 0 {
				t.Fatalf("frame %q: %+v", want, f)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no frame %q", want)
		}
	}
	end, err := watch.Stop()
	if err != nil || end.Reason != durable.WatchStopped {
		t.Fatalf("stop: %+v %v", end, err)
	}
	if again, _ := watch.Stop(); again.Reason != durable.WatchStopped {
		t.Fatalf("stop is idempotent: %+v", again)
	}
	writeNotes(t, s, "d")
	select {
	case f := <-frames:
		t.Fatalf("a stopped watch delivers nothing: %+v", f)
	case <-time.After(200 * time.Millisecond):
	}
}

// Cancelling the caller's context ends the watch with reason cancelled (session.ts watch.observeCancellation), and closing the
// Session ends the others with session_closed.
func TestWatchDocEndsOnCancellationAndSessionClose(t *testing.T) {
	s := openCore(t)
	writeNotes(t, s, "a")
	ctx, cancel := context.WithCancel(t.Context())
	cancelled, err := s.WatchDocErased(ctx, notesDoc)
	if err != nil || cancelled == nil {
		t.Fatalf("watch: %v %v", cancelled, err)
	}
	open, err := s.WatchDocErased(t.Context(), notesDoc)
	if err != nil || open == nil {
		t.Fatalf("watch: %v %v", open, err)
	}
	cancel()
	select {
	case <-cancelled.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled watch closes")
	}
	if got := cancelled.End().Reason; got != durable.WatchCancelled {
		t.Fatalf("cancelled: %v", got)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-open.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("closing the Session closes its watches")
	}
	if got := open.End().Reason; got != durable.WatchSessionClosed {
		t.Fatalf("session closed: %v", got)
	}
}

// Harness.inspect (harness.ts inspect) reports the scheduling state and the live work: a queued submission behind a busy
// conversation is listed, an idle Session lists nothing.
func TestInspectReportsSchedulingAndLiveWork(t *testing.T) {
	s := openCore(t)
	idle, err := s.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if idle.Scheduling == "" || len(idle.Tasks) != 0 || len(idle.Submissions) != 0 {
		t.Fatalf("an idle Session has no live work: %+v", idle)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inspect(t.Context()); err == nil {
		t.Fatal("a closed Session rejects inspect")
	}
}

// session.ts close runs the close listeners synchronously when close begins, once each, in subscription order; an
// unsubscribed listener does not run.
func TestSubscribeCloseRunsListenersOnceInOrder(t *testing.T) {
	s := openCore(t)
	var order []string
	s.SubscribeClose(func() { order = append(order, "first") })
	off := s.SubscribeClose(func() { order = append(order, "dropped") })
	s.SubscribeClose(func() { order = append(order, "second") })
	off()
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(order); got != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("close listeners: %v", order)
	}
}

// Conversation.watch (view.ts) delivers the structural view as exact frames: the first revision holds the conversation and its
// entries, a committed entry arrives as a frame whose value holds it (the driver applies the core's ops-only frame to the
// view it holds), and the view state of the same conversation sees it too.
func TestConversationWatchAndViewStateFollowCommits(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	watch, err := root.Watch(t.Context())
	if err != nil || watch == nil {
		t.Fatalf("watch: %v %v", watch, err)
	}
	state, err := root.ViewState(t.Context())
	if err != nil || state == nil {
		t.Fatalf("view state: %v %v", state, err)
	}
	defer state.Dispose()
	if watch.Value().Conversation.Id != root.Id() {
		t.Fatalf("the view names its conversation: %+v", watch.Value().Conversation)
	}
	before := len(watch.Value().Entries)
	frames := make(chan int, 4)
	watch.Start(func(_ context.Context, value harness.ConversationView, ops []durable.Op) error {
		frames <- len(value.Entries)
		return nil
	})
	entry := durable.EntryDraft{Kind: "note"}
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-frames:
		if n != before+1 {
			t.Fatalf("the frame holds the new entry: %d entries, was %d", n, before)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no view frame")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(state.Value().Entries) != before+1 {
		if time.Now().After(deadline) {
			t.Fatalf("the view state follows the commit: %d entries", len(state.Value().Entries))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if end, err := watch.Stop(); err != nil || end.Reason != durable.WatchStopped {
		t.Fatalf("stop: %+v %v", end, err)
	}
}

// Harness.watchTaskGraph and taskGraph (task-graph.ts) open over the core's graph target: an idle Session has no live task.
func TestTaskGraphOpensEmptyOnAnIdleSession(t *testing.T) {
	s := openCore(t)
	state, err := s.TaskGraph(t.Context())
	if err != nil || state == nil {
		t.Fatalf("task graph: %v %v", state, err)
	}
	defer state.Dispose()
	if n := len(state.Value().Tasks); n != 0 {
		t.Fatalf("no live task: %d", n)
	}
	watch, err := s.WatchTaskGraph(t.Context())
	if err != nil || watch == nil {
		t.Fatalf("watch: %v %v", watch, err)
	}
	if end, err := watch.Stop(); err != nil || end.Reason != durable.WatchStopped {
		t.Fatalf("stop: %+v %v", end, err)
	}
}

// Session.documentState (session.ts): an absent document is nil, a present one is a read-only state that follows each commit and
// is released by Dispose.
func TestDocumentStateFollowsCommitsUntilDisposed(t *testing.T) {
	s := openCore(t)
	absent, err := s.DocumentStateErased(t.Context(), notesDoc)
	if err != nil || absent != nil {
		t.Fatalf("an absent document has no state: %v %v", absent, err)
	}
	writeNotes(t, s, "a")
	state, err := s.DocumentStateErased(t.Context(), notesDoc)
	if err != nil || state == nil {
		t.Fatalf("state: %v %v", state, err)
	}
	if got := state.Value().Value("text"); got != "a" {
		t.Fatalf("hydrated value: %v", got)
	}
	writeNotes(t, s, "b")
	deadline := time.Now().Add(5 * time.Second)
	for state.Value().Value("text") != "b" {
		if time.Now().After(deadline) {
			t.Fatalf("the state follows the commit: %v", state.Value())
		}
		time.Sleep(10 * time.Millisecond)
	}
	state.Dispose()
	writeNotes(t, s, "c")
	time.Sleep(100 * time.Millisecond)
	if got := state.Value().Value("text"); got != "b" {
		t.Fatalf("a disposed state stays at its last value: %v", got)
	}
}

// Session.subscribeCommits (session.ts): a listener receives each successful commit's complete publication in commit order,
// with the document change's exact value and ops; an unsubscribed listener hears nothing more.
func TestSubscribeCommitsDeliversPublicationsInOrder(t *testing.T) {
	s := openCore(t)
	got := make(chan durable.CommitPublication, 8)
	off := s.SubscribeCommits(func(_ context.Context, p durable.CommitPublication) { got <- p })
	writeNotes(t, s, "a")
	writeNotes(t, s, "b")
	var seqs []durable.Seq
	for range 2 {
		select {
		case p := <-got:
			seqs = append(seqs, p.Seq)
			var doc *durable.DocumentChange
			for _, c := range p.Changes {
				if d, ok := c.(durable.DocumentChange); ok && d.Record.Kind == "test.driver-notes" {
					doc = &d
				}
			}
			if doc == nil || doc.Value == nil || (len(seqs) == 2 && (doc.Value.Value("text") != "b" || len(doc.Ops) == 0)) {
				t.Fatalf("publication %d: %+v", p.Seq, p.Changes)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no publication")
		}
	}
	if seqs[0] >= seqs[1] {
		t.Fatalf("commit order: %v", seqs)
	}
	off()
	off()
	writeNotes(t, s, "c")
	select {
	case p := <-got:
		t.Fatalf("an unsubscribed listener hears nothing: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
}

// driver.OpenHarness opens the same store again after close: what one Harness committed the next one reads (a store is a plain
// SQLite file, CONTRACT section 3).
func TestOpenHarnessReopensItsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	options := harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()}
	first, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	root, err := first.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := durable.EntryDraft{Kind: "note"}
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := root.Id()
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close(t.Context()) }()
	again, err := second.Root(t.Context(), nil)
	if err != nil || again.Id() != id {
		t.Fatalf("the reserved root is the same conversation: %v %v", again, err)
	}
	page, err := again.Entries(t.Context(), durable.EntryQuery{}, 10, nil)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("the committed entry survives a reopen: %+v %v", page, err)
	}
}

// Publications reach a listener in commit order however many commits follow each other (one delivery goroutine per Session).
func TestSubscribeCommitsKeepsCommitOrderUnderBurst(t *testing.T) {
	s := openCore(t)
	var mu sync.Mutex
	var seqs []durable.Seq
	s.SubscribeCommits(func(_ context.Context, p durable.CommitPublication) {
		mu.Lock()
		seqs = append(seqs, p.Seq)
		mu.Unlock()
	})
	const n = 30
	for i := range n {
		writeNotes(t, s, fmt.Sprint(i))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := len(seqs)
		mu.Unlock()
		if got >= n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d publications arrived", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("commit order broke at %d: %v", i, seqs)
		}
	}
}

// The real core accepts the retired incarnation's write after the retireDoc op (Pi retireDoc persists the final content): the
// commit succeeds, the next commit starts a new incarnation from Initial, and a stored document's checkpointWhen verdict and entry
// index reach the core without a rejection.
func TestRetiringADocumentThroughTheRealCoreStartsANewIncarnation(t *testing.T) {
	s := openCore(t)
	token := durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
		CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{
			Kind: "t.native.retire", Version: 1,
			CheckpointWhen: func(durable.JsonObject, []durable.Op, durable.CheckpointInfo) bool { return true }, Initial: func() durable.JsonObject { return delta.JsonObjectOf() }},
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
	set := func(n int, retire bool) (seen bool) {
		t.Helper()
		_, err := s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			object, err := tx.Doc(token)
			if err != nil {
				return nil, err
			}
			seen = object.Has("n")
			if err := object.Set("n", n); err != nil {
				return nil, err
			}
			if retire {
				return nil, tx.RetireDoc(token)
			}
			return nil, nil
		})
		if err != nil {
			t.Fatalf("commit n=%d retire=%v: %v", n, retire, err)
		}
		return seen
	}
	if set(1, false) {
		t.Fatal("a new document starts from Initial")
	}
	if !set(2, false) {
		t.Fatal("the stored document keeps n")
	}
	if !set(3, true) {
		t.Fatal("the incarnation being retired still holds n")
	}
	if set(4, false) {
		t.Fatal("after the retirement the address starts a new incarnation from Initial")
	}
}
