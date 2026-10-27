package session_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Deferred commits (durable.CommitPersistence): what a deferred commit holds in memory, what stores it, and what Storage and
// each kind of listener then sees. Ports packages/durable/test/session-deferred-commits.test.ts (Pi 1.4.0+6c2fd73b). The
// test that a held revision is garbage-collected once stored has no Go counterpart: it reads V8's WeakRef and gc().

func textDoc() durable.DocToken[obj] {
	return defineDoc("test.deferred.text", 1, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("text", "") },
		withCheckpoint(func(value obj, _ []durable.Op, _ durable.CheckpointInfo) bool {
			if value.Value("explode") == true {
				panic(errors.New("checkpoint failed"))
			}
			return value.Value("base") == true
		}))
}

// textDocV2 is textDoc at version 2, which a reload migrates to.
func textDocV2() durable.DocToken[obj] {
	return defineDoc("test.deferred.text", 2, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("text", "", "migrated", true) },
		withMigrate(func(value obj, _ int) obj { return delta.JsonObjectOf("text", value.Value("text"), "migrated", true) }))
}

var (
	deferredText       = textDoc()
	deferredTextV2     = textDocV2()
	deferredRewindable = defineDoc("test.deferred.rewindable", 1, rewindableScope(durable.ForkAsOf), func() obj { return delta.JsonObjectOf("text", "") })
	deferredCurrent    = defineDoc("test.deferred.current-fork", 1, latestScope(durable.ForkCurrent), func() obj { return delta.JsonObjectOf("text", "") })
	deferredSessionDoc = defineDoc("test.deferred.session", 1, sessionScope, func() obj { return delta.JsonObjectOf("text", "") })
)

var (
	deferredCommit  = durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred}}
	immediateCommit = durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitImmediate}}
)

func within(ms float64) durable.CommitOptions {
	return durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: &ms}}
}

// armedTimer is one foreground timer the Session armed.
type armedTimer struct {
	callback func()
	delayMs  float64
	disarmed bool
	fired    bool
}

// deferredSession is a Session over controlled storage with a manual clock and manual foreground timers (background ones are
// declined, so commits evict), recording what SubscribeCommits and the Session's own observers get.
type deferredSession struct {
	t              *testing.T
	clock          *retentionClock
	storage        *sessiontest.ControlledStorage
	session        *session.SessionImpl
	mu             sync.Mutex
	timers         []*armedTimer
	stored         []durable.CommitPublication
	observed       []session.ObservedCommit
	conversationId durable.ConversationId
	recordId       durable.DocumentId
}

type deferredOptions struct {
	retentionMs     float64
	persistWithinMs *float64
}

func openDeferred(t *testing.T, options ...deferredOptions) *deferredSession {
	t.Helper()
	opts := deferredOptions{retentionMs: 600_000}
	if len(options) > 0 {
		opts = options[0]
	}
	opened := &deferredSession{t: t, clock: &retentionClock{}, storage: sessiontest.NewControlledStorage()}
	opened.clock.now.Store(1_000)
	retention := opts.retentionMs
	opened.session = session.NewSessionImpl(opened.storage, session.Hooks{
		SetTimer: func(callback func(), delayMs float64, background bool) func() {
			if background {
				return nil
			}
			timer := &armedTimer{callback: callback, delayMs: delayMs}
			opened.mu.Lock()
			opened.timers = append(opened.timers, timer)
			opened.mu.Unlock()
			return func() {
				opened.mu.Lock()
				timer.disarmed = true
				opened.mu.Unlock()
			}
		},
		PersistWithinMs: func() *float64 { return opts.persistWithinMs },
	}, opened.clock.read, func() *float64 { return &retention })
	opened.session.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		opened.mu.Lock()
		opened.stored = append(opened.stored, publication)
		opened.mu.Unlock()
	})
	opened.session.ObserveCommits(func(_ context.Context, commit session.ObservedCommit) {
		opened.mu.Lock()
		opened.observed = append(opened.observed, commit)
		opened.mu.Unlock()
	})
	opened.conversationId = sessiontest.CreateConversation(t, opened.session)
	commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(deferredText, opened.conversationId); return err })
	opened.recordId = opened.documentId(deferredText.AnyDefinition().Kind, opened.conversationId)
	return opened
}

