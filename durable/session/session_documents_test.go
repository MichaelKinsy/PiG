package session_test

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-documents.test.ts

var (
	liveInitMu    sync.Mutex
	liveInitCount int
)

func liveInitial() obj {
	return delta.JsonObjectOf("items", []any{}, "nested", delta.JsonObjectOf("count", 0), "other", delta.JsonObjectOf("label", "x"))
}

var liveDoc = defineDoc("test.live", 1, latestScope(durable.ForkInitial), func() obj {
	liveInitMu.Lock()
	liveInitCount++
	liveInitMu.Unlock()
	return liveInitial()
})

var rewindableLiveDoc = defineDoc("test.live", 1, rewindableScope(durable.ForkAsOf), liveInitial)

var liveDocV2 = defineDoc("test.live", 2, latestScope(durable.ForkInitial), liveInitial)

var counterDoc = defineDoc("test.counter", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0) })

var (
	seedsMu sync.Mutex
	seeds   []string
)

var memberDoc = defineFamily("test.member", 1, sessionScope, func(seed string) obj {
	seedsMu.Lock()
	seeds = append(seeds, seed)
	seedsMu.Unlock()
	return delta.JsonObjectOf("seed", seed, "hits", 0)
})

type liveHarness struct {
	sessiontest.Harness
	conversationId durable.ConversationId
}

func setupLive(t *testing.T) liveHarness {
	t.Helper()
	harness := open()
	conversationId := createConversation(t, harness)
	commit(t, harness.Session, func(tx durable.Tx) error {
		_, err := mustDoc(t, tx, liveDoc, conversationId).Array("items").Push("a", "b")
		return err
	})
	return liveHarness{harness, conversationId}
}

func lastPublishedDocument(harness sessiontest.Harness) durable.DocumentChange {
	return sessiontest.DocumentChanges(harness.Publications.Last())[0]
}

func lastAdmitted(harness sessiontest.Harness) []durable.StorageWrite {
	admitted := harness.Storage.AdmittedCommits()
	return admitted[len(admitted)-1]
}

func setLive(t *testing.T, harness liveHarness, key string, value any) {
	t.Helper()
	commit(t, harness.Session, func(tx durable.Tx) error {
		return mustDoc(t, tx, liveDoc, harness.conversationId).Set(key, value)
	})
}

// waitLineJobs waits until n line jobs are admitted, as upstream's synchronous calls admit them in call order.
func waitLineJobs(harness sessiontest.Harness, n int) {
	for harness.Session.LineJobs() < n {
		runtime.Gosched()
	}
}

// waitSealed waits until a commit callback settled, which upstream's synchronous return guarantees before the test
// releases a held call.
func waitSealed(tx *session.Transaction) {
	for !session.IsSealed(tx) {
		runtime.Gosched()
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a held call")
	}
}