func (opened *deferredSession) documentId(kind string, conversationId durable.ConversationId) durable.DocumentId {
	opened.t.Helper()
	record, err := opened.storage.FindDocument(ctx, durable.DocumentAddress{Kind: kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}}, durable.CurrentPoint)
	if err != nil || record == nil {
		opened.t.Fatalf("document %s of conversation %d: %v, %v", kind, conversationId, record, err)
	}
	return record.Id
}

// armed is the timers armed and neither disarmed nor fired.
func (opened *deferredSession) armed() []float64 {
	opened.mu.Lock()
	defer opened.mu.Unlock()
	delays := []float64{}
	for _, timer := range opened.timers {
		if !timer.disarmed && !timer.fired {
			delays = append(delays, timer.delayMs)
		}
	}
	return delays
}

// fire fires every armed timer and lets the line run what they queued.
func (opened *deferredSession) fire() {
	opened.t.Helper()
	opened.mu.Lock()
	var due []*armedTimer
	for _, timer := range opened.timers {
		if !timer.disarmed && !timer.fired {
			timer.fired = true
			due = append(due, timer)
		}
	}
	opened.mu.Unlock()
	for _, timer := range due {
		timer.callback()
	}
	_, err := opened.session.ReadOnLine(func() (any, error) { return nil, nil })
	must(opened.t, err)
}

// mark returns the batches admitted to Storage from here on.
func (opened *deferredSession) mark() func() [][]durable.StorageWrite {
	from := len(opened.storage.Commits())
	return func() [][]durable.StorageWrite { return opened.storage.Commits()[from:] }
}

func (opened *deferredSession) storedValue(id durable.DocumentId) obj {
	opened.t.Helper()
	opened.storage.Reopen()
	document, err := opened.storage.Document(ctx, id, durable.CurrentPoint)
	must(opened.t, err)
	if document == nil {
		return nil
	}
	return document.Value
}

func (opened *deferredSession) storedText() any {
	return opened.storedValue(opened.recordId).Value("text")
}

func (opened *deferredSession) sizes() session.DocumentCacheSizes {
	return opened.session.DocumentCacheSizes()
}

func (opened *deferredSession) storedPublications() []durable.CommitPublication {
	opened.mu.Lock()
	defer opened.mu.Unlock()
	return slices.Clone(opened.stored)
}

func (opened *deferredSession) observedCommits() []session.ObservedCommit {
	opened.mu.Lock()
	defer opened.mu.Unlock()
	return slices.Clone(opened.observed)
}

func (opened *deferredSession) snapshotText(token durable.AnyDocToken) any {
	opened.t.Helper()
	return snapshot(opened.t, opened.session, token, opened.conversationId).Value("text")
}

func (opened *deferredSession) tryAppend(text string, options ...durable.CommitOptions) error {
	if len(options) == 0 {
		options = []durable.CommitOptions{deferredCommit}
	}
	_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
		draft, err := tx.Doc(deferredText, opened.conversationId)
		if err != nil {
			return nil, err
		}
		return nil, delta.AppendText(draft, "text", text)
	}, options...)
	return err
}

func (opened *deferredSession) append(text string, options ...durable.CommitOptions) {
	opened.t.Helper()
	must(opened.t, opened.tryAppend(text, options...))
}

// commitDeferred runs change as a deferred commit and reports whether Storage was written before it returned.
func (opened *deferredSession) stores(change func(tx durable.Tx) error) bool {
	opened.t.Helper()
	admitted := len(opened.storage.Commits())
	_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) { return nil, change(tx) }, deferredCommit)
	must(opened.t, err)
	return len(opened.storage.Commits()) > admitted
}

func writeType(write durable.StorageWrite) string {
	switch write.(type) {
	case durable.DocumentChangeWrite:
		return "document.change"
	case durable.DocumentRetireWrite:
		return "document.retire"
	case durable.DocumentCreateWrite:
		return "document.create"
	case durable.EntryWrite:
		return "entry"
	}
	return "other"
}

func changesOf(batches [][]durable.StorageWrite) []durable.DocumentChangeWrite {
	var changes []durable.DocumentChangeWrite
	for _, batch := range batches {
		for _, write := range batch {
			if change, ok := write.(durable.DocumentChangeWrite); ok {
				changes = append(changes, change)
			}
		}
	}
	return changes
}

func contentKinds(batches [][]durable.StorageWrite) []durable.DocumentContentKind {
	kinds := []durable.DocumentContentKind{}
	for _, change := range changesOf(batches) {
		kinds = append(kinds, change.Content.Kind)
	}
	return kinds
}

func appendOps(text string) []durable.Op { return []durable.Op{{"a", []any{"text"}, text}} }

func TestHoldingADeferredCommit(t *testing.T) {
	t.Run("adopts a commit that only changes an existing latest, initial-fork conversation document in memory, unstored", func(t *testing.T) {
		opened := openDeferred(t)
		admitted, publications := len(opened.storage.Commits()), len(opened.storedPublications())
		opened.append("a")
		opened.append("b")
		if got := len(opened.storage.Commits()); got != admitted {
			t.Fatalf("Storage admitted %d batches, want %d", got, admitted)
		}
		if got := len(opened.storedPublications()); got != publications {
			t.Fatalf("subscribeCommits saw %d publications, want %d", got, publications)
		}
		expectEqual(t, opened.snapshotText(deferredText), "ab")
		expectEqual(t, opened.storedText(), "")
		if held := opened.sizes().Held; held != 1 {
			t.Fatalf("held = %d, want 1", held)
		}
		observed := opened.observedCommits()
		last := observed[len(observed)-2:]
		if !last[0].Memory || !last[1].Memory {
			t.Fatalf("the Session's observers saw %+v, want two memory commits", last)
		}
	})

	t.Run("stores a deferred commit at once when it writes anything else", func(t *testing.T) {
		opened := openDeferred(t)
		conversationId := opened.conversationId
		// An entry next to the held document's change.
		if !opened.stores(func(tx durable.Tx) error {
			draft, err := tx.Doc(deferredText, conversationId)
			if err != nil {
				return err
			}
			if err := delta.AppendText(draft, "text", "x"); err != nil {
				return err
			}
			_, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "test.note"})
			return err
		}) {
			t.Fatal("a commit with an entry was held")
		}
		// Creation, then other document semantics.
		set := func(token durable.AnyDocToken, args ...any) func(tx durable.Tx) error {
			return func(tx durable.Tx) error { return mustDoc(t, tx, token, args...).Set("text", "v") }
		}
		touch := func(token durable.AnyDocToken, args ...any) func(tx durable.Tx) error {
			return func(tx durable.Tx) error { _, err := tx.Doc(token, args...); return err }
		}
		for _, step := range []struct {
			name   string
			change func(tx durable.Tx) error
		}{
			{"creating a rewindable document", touch(deferredRewindable, conversationId)},
			{"changing a rewindable document", set(deferredRewindable, conversationId)},
			{"creating a current-fork document", touch(deferredCurrent, conversationId)},
			{"changing a current-fork document", set(deferredCurrent, conversationId)},
			{"creating a session document", touch(deferredSessionDoc)},
			{"changing a session document", set(deferredSessionDoc)},
			// A held document next to an immediate one: the whole commit is stored.
			{"a held document next to a session document", func(tx durable.Tx) error {
				draft, err := tx.Doc(deferredText, conversationId)
				if err != nil {
					return err
				}
				if err := delta.AppendText(draft, "text", "y"); err != nil {
					return err
				}
				return mustDoc(t, tx, deferredSessionDoc).Set("text", "w")
			}},
		} {
			if !opened.stores(step.change) {
				t.Fatalf("%s was held", step.name)
			}
		}
		// Retirement.
		if !opened.stores(func(tx durable.Tx) error { return tx.RetireDoc(deferredCurrent, conversationId) }) {
			t.Fatal("a retirement was held")
		}
		if held := opened.sizes().Held; held != 0 {
			t.Fatalf("held = %d, want 0", held)
		}
		// The control: the same change alone is held.
		if opened.stores(func(tx durable.Tx) error {
			return delta.AppendText(mustDoc(t, tx, deferredText, conversationId), "text", "z")
		}) {
			t.Fatal("the control commit was stored at once")
		}
	})

	t.Run("stores a deferred commit that migrates its document at once, as a base", func(t *testing.T) {
		opened := openDeferred(t)
		batches := opened.mark()
		_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			return nil, mustDoc(t, tx, deferredTextV2, opened.conversationId).Set("text", "v2")
		}, deferredCommit)
		must(t, err)
		expectEqual(t, contentKinds(batches()), []durable.DocumentContentKind{durable.ContentBase})
		if held := opened.sizes().Held; held != 0 {
			t.Fatalf("held = %d, want 0", held)
		}
	})

	t.Run("rejects a deferred commit on a read-only Session like any commit with writes", func(t *testing.T) {
		opened := openDeferred(t)
		viewer := session.NewSessionImpl(opened.storage, session.Hooks{ReadOnly: true}, nil, nil)
		err := func() error {
			_, err := viewer.Commit(ctx, func(tx durable.Tx) (any, error) {
				return nil, delta.AppendText(mustDoc(t, tx, deferredText, opened.conversationId), "text", "a")
			}, deferredCommit)
			return err
		}()
		if _, ok := errors.AsType[*durable.ReadOnlySession](err); !ok {
			t.Fatalf("a read-only Session returned %v, want ReadOnlySession", err)
		}
	})

	t.Run("rejects a withinMs that is not a number of milliseconds, 0 or more", func(t *testing.T) {
		opened := openDeferred(t)
		expectErrorContains(t, opened.tryAppend("a", within(-1)), "withinMs")
		expectErrorContains(t, opened.tryAppend("a", within(math.NaN())), "withinMs")
		expectErrorContains(t, opened.tryAppend("a", deferredCommit, deferredCommit), "at most one")
		must(t, opened.tryAppend("a", immediateCommit))
	})
}