func TestSessionDocumentTransactions(t *testing.T) {
	t.Run("creates an initial base on first access and adopts it after Storage success", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		commit(t, harness.Session, func(tx durable.Tx) error {
			return mustDoc(t, tx, liveDoc, conversationId).Set("message", "hello")
		})
		writes := harness.Storage.LastCommit()
		if len(writes) != 1 {
			t.Fatalf("writes %d", len(writes))
		}
		create, ok := writes[0].(durable.DocumentCreateWrite)
		if !ok {
			t.Fatalf("write %T", writes[0])
		}
		if create.Record.Kind != "test.live" || create.Record.Scope != (durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}) ||
			create.Record.History != durable.HistoryLatest || create.Record.Fork != durable.ForkInitial {
			t.Fatalf("record %+v", create.Record)
		}
		if create.Content.Kind != durable.ContentBase || create.Content.Version != 1 {
			t.Fatalf("content %+v", create.Content)
		}
		expectEqual(t, create.Content.Value.Value("message"), "hello")
		value := snapshot(t, harness.Session, liveDoc, conversationId)
		expectEqual(t, value, delta.JsonObjectOf("message", "hello", "items", []any{}, "nested", delta.JsonObjectOf("count", 0), "other", delta.JsonObjectOf("label", "x")))
		flush(harness)
		publication := harness.Publications.Last()
		published := sessiontest.DocumentChanges(publication)[0]
		if published.Record.CreatedAt != publication.Seq || !same(published.Value, value) || *published.ConversationId != conversationId {
			t.Fatal("publication carries the adopted creation")
		}
		admittedCreate := writesOfType(lastAdmitted(harness), "document.create")[0].(durable.DocumentCreateWrite)
		if !same(admittedCreate.Content.Value, published.Value) {
			t.Fatal("the admitted base is the published value")
		}
	})

	t.Run("never creates on snapshot and returns undefined when absent", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		before := len(harness.Storage.Commits())
		if snapshot(t, harness.Session, liveDoc, conversationId) != nil || snapshot(t, harness.Session, counterDoc) != nil || snapshot(t, harness.Session, memberDoc, "k") != nil {
			t.Fatal("absent documents snapshot as nil")
		}
		if len(harness.Storage.Commits()) != before || harness.Storage.MintCount() != 1 {
			t.Fatal("snapshot never creates")
		}
	})

	t.Run("returns shared immutable snapshots and keeps prior revisions stable", func(t *testing.T) {
		harness := setupLive(t)
		first := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		if !same(snapshot(t, harness.Session, liveDoc, harness.conversationId), first) {
			t.Fatal("repeated snapshots share the revision")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			return mustDoc(t, tx, liveDoc, harness.conversationId).Object("nested").Set("count", 1)
		})
		second := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		if same(second, first) {
			t.Fatal("a change produces a new revision")
		}
		expectEqual(t, first.Value("nested"), delta.JsonObjectOf("count", 0))
		expectEqual(t, second.Value("nested"), delta.JsonObjectOf("count", 1))
		// Unchanged subtrees are structurally shared between immutable revisions.
		if !same(second.Value("items"), first.Value("items")) || !same(second.Value("other"), first.Value("other")) {
			t.Fatal("unchanged subtrees are shared")
		}
	})

	t.Run("adopts by pointer swap and shares operation payloads with the published revision", func(t *testing.T) {
		harness := setupLive(t)
		commit(t, harness.Session, func(tx durable.Tx) error {
			live := mustDoc(t, tx, liveDoc, harness.conversationId)
			must(t, live.Set("other", delta.JsonObjectOf("label", "y")))
			_, err := live.Array("items").Push("c")
			return err
		})
		flush(harness.Harness)
		value := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		published := lastPublishedDocument(harness.Harness)
		if !same(published.Value, value) {
			t.Fatal("published value is the snapshot")
		}
		change := writesOfType(lastAdmitted(harness.Harness), "document.change")[0].(durable.DocumentChangeWrite)
		if change.Content.Kind != durable.ContentDelta || !same(published.Ops, change.Content.Ops) {
			t.Fatal("published ops are the admitted delta")
		}
		var set durable.Op
		for _, op := range published.Ops {
			if op[0] == "s" {
				set = op
			}
		}
		expectEqual(t, set, durable.Op{"s", []any{"other"}, delta.JsonObjectOf("label", "y")})
		// Trusted immutability: the Session makes no second copy of operation payloads.
		if !same(set[2], value.Value("other")) {
			t.Fatal("the set payload is the revision's container")
		}
	})

	t.Run("copies assigned values per placement", func(t *testing.T) {
		harness := setupLive(t)
		value := delta.JsonObjectOf("label", "shared")
		commit(t, harness.Session, func(tx durable.Tx) error {
			live := mustDoc(t, tx, liveDoc, harness.conversationId)
			must(t, live.Set("other", value))
			must(t, live.Set("copy", value))
			value.Set("label", "mutated")
			return nil
		})
		got := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		expectEqual(t, got.Value("other"), delta.JsonObjectOf("label", "shared"))
		expectEqual(t, got.Value("copy"), delta.JsonObjectOf("label", "shared"))
		if same(got.Value("copy"), got.Value("other")) || same(got.Value("other"), value) {
			t.Fatal("each placement is an independent copy")
		}
	})

	t.Run("suppresses writes and publications for empty batches", func(t *testing.T) {
		harness := setupLive(t)
		flush(harness.Harness)
		commits := len(harness.Storage.Commits())
		published := harness.Publications.Len()
		commit(t, harness.Session, func(tx durable.Tx) error {
			live := mustDoc(t, tx, liveDoc, harness.conversationId)
			must(t, live.Object("nested").Set("count", 0))
			_, err := live.Array("items").Push("z")
			must(t, err)
			live.Array("items").Pop()
			return nil
		})
		flush(harness.Harness)
		if len(harness.Storage.Commits()) != commits || harness.Publications.Len() != published {
			t.Fatal("an empty batch writes and publishes nothing")
		}
	})

	t.Run("writes and publishes replayable nonempty structural no-ops", func(t *testing.T) {
		harness := setupLive(t)
		before := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		commit(t, harness.Session, func(tx durable.Tx) error {
			items := mustDoc(t, tx, liveDoc, harness.conversationId).Array("items")
			first := items.Shift()
			_, err := items.Unshift(first)
			return err
		})
		flush(harness.Harness)
		change, ok := harness.Storage.LastCommit()[0].(durable.DocumentChangeWrite)
		if !ok || change.Content.Kind != durable.ContentDelta {
			t.Fatal("a structural no-op writes a delta")
		}
		after := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		expectEqual(t, after, before)
		if same(after, before) || len(lastPublishedDocument(harness.Harness).Ops) == 0 {
			t.Fatal("a structural no-op publishes a new revision with ops")
		}
	})

	t.Run("revokes escaped drafts when the callback settles", func(t *testing.T) {
		harness := setupLive(t)
		var escaped *delta.Object
		var items *delta.Array
		commit(t, harness.Session, func(tx durable.Tx) error {
			escaped = mustDoc(t, tx, liveDoc, harness.conversationId)
			items = escaped.Array("items")
			return escaped.Set("message", "inside")
		})
		expectPanic(t, "settled overlay", func() { escaped.Get("message") })
		expectPanic(t, "settled overlay", func() { _ = escaped.Set("message", "outside") })
		expectPanic(t, "settled overlay", func() { items.Len() })
		expectPanic(t, "settled overlay", func() { _, _ = items.Push("outside") })
		expectEqual(t, snapshot(t, harness.Session, liveDoc, harness.conversationId).Value("message"), "inside")
		returned, err := harness.Session.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Doc(liveDoc, harness.conversationId) })
		must(t, err)
		expectPanic(t, "settled overlay", func() { returned.(*delta.Object).Get("message") })
	})

	t.Run("aborts every change when the callback fails", func(t *testing.T) {
		harness := setupLive(t)
		before := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		commits := len(harness.Storage.Commits())
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			live := mustDoc(t, tx, liveDoc, harness.conversationId)
			counter := mustDoc(t, tx, counterDoc)
			must(t, live.Set("message", "lost"))
			must(t, counter.Set("count", 5))
			return errors.New("callback failed")
		})
		expectErrorContains(t, err, "callback failed")
		if len(harness.Storage.Commits()) != commits || !same(snapshot(t, harness.Session, liveDoc, harness.conversationId), before) || snapshot(t, harness.Session, counterDoc) != nil {
			t.Fatal("a failed callback changes nothing")
		}
		setLive(t, harness, "message", "kept")
		expectEqual(t, snapshot(t, harness.Session, liveDoc, harness.conversationId).Value("message"), "kept")
	})

	t.Run("memoizes concurrent duplicate acquisition and initializes once", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		liveInitMu.Lock()
		initCount := liveInitCount
		liveInitMu.Unlock()
		mints := harness.Storage.MintCount()
		commit(t, harness.Session, func(tx durable.Tx) error {
			var first, second *delta.Object
			var group sync.WaitGroup
			group.Go(func() { first, _ = tx.Doc(liveDoc, conversationId) })
			group.Go(func() { second, _ = tx.Doc(liveDoc, conversationId) })
			group.Wait()
			if first == nil || first != second || mustDoc(t, tx, liveDoc, conversationId) != first {
				t.Fatal("duplicate acquisitions share one draft")
			}
			return first.Set("message", "once")
		})
		liveInitMu.Lock()
		defer liveInitMu.Unlock()
		if liveInitCount != initCount+1 || harness.Storage.MintCount() != mints+1 || len(writesOfType(harness.Storage.LastCommit(), "document.create")) != 1 {
			t.Fatal("one initialization, one mint, one creation")
		}
	})

	t.Run("uses the first family seed and ignores seeds for existing members", func(t *testing.T) {
		harness := open()
		seedsMu.Lock()
		seeds = nil
		seedsMu.Unlock()
		increment := func(draft *delta.Object) error { return draft.Set("hits", num(draft.Get("hits"))+1) }
		commit(t, harness.Session, func(tx durable.Tx) error {
			first := mustDoc(t, tx, memberDoc, "k", "first")
			if mustDoc(t, tx, memberDoc, "k", "second") != first {
				t.Fatal("one draft per member")
			}
			return increment(first)
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, increment(mustDoc(t, tx, memberDoc, "k", "third")))
			return increment(mustDoc(t, tx, memberDoc, "other", "fourth"))
		})
		seedsMu.Lock()
		seen := append([]string(nil), seeds...)
		seedsMu.Unlock()
		expectEqual(t, seen, []string{"first", "fourth"})
		expectEqual(t, snapshot(t, harness.Session, memberDoc, "k"), delta.JsonObjectOf("seed", "first", "hits", 2))
		expectEqual(t, snapshot(t, harness.Session, memberDoc, "other"), delta.JsonObjectOf("seed", "fourth", "hits", 1))
	})

	t.Run("rejects a callback that succeeds with a pending acquisition and drains it", func(t *testing.T) {
		harness := setupLive(t)
		must(t, harness.Session.UnloadDocuments())
		gate := harness.Storage.HoldFindDocument()
		commits := len(harness.Storage.Commits())
		pending := make(chan error, 1)
		settled := make(chan error, 1)
		captured := make(chan *session.Transaction, 1)
		go func() {
			settled <- tryCommit(harness.Session, func(tx durable.Tx) error {
				captured <- tx.(*session.Transaction)
				go func() {
					_, err := tx.Doc(liveDoc, harness.conversationId)
					pending <- err
				}()
				<-gate.Entered()
				return nil
			})
		}()
		waitSealed(<-captured)
		// The line stays held until the late acquisition settles.
		select {
		case <-settled:
			t.Fatal("the commit settles only after the pending acquisition")
		default:
		}
		gate.Release()
		expectErrorContains(t, <-settled, "pending Tx operations")
		expectErrorContains(t, <-pending, "Transaction has settled")
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is written")
		}
		setLive(t, harness, "message", "after")
		expectEqual(t, snapshot(t, harness.Session, liveDoc, harness.conversationId).Value("message"), "after")
	})

	t.Run("does not initialize or mint for an absent acquisition that finishes after settlement", func(t *testing.T) {
		harness := open()
		var mu sync.Mutex
		initialized := 0
		lateDoc := defineDoc("test.late", 1, sessionScope, func() obj {
			mu.Lock()
			initialized++
			mu.Unlock()
			return delta.JsonObjectOf("count", 0)
		})
		gate := harness.Storage.HoldFindDocument()
		mints := harness.Storage.MintCount()
		pending := make(chan error, 1)
		captured := make(chan *session.Transaction, 1)
		err := func() error {
			defer gate.Release()
			return tryCommit(harness.Session, func(tx durable.Tx) error {
				captured <- tx.(*session.Transaction)
				go func() {
					_, err := tx.Doc(lateDoc)
					pending <- err
				}()
				<-gate.Entered()
				return nil
			})
		}
		settled := make(chan error, 1)
		go func() { settled <- err() }()
		waitSealed(<-captured)
		gate.Release()
		expectErrorContains(t, <-settled, "pending Tx operations")
		expectErrorContains(t, <-pending, "Transaction has settled")
		mu.Lock()
		defer mu.Unlock()
		if initialized != 0 || harness.Storage.MintCount() != mints {
			t.Fatal("a late absent acquisition neither initializes nor mints")
		}
	})

	t.Run("rejects with the callback error when it fails with a pending acquisition", func(t *testing.T) {
		harness := setupLive(t)
		must(t, harness.Session.UnloadDocuments())
		gate := harness.Storage.HoldFindDocument()
		pending := make(chan error, 1)
		settled := make(chan error, 1)
		captured := make(chan *session.Transaction, 1)
		go func() {
			settled <- tryCommit(harness.Session, func(tx durable.Tx) error {
				captured <- tx.(*session.Transaction)
				go func() {
					_, err := tx.Doc(liveDoc, harness.conversationId)
					pending <- err
				}()
				<-gate.Entered()
				return errors.New("callback failed")
			})
		}()
		waitSealed(<-captured)
		gate.Release()
		expectErrorContains(t, <-settled, "callback failed")
		expectErrorContains(t, <-pending, "Transaction has settled")
	})

	t.Run("rejects Tx use after the callback settles", func(t *testing.T) {
		harness := setupLive(t)
		var captured *session.Transaction
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			captured = tx
			return nil
		})
		_, err := captured.Doc(liveDoc, harness.conversationId)
		expectErrorContains(t, err, "Transaction has settled")
		_, err = captured.Conversation(harness.conversationId)
		expectErrorContains(t, err, "Transaction has settled")
		expectErrorContains(t, captured.SetTask(session.AnyTaskRecord{}), "Transaction has settled")
	})

	t.Run("rejects tokens whose semantics or version disagree with the stored incarnation", func(t *testing.T) {
		harness := setupLive(t)
		_, err := harness.Session.SnapshotErased(ctx, rewindableLiveDoc, harness.conversationId)
		expectErrorContains(t, err, "does not match the supplied definition semantics")
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(rewindableLiveDoc, harness.conversationId)
			return err
		})
		expectErrorContains(t, err, "does not match the supplied definition semantics")
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(liveDocV2, harness.conversationId)
			return err
		})
		expectErrorContains(t, err, "requires migration from version 1")
	})

	t.Run("rejects non-JSON initializer values and draft placements before Storage admission", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		// Upstream's non-JSON value is a Date instance. A Go value with a JSON encoding (time.Time) is typed JSON, so
		// the Go counterpart is a kind with no JSON form.
		dateDoc := defineDoc("test.date", 1, sessionScope, func() obj { return delta.JsonObjectOf("at", make(chan int)) })
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(dateDoc)
			return err
		})
		expectStrictJSON(t, err)
		commits := len(harness.Storage.Commits())
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := mustDoc(t, tx, liveDoc, conversationId).Array("items").Push(func() {})
			return err
		})
		expectStrictJSON(t, err)
		if len(harness.Storage.Commits()) != commits || snapshot(t, harness.Session, liveDoc, conversationId) != nil {
			t.Fatal("a rejected placement writes nothing")
		}
	})

	t.Run("rolls back prepared documents when batch assembly fails", func(t *testing.T) {
		harness := setupLive(t)
		commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, counterDoc).Set("count", 1) })
		live := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		counter := snapshot(t, harness.Session, counterDoc)
		commits := len(harness.Storage.Commits())
		err := tryCommitWith(harness.Session, func(tx *session.Transaction) error {
			must(t, mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "lost"))
			must(t, mustDoc(t, tx, counterDoc).Set("count", 2))
			// Replacing a missing task fails during assembly, after every change was prepared.
			var checkpoint any = delta.JsonObjectOf("phase", "start")
			return tx.SetTask(session.AnyTaskRecord{
				Id: 999, ConversationId: harness.conversationId, Kind: "missing", Version: 1,
				State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
			})
		})
		expectErrorContains(t, err, "Task 999 does not exist")
		if len(harness.Storage.Commits()) != commits || !same(snapshot(t, harness.Session, liveDoc, harness.conversationId), live) || !same(snapshot(t, harness.Session, counterDoc), counter) {
			t.Fatal("assembly failure rolls back every prepared change")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "next"))
			return mustDoc(t, tx, counterDoc).Set("count", 3)
		})
		expectEqual(t, snapshot(t, harness.Session, counterDoc).Value("count"), 3)
	})

	t.Run("poisons the Session after an uncertain Storage failure and publishes nothing", func(t *testing.T) {
		harness := setupLive(t)
		flush(harness.Harness)
		before := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		published := harness.Publications.Len()
		harness.Storage.FailNextCommit(errors.New("disk vanished"))
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			return mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "uncertain")
		})
		expectErrorContains(t, err, "disk vanished")
		flush(harness.Harness)
		if harness.Publications.Len() != published {
			t.Fatal("nothing is published")
		}
		if _, ok := before.Get("message"); ok {
			t.Fatal("the prior revision is unchanged")
		}
		_, err = harness.Session.SnapshotErased(ctx, liveDoc, harness.conversationId)
		expectErrorContains(t, err, "poisoned")
		expectErrorContains(t, tryCommit(harness.Session, func(durable.Tx) error { return nil }), "poisoned")
		must(t, harness.Session.Close(ctx))
	})

	t.Run("keeps the previous revision unchanged through Storage settlement", func(t *testing.T) {
		harness := setupLive(t)
		before := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		copied := normalize(before)
		gate := harness.Storage.HoldCommits()
		done := make(chan error, 1)
		go func() {
			done <- tryCommit(harness.Session, func(tx durable.Tx) error {
				live := mustDoc(t, tx, liveDoc, harness.conversationId)
				_, err := live.Array("items").Push("c")
				must(t, err)
				return live.Object("nested").Set("count", 9)
			})
		}()
		waitClosed(t, gate.Entered())
		if !same(snapshot(t, harness.Session, liveDoc, harness.conversationId), before) {
			t.Fatal("the snapshot stays at the committed revision during Storage settlement")
		}
		expectEqual(t, before, copied)
		gate.Release()
		must(t, <-done)
		expectEqual(t, snapshot(t, harness.Session, liveDoc, harness.conversationId).Value("items"), []any{"a", "b", "c"})
		expectEqual(t, before, copied)
	})

	t.Run("retires documents and creates a new incarnation at the same address", func(t *testing.T) {
		harness := setupLive(t)
		flush(harness.Harness)
		oldId := lastPublishedDocument(harness.Harness).Record.Id
		commit(t, harness.Session, func(tx durable.Tx) error {
			live := mustDoc(t, tx, liveDoc, harness.conversationId)
			must(t, live.Set("message", "final"))
			must(t, tx.RetireDoc(liveDoc, harness.conversationId))
			replacement := mustDoc(t, tx, liveDoc, harness.conversationId)
			if replacement == live {
				t.Fatal("the replacement is a new draft")
			}
			return replacement.Set("message", "new")
		})
		writes := harness.Storage.LastCommit()
		if len(writes) != 3 {
			t.Fatalf("writes %d", len(writes))
		}
		change := writesOfType(writes, "document.change")
		if len(change) != 1 || change[0].(durable.DocumentChangeWrite).Id != oldId {
			t.Fatal("the old incarnation persists its final content")
		}
		if retire := writesOfType(writes, "document.retire"); len(retire) != 1 || retire[0].(durable.DocumentRetireWrite).Id != oldId {
			t.Fatal("the old incarnation retires")
		}
		if create := writesOfType(writes, "document.create"); len(create) != 1 || create[0].(durable.DocumentCreateWrite).Record.Kind != "test.live" {
			t.Fatal("a new incarnation is created")
		}
		flush(harness.Harness)
		publication := harness.Publications.Last()
		documents := sessiontest.DocumentChanges(publication)
		retired, created := documents[0], documents[1]
		if retired.Record.Id != oldId || retired.Value != nil || len(retired.Ops) != 0 || *retired.Record.RetiredAt != publication.Seq {
			t.Fatal("retirement publication")
		}
		if created.Record.Id == oldId || created.Record.CreatedAt != publication.Seq || len(created.Ops) != 0 {
			t.Fatal("creation publication")
		}
		expectEqual(t, created.Value.Value("message"), "new")
		expectEqual(t, created.Value.Value("items"), []any{})
		if !same(snapshot(t, harness.Session, liveDoc, harness.conversationId), created.Value) {
			t.Fatal("the replacement is current")
		}
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(liveDoc, harness.conversationId) })
		if snapshot(t, harness.Session, liveDoc, harness.conversationId) != nil {
			t.Fatal("retired")
		}
		must(t, harness.Session.UnloadDocuments())
		if snapshot(t, harness.Session, liveDoc, harness.conversationId) != nil {
			t.Fatal("retired after unload")
		}
		// Retiring an absent address is a no-op.
		commits := len(harness.Storage.Commits())
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(liveDoc, harness.conversationId) })
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("retiring an absent address writes nothing")
		}
	})

	t.Run("retires without acquisition and recreates both existing and absent addresses", func(t *testing.T) {
		harness := setupLive(t)
		flush(harness.Harness)
		oldId := lastPublishedDocument(harness.Harness).Record.Id
		must(t, harness.Session.UnloadDocuments())
		commit(t, harness.Session, func(tx durable.Tx) error {
			// Upstream starts retirement, then the acquisition, before awaiting either; the staging order is the
			// contract, so they run in that order here.
			must(t, tx.RetireDoc(liveDoc, harness.conversationId))
			return mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "replacement")
		})
		existing := harness.Storage.LastCommit()
		if len(existing) != 2 || len(writesOfType(existing, "document.create")) != 1 {
			t.Fatalf("writes %d", len(existing))
		}
		if retire := writesOfType(existing, "document.retire"); len(retire) != 1 || retire[0].(durable.DocumentRetireWrite).Id != oldId {
			t.Fatal("the existing incarnation retires")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, tx.RetireDoc(memberDoc, "absent"))
			return mustDoc(t, tx, memberDoc, "absent", "seed").Set("hits", 1)
		})
		absent := harness.Storage.LastCommit()
		if len(writesOfType(absent, "document.retire")) != 0 || len(writesOfType(absent, "document.create")) != 1 {
			t.Fatal("an absent address only creates")
		}
		expectEqual(t, snapshot(t, harness.Session, memberDoc, "absent"), delta.JsonObjectOf("seed", "seed", "hits", 1))
	})

	t.Run("retires the existing incarnation when retirement races a pending acquisition", func(t *testing.T) {
		harness := setupLive(t)
		flush(harness.Harness)
		oldId := lastPublishedDocument(harness.Harness).Record.Id
		commit(t, harness.Session, func(tx durable.Tx) error {
			acquired := mustDoc(t, tx, liveDoc, harness.conversationId)
			must(t, tx.RetireDoc(liveDoc, harness.conversationId))
			return acquired.Set("message", "final")
		})
		first := harness.Storage.LastCommit()
		if len(first) != 2 {
			t.Fatalf("writes %d", len(first))
		}
		change := writesOfType(first, "document.change")
		if len(change) != 1 || change[0].(durable.DocumentChangeWrite).Id != oldId || change[0].(durable.DocumentChangeWrite).Content.Kind != durable.ContentDelta {
			t.Fatal("the final content is a delta of the old incarnation")
		}
		if retire := writesOfType(first, "document.retire"); len(retire) != 1 || retire[0].(durable.DocumentRetireWrite).Id != oldId {
			t.Fatal("the old incarnation retires")
		}
		if snapshot(t, harness.Session, liveDoc, harness.conversationId) != nil {
			t.Fatal("retired")
		}
		setLive(t, harness, "message", "second")
		flush(harness.Harness)
		secondId := lastPublishedDocument(harness.Harness).Record.Id
		commit(t, harness.Session, func(tx durable.Tx) error {
			old := mustDoc(t, tx, liveDoc, harness.conversationId)
			must(t, tx.RetireDoc(liveDoc, harness.conversationId))
			fresh := mustDoc(t, tx, liveDoc, harness.conversationId)
			if fresh == old {
				t.Fatal("recreation is a new draft")
			}
			return fresh.Set("message", "third")
		})
		writes := harness.Storage.LastCommit()
		if len(writes) != 2 || len(writesOfType(writes, "document.create")) != 1 {
			t.Fatalf("writes %d", len(writes))
		}
		if retire := writesOfType(writes, "document.retire"); len(retire) != 1 || retire[0].(durable.DocumentRetireWrite).Id != secondId {
			t.Fatal("the second incarnation retires")
		}
		expectEqual(t, snapshot(t, harness.Session, liveDoc, harness.conversationId).Value("message"), "third")
	})

	t.Run("reloads an unloaded document from Storage", func(t *testing.T) {
		harness := setupLive(t)
		loaded := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		must(t, harness.Session.UnloadDocuments())
		reloaded := snapshot(t, harness.Session, liveDoc, harness.conversationId)
		if same(reloaded, loaded) || !equal(reloaded, loaded) {
			t.Fatal("reload is a detached equal value")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := mustDoc(t, tx, liveDoc, harness.conversationId).Array("items").Push("c")
			return err
		})
		must(t, harness.Session.UnloadDocuments())
		expectEqual(t, snapshot(t, harness.Session, liveDoc, harness.conversationId).Value("items"), []any{"a", "b", "c"})
	})

	t.Run("delivers complete publications synchronously after adoption", func(t *testing.T) {
		harness := setupLive(t)
		published := harness.Publications.Len()
		var listenerContext ctxT
		unsubscribe := harness.Session.SubscribeCommits(func(delivered ctxT, _ durable.CommitPublication) { listenerContext = delivered })
		result, err := harness.Session.Commit(ctx, func(tx durable.Tx) (any, error) {
			return "done", mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "m")
		})
		must(t, err)
		if result != "done" || listenerContext != ctx || harness.Publications.Len() != published+1 {
			t.Fatal("the listener runs before Commit returns, with the commit context")
		}
		unsubscribe()
	})

	t.Run("publishes close synchronously and supports unsubscription", func(t *testing.T) {
		// packages/durable/test/session-documents.test.ts:623 (Session.subscribeClose, types.ts:907)
		harness := open()
		var calls []string
		harness.Session.SubscribeClose(func() { calls = append(calls, "active") })
		unsubscribe := harness.Session.SubscribeClose(func() { calls = append(calls, "removed") })
		unsubscribe()
		must(t, harness.Session.Close(ctx))
		expectEqual(t, calls, []string{"active"})
	})

	t.Run("settles admitted commits before close and rejects later admission", func(t *testing.T) {
		harness := setupLive(t)
		gate := harness.Storage.HoldCommits()
		admitted := make(chan error, 1)
		go func() {
			admitted <- tryCommit(harness.Session, func(tx durable.Tx) error {
				return mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "admitted")
			})
		}()
		waitClosed(t, gate.Entered())
		queued := make(chan error, 1)
		go func() {
			queued <- tryCommit(harness.Session, func(tx durable.Tx) error {
				return mustDoc(t, tx, liveDoc, harness.conversationId).Set("message", "queued")
			})
		}()
		waitLineJobs(harness.Harness, 2)
		admittedSnapshot := make(chan obj, 1)
		go func() {
			value, _ := harness.Session.SnapshotErased(ctx, memberDoc, "absent")
			admittedSnapshot <- value
		}()
		waitLineJobs(harness.Harness, 3)
		closed := make(chan error, 1)
		go func() { closed <- harness.Session.Close(ctx) }()
		for !session.IsClosing(harness.Session) {
			runtime.Gosched()
		}
		expectErrorContains(t, tryCommit(harness.Session, func(durable.Tx) error { return nil }), "closed")
		_, err := harness.Session.SnapshotErased(ctx, liveDoc, harness.conversationId)
		expectErrorContains(t, err, "closed")
		gate.Release()
		must(t, <-admitted)
		must(t, <-queued)
		if <-admittedSnapshot != nil {
			t.Fatal("the admitted snapshot settles absent")
		}
		must(t, <-closed)
		_, err = harness.Storage.FindDocument(ctx, durable.DocumentAddress{Kind: "test.live", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: harness.conversationId}}, durable.CurrentPoint)
		if err == nil {
			t.Fatal("Storage is closed")
		}
	})
}