func TestStoringHeldChanges(t *testing.T) {
	t.Run("stores them first, folded with its own, in a commit that writes the document", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("a")
		opened.append("b")
		batches := opened.mark()
		opened.append("c", immediateCommit)
		if got := len(batches()); got != 1 {
			t.Fatalf("%d batches, want 1", got)
		}
		changes := changesOf(batches())
		if len(changes) != 1 {
			t.Fatalf("%d document changes, want 1", len(changes))
		}
		expectEqual(t, changes[0].Content, durable.DocumentContent{Version: 1, Kind: durable.ContentDelta, Ops: appendOps("abc")})
		expectEqual(t, opened.storedText(), "abc")
		if held := opened.sizes().Held; held != 0 {
			t.Fatalf("held = %d, want 0", held)
		}
	})

	t.Run("stores them in a commit that only reads the document, with its other writes", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		batches := opened.mark()
		_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			text, _ := mustDoc(t, tx, deferredText, opened.conversationId).Get("text").(string)
			_, err := tx.AppendEntry(opened.conversationId, durable.EntryDraft{Kind: "test.derived", Data: text})
			return nil, err
		})
		must(t, err)
		if got := len(batches()); got != 1 {
			t.Fatalf("%d batches, want 1", got)
		}
		types := []string{}
		for _, write := range batches()[0] {
			types = append(types, writeType(write))
		}
		slices.Sort(types)
		expectEqual(t, types, []string{"document.change", "entry"})
		expectEqual(t, opened.storedText(), "held")
	})

	t.Run("leaves them held through commits that neither read nor write the document, or write nothing", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		batches := opened.mark()
		commit(t, opened.session, func(tx durable.Tx) error {
			_, err := tx.AppendEntry(opened.conversationId, durable.EntryDraft{Kind: "test.unrelated"})
			return err
		})
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(deferredText, opened.conversationId); return err })
		if changes := changesOf(batches()); len(changes) != 0 {
			t.Fatalf("stored %+v, want no document change", changes)
		}
		if held := opened.sizes().Held; held != 1 {
			t.Fatalf("held = %d, want 1", held)
		}
	})

	t.Run("keeps them when a commit that reads the document fails before Storage", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		callbackFailed := errors.New("callback failed")
		_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			if err := delta.AppendText(mustDoc(t, tx, deferredText, opened.conversationId), "text", "!"); err != nil {
				return nil, err
			}
			if _, err := tx.AppendEntry(opened.conversationId, durable.EntryDraft{Kind: "test.note"}); err != nil {
				return nil, err
			}
			return nil, callbackFailed
		})
		if !errors.Is(err, callbackFailed) {
			t.Fatalf("the failing commit returned %v", err)
		}
		// Its checkpoint predicate panics: settlement fails after every other step.
		err = tryCommit(opened.session, func(tx durable.Tx) error {
			return mustDoc(t, tx, deferredText, opened.conversationId).Set("explode", true)
		})
		expectErrorContains(t, err, "checkpoint failed")
		if held := opened.sizes().Held; held != 1 {
			t.Fatalf("held = %d, want 1", held)
		}
		opened.fire()
		expectEqual(t, opened.storedText(), "held")
	})

	t.Run("stores every held document in one transaction when the earliest deadline comes", func(t *testing.T) {
		opened := openDeferred(t)
		other := sessiontest.CreateConversation(t, opened.session)
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(deferredText, other); return err })
		opened.append("one")
		must(t, func() error {
			_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
				return nil, delta.AppendText(mustDoc(t, tx, deferredText, other), "text", "two")
			}, deferredCommit)
			return err
		}())
		expectEqual(t, opened.armed(), []float64{1_000})
		publications := len(opened.storedPublications())
		batches := opened.mark()
		opened.fire()
		if got := len(batches()); got != 1 {
			t.Fatalf("%d batches, want 1", got)
		}
		if got := len(changesOf(batches())); got != 2 {
			t.Fatalf("%d document changes, want 2", got)
		}
		if got := len(opened.storedPublications()); got != publications+1 {
			t.Fatalf("%d publications, want %d", got, publications+1)
		}
		if held := opened.sizes().Held; held != 0 {
			t.Fatalf("held = %d, want 0", held)
		}
		expectEqual(t, opened.armed(), []float64{})
		expectEqual(t, opened.storedValue(opened.documentId(deferredText.AnyDefinition().Kind, other)).Value("text"), "two")
	})

	t.Run("arms the earliest deadline of the held commits, each with its withinMs or the default", func(t *testing.T) {
		two := 2_000.0
		opened := openDeferred(t, deferredOptions{retentionMs: 600_000, persistWithinMs: &two})
		opened.append("a")
		expectEqual(t, opened.armed(), []float64{2_000})
		opened.clock.advance(500)
		// Earlier: the timer moves up.
		opened.append("b", within(100))
		expectEqual(t, opened.armed(), []float64{100})
		// Later: the deadline stays.
		opened.append("c", within(60_000))
		expectEqual(t, opened.armed(), []float64{100})
	})

	t.Run("arms no timer for an infinite window, and moves the timer on when a commit stores the held document", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("a", within(math.Inf(1)))
		expectEqual(t, opened.armed(), []float64{})
		if held := opened.sizes().Held; held != 1 {
			t.Fatalf("held = %d, want 1", held)
		}
		opened.append("b")
		expectEqual(t, opened.armed(), []float64{1_000})
		opened.append("c", immediateCommit)
		expectEqual(t, opened.armed(), []float64{})
		if held := opened.sizes().Held; held != 0 {
			t.Fatalf("held = %d, want 0", held)
		}
	})

	t.Run("stores them at close, after the commits admitted before it, and closes Storage when that store fails", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		must(t, opened.session.Close(ctx))
		expectEqual(t, opened.storedText(), "held")

		failing := openDeferred(t)
		failing.append("lost")
		failure := errors.New("disk gone")
		failing.storage.FailNextCommit(failure)
		if err := failing.session.Close(ctx); !errors.Is(err, failure) {
			t.Fatalf("close returned %v, want the storage error", err)
		}
		<-failing.session.Closed()
		if end := failing.session.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, failure) {
			t.Fatalf("end = %+v, want failed with the storage error", end)
		}
		expectEqual(t, failing.storedText(), "")
	})

	t.Run("stores nothing once the Session failed", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("lost")
		batches := opened.mark()
		opened.session.Fail(ctx, errors.New("crash"))
		<-opened.session.Closed()
		if got := len(batches()); got != 0 {
			t.Fatalf("a failed Session stored %d batches", got)
		}
		expectEqual(t, opened.storedText(), "")
	})

	t.Run("stores them before UnloadDocuments drops the trackers", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		must(t, opened.session.UnloadDocuments())
		expectEqual(t, opened.storedText(), "held")
		if sizes := opened.sizes(); sizes.Cached != 0 || sizes.Held != 0 {
			t.Fatalf("sizes = %+v, want nothing cached or held", sizes)
		}
		expectEqual(t, opened.snapshotText(deferredText), "held")
	})

	t.Run("stores them before a token of another definition version reloads the document", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		batches := opened.mark()
		migrated := snapshot(t, opened.session, deferredTextV2, opened.conversationId)
		expectEqual(t, migrated.Value("text"), "held")
		expectEqual(t, migrated.Value("migrated"), true)
		expectEqual(t, contentKinds(batches()), []durable.DocumentContentKind{durable.ContentDelta})
		expectEqual(t, opened.storedText(), "held")
		// The same inside a commit, which then migrates with a base.
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(deferredText, opened.conversationId); return err })
		opened.append("+")
		commit(t, opened.session, func(tx durable.Tx) error {
			return delta.AppendText(mustDoc(t, tx, deferredTextV2, opened.conversationId), "text", "v2")
		})
		stored := opened.storedValue(opened.recordId)
		expectEqual(t, stored.Value("text"), "held+v2")
		expectEqual(t, stored.Value("migrated"), true)
	})

	t.Run("stores them before the document's retirement, in the retiring commit", func(t *testing.T) {
		opened := openDeferred(t)
		opened.append("held")
		batches := opened.mark()
		commit(t, opened.session, func(tx durable.Tx) error { return tx.RetireDoc(deferredText, opened.conversationId) })
		types := []string{}
		for _, write := range batches()[0] {
			types = append(types, writeType(write))
		}
		expectEqual(t, types, []string{"document.change", "document.retire"})
		expectEqual(t, changesOf(batches())[0].Content, durable.DocumentContent{Version: 1, Kind: durable.ContentDelta, Ops: appendOps("held")})
		if held := opened.sizes().Held; held != 0 {
			t.Fatalf("held = %d, want 0", held)
		}
		if value := snapshot(t, opened.session, deferredText, opened.conversationId); value != nil {
			t.Fatalf("snapshot of a retired document = %v", value)
		}
	})
}

// overflow holds more than MaxPendingOps operations: deferred commits of one set per key, each below the tracker's own
// limit, past which a commit is one root replacement.
func overflow(t *testing.T, opened *deferredSession) {
	t.Helper()
	commit(t, opened.session, func(tx durable.Tx) error {
		return mustDoc(t, tx, deferredText, opened.conversationId).Set("keys", delta.JsonObjectOf())
	})
	for from := 0; from <= delta.MaxPendingOps; from += 4_000 {
		_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			keys := mustDoc(t, tx, deferredText, opened.conversationId).Object("keys")
			for index := from; index < from+4_000; index++ {
				if err := keys.Set("k"+strconv.Itoa(index), float64(index)); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}, deferredCommit)
		must(t, err)
	}
}

func TestBasesAndOverflowOfHeldChanges(t *testing.T) {
	t.Run("stores a base when a held commit selected one, and counts the store as one delta otherwise", func(t *testing.T) {
		opened := openDeferred(t)
		batches := opened.mark()
		_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			draft := mustDoc(t, tx, deferredText, opened.conversationId)
			if err := draft.Set("text", "x"); err != nil {
				return nil, err
			}
			return nil, draft.Set("base", true)
		}, deferredCommit)
		must(t, err)
		opened.append("y")
		opened.fire()
		changes := changesOf(batches())
		if len(changes) != 1 || changes[0].Content.Kind != durable.ContentBase {
			t.Fatalf("stored %+v, want one base", changes)
		}
		value := changes[0].Content.Value
		expectEqual(t, value.Value("text"), "xy")
		expectEqual(t, value.Value("base"), true)
	})

	t.Run("stores the whole value once more than MaxPendingOps operations are held", func(t *testing.T) {
		opened := openDeferred(t)
		overflow(t, opened)
		batches := opened.mark()
		opened.fire()
		value := snapshot(t, opened.session, deferredText, opened.conversationId)
		changes := changesOf(batches())
		if len(changes) != 1 || changes[0].Content.Kind != durable.ContentBase || !equal(changes[0].Content.Value, value) {
			t.Fatalf("stored %+v, want one base of the current value", changes)
		}
		// A durable listener replaces its copy with the stored value.
		published := opened.storedPublications()
		last := sessiontest.DocumentChanges(published[len(published)-1])
		if len(last) != 1 || !equal(last[0].Ops, []durable.Op{{"r", value}}) {
			t.Fatalf("the last publication carried %+v, want a root replacement", last)
		}
	})

	t.Run("stores a base of the commit's own value when it stores an overflowed hold", func(t *testing.T) {
		opened := openDeferred(t)
		overflow(t, opened)
		opened.append("a")
		batches := opened.mark()
		opened.append("b", immediateCommit)
		value := snapshot(t, opened.session, deferredText, opened.conversationId)
		expectEqual(t, value.Value("text"), "ab")
		changes := changesOf(batches())
		if len(changes) != 1 || changes[0].Content.Kind != durable.ContentBase || !equal(changes[0].Content.Value, value) {
			t.Fatalf("stored %+v, want one base of the commit's value", changes)
		}
	})
}

func TestPublicationsOfDeferredCommits(t *testing.T) {
	t.Run("give SubscribeCommits exactly what was stored, and nothing for a held commit", func(t *testing.T) {
		opened := openDeferred(t)
		from, publications := len(opened.storage.Commits()), len(opened.storedPublications())
		opened.append("a")
		opened.append("b")
		opened.append("c", immediateCommit)
		opened.append("d")
		opened.fire()
		opened.append("e")
		commit(t, opened.session, func(tx durable.Tx) error {
			if _, err := tx.Doc(deferredText, opened.conversationId); err != nil {
				return err
			}
			_, err := tx.AppendEntry(opened.conversationId, durable.EntryDraft{Kind: "test.note"})
			return err
		})
		opened.append("f")
		must(t, opened.session.Close(ctx))
		batches := opened.storage.Commits()[from:]
		published := opened.storedPublications()[publications:]
		if len(published) != len(batches) {
			t.Fatalf("%d publications for %d batches", len(published), len(batches))
		}
		// A replica built from the publications alone always equals what Storage holds.
		var fromStorage, fromPublications durable.JsonValue = delta.JsonObjectOf("text", ""), delta.JsonObjectOf("text", "")
		kinds := [][]string{}
		for index, batch := range batches {
			var write durable.DocumentChangeWrite
			for _, candidate := range changesOf([][]durable.StorageWrite{batch}) {
				if candidate.Id == opened.recordId {
					write = candidate
				}
			}
			var change durable.DocumentChange
			names := []string{}
			for _, candidate := range published[index].Changes {
				names = append(names, durable.CommitChangeType(candidate))
				if document, ok := candidate.(durable.DocumentChange); ok && document.Record.Id == opened.recordId {
					change = document
				}
			}
			slices.Sort(names)
			kinds = append(kinds, names)
			var err error
			if write.Content.Kind == durable.ContentBase {
				fromStorage = write.Content.Value
			} else if fromStorage, err = delta.ApplyImmutable(fromStorage, write.Content.Ops); err != nil {
				t.Fatal(err)
			}
			if fromPublications, err = delta.ApplyImmutable(fromPublications, change.Ops); err != nil {
				t.Fatal(err)
			}
			if !equal(fromPublications, fromStorage) || !equal(change.Value, fromStorage) {
				t.Fatalf("batch %d: the replica holds %v, Storage %v, the publication's value %v", index, fromPublications, fromStorage, change.Value)
			}
		}
		expectEqual(t, fromStorage.(obj).Value("text"), "abcdef")
		expectEqual(t, kinds, [][]string{{"document"}, {"document"}, {"document", "entry"}, {"document"}})
	})

	t.Run("show every held commit to watches and the Session's own observers, once", func(t *testing.T) {
		opened := openDeferred(t)
		watch, err := opened.session.WatchDocErased(ctx, deferredText, opened.conversationId)
		must(t, err)
		var mu sync.Mutex
		values := []any{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value := change.Value
			mu.Lock()
			values = append(values, value.Value("text"))
			mu.Unlock()
			return nil
		})
		from := len(opened.observedCommits())
		for _, text := range []string{"a", "b", "c"} {
			opened.append(text)
			opened.session.WaitDeliveries()
		}
		opened.append("d", immediateCommit)
		opened.session.WaitDeliveries()
		opened.fire()
		mu.Lock()
		expectEqual(t, values, []any{"a", "ab", "abc", "abcd"})
		mu.Unlock()
		seen := opened.observedCommits()[from:]
		memory := []bool{}
		ops := [][]durable.Op{}
		for _, commit := range seen {
			memory = append(memory, commit.Memory)
			for _, change := range commit.Changes {
				if document, ok := change.(durable.DocumentChange); ok {
					ops = append(ops, document.Ops)
				}
			}
		}
		expectEqual(t, memory, []bool{true, true, true, false})
		// The storing commit shows its own operations; the held ones were shown already. The timer showed nothing.
		expectEqual(t, ops, [][]durable.Op{appendOps("a"), appendOps("b"), appendOps("c"), appendOps("d")})
		if _, err := watch.Stop(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAHeldDocumentIsPinned(t *testing.T) {
	t.Run("keeps a held document cached with retention 0 until it is stored, then lets it go", func(t *testing.T) {
		opened := openDeferred(t, deferredOptions{retentionMs: 0})
		settle := func() {
			_, err := opened.session.ReadOnLine(func() (any, error) { return nil, nil })
			must(t, err)
		}
		settle()
		if sizes := opened.sizes(); sizes.Cached != 0 || sizes.Held != 0 || sizes.Attached != 0 {
			t.Fatalf("sizes = %+v, want an empty cache", sizes)
		}
		opened.append("held")
		settle()
		if sizes := opened.sizes(); sizes.Cached != 1 || sizes.Held != 1 || sizes.Attached != 1 {
			t.Fatalf("sizes = %+v, want the held document cached and pinned", sizes)
		}
		opened.fire()
		commit(t, opened.session, func(durable.Tx) error { return nil })
		if sizes := opened.sizes(); sizes.Cached != 0 || sizes.Held != 0 || sizes.Attached != 0 {
			t.Fatalf("sizes = %+v, want the stored document released", sizes)
		}
		expectEqual(t, opened.snapshotText(deferredText), "held")
	})
}

func TestACrashLeavesStorageAtTheLastStoredState(t *testing.T) {
	opened := openDeferred(t)
	var mu sync.Mutex
	states := []any{}
	opened.session.ObserveCommits(func(_ context.Context, commit session.ObservedCommit) {
		for _, change := range commit.Changes {
			if document, ok := change.(durable.DocumentChange); ok && document.Record.Id == opened.recordId {
				mu.Lock()
				states = append(states, document.Value.Value("text"))
				mu.Unlock()
			}
		}
	})
	opened.append("a")
	opened.fire()
	opened.append("b")
	opened.append("c")
	opened.session.Fail(ctx, errors.New("crash"))
	<-opened.session.Closed()
	opened.storage.Reopen()
	reopened := session.NewSessionImpl(opened.storage, session.Hooks{}, nil, nil)
	value := snapshot(t, reopened, deferredText, opened.conversationId)
	expectEqual(t, value.Value("text"), "a")
	mu.Lock()
	if !slices.Contains(states, any("a")) {
		t.Fatalf("memory never had the stored state: %v", states)
	}
	mu.Unlock()
	if held := reopened.DocumentCacheSizes().Held; held != 0 {
		t.Fatalf("a reopened Session holds %d documents", held)
	}
	must(t, reopened.Close(ctx))
}
