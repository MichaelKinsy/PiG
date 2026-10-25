package session_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-document-retention.test.ts. The Session's document cache is bounded by use:
// unattached documents leave it after the retention period and load from Storage again; documents with a watch or a
// document state never leave it.
//
// The Pi 1.2.0 cases "follows a Harness settings change at the next use" (:477) and "gives the same events with documents
// evicted right after use as with documents kept" (:497) live in durable/harness/document_retention_test.go. Of the case
// at :426, the "soon" input (a string cast to a number) is a type error in Go, whose retention source returns *float64;
// NaN, -5 and Infinity are ported. The case at :451 replaces globalThis.setTimeout, a JS mechanic, and is skipped below.
// The suite-wide PI_DURABLE_DOCUMENT_RETENTION_MS run is a vitest setup file; the Go Session default is the constant
// 600_000, which setDefaultDocumentRetention(600_000) restores in Pi.

var (
	retentionCounterDoc    = defineDoc("test.retention.counter", 1, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("count", 0) })
	retentionRewindableDoc = defineDoc("test.retention.rewindable", 1, rewindableScope(durable.ForkAsOf), func() obj { return delta.JsonObjectOf("count", 0) })
	// A third-party conversation family: one member per event.
	retentionEventDoc          = defineFamily("test.retention.event", 1, latestScope(durable.ForkInitial), func(seq int) obj { return delta.JsonObjectOf("seq", seq) })
	retentionSessionCounterDoc = defineDoc("test.retention.session-counter", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0) })
)

// retentionClock is a controllable wall clock.
type retentionClock struct {
	now  atomic.Int64
	fail atomic.Bool
}

func (clock *retentionClock) read() float64 {
	if clock.fail.Load() {
		panic(errors.New("clock"))
	}
	return float64(clock.now.Load())
}

func (clock *retentionClock) advance(ms int64) { clock.now.Add(ms) }

// retentionSession is a Session with a controllable clock and retention.
type retentionSession struct {
	storage   *sessiontest.ControlledStorage
	session   *session.SessionImpl
	clock     *retentionClock
	retention atomic.Pointer[float64]
}

func openRetention(retentionMs float64) *retentionSession {
	opened := &retentionSession{storage: sessiontest.NewControlledStorage(), clock: &retentionClock{}}
	opened.clock.now.Store(1_000)
	opened.retention.Store(&retentionMs)
	opened.session = session.NewSessionImpl(opened.storage, session.Hooks{}, opened.clock.read, func() *float64 { return opened.retention.Load() })
	return opened
}

func (opened *retentionSession) sizes() session.DocumentCacheSizes {
	return opened.session.DocumentCacheSizes()
}

// settleLine lets the line run what is queued on it: an eviction job, then returns.
func (opened *retentionSession) settleLine(t *testing.T) {
	t.Helper()
	_, err := opened.session.ReadOnLine(func() (any, error) { return nil, nil })
	must(t, err)
}

// untilCached waits until the cache holds want documents, letting the line run in between.
func (opened *retentionSession) untilCached(t *testing.T, want int) {
	t.Helper()
	eventuallyTrue(t, func() bool {
		opened.settleLine(t)
		return opened.sizes().Cached == want
	})
}

func (opened *retentionSession) conversation(t *testing.T) durable.ConversationId {
	t.Helper()
	return sessiontest.CreateConversation(t, opened.session)
}

func (opened *retentionSession) snapshot(t *testing.T, token durable.AnyDocToken, args ...any) obj {
	t.Helper()
	return snapshot(t, opened.session, token, args...)
}

func (opened *retentionSession) close(t *testing.T) {
	t.Helper()
	must(t, opened.session.Close(ctx))
}

func (opened *retentionSession) bump(t *testing.T, conversationId durable.ConversationId, by int) {
	t.Helper()
	must(t, tryCommit(opened.session, func(tx durable.Tx) error {
		draft := mustDoc(t, tx, retentionCounterDoc, conversationId)
		return draft.Set("count", int(num(draft.Get("count")))+by)
	}))
}

// touchSessionDoc uses a session document, a commit that changes nothing else.
func (opened *retentionSession) touchSessionDoc(t *testing.T) {
	t.Helper()
	commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(retentionSessionCounterDoc); return err })
}

func TestSessionDocumentCacheRetention(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:109 "evicts an unattached document once its retention passed, and loads it again with the same value"
	t.Run("evicts an unattached document once its retention passed, and loads it again with the same value", func(t *testing.T) {
		opened := openRetention(60_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 3)
		if opened.sizes().Cached == 0 {
			t.Fatal("the document is cached")
		}
		// Not yet: inside the retention period.
		opened.clock.advance(59_000)
		opened.bump(t, conversationId, 0)
		opened.settleLine(t)
		if opened.sizes().Cached == 0 {
			t.Fatal("the document stays inside the retention period")
		}
		// Past it, with no use since: the next commit evicts it.
		opened.clock.advance(61_000)
		commit(t, opened.session, func(durable.Tx) error { return nil })
		opened.touchSessionDoc(t)
		opened.settleLine(t)
		reads := opened.storage.DocumentReadCount()
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 3))
		if opened.storage.DocumentReadCount() <= reads {
			t.Fatal("the evicted document loads from Storage again")
		}
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:132 "evicts with retention 0 right after use, on the line"
	t.Run("evicts with retention 0 right after use, on the line", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		opened.untilCached(t, 0)
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 1))
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:144 "never evicts a document a watch observes, and evicts it once the last watch detaches"
	t.Run("never evicts a document a watch observes, and evicts it once the last watch detaches", func(t *testing.T) {
		opened := openRetention(1_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		watch := watchOf(t, opened, retentionCounterDoc, conversationId)
		kept := watchOf(t, opened, retentionCounterDoc, conversationId)
		kept.Start(func(context.Context, obj, []durable.Op) error { return nil })
		seen := &watchRecorder{}
		watch.Start(func(_ context.Context, value obj, ops []durable.Op) error { seen.record(value, ops); return nil })
		opened.clock.advance(1_000_000)
		opened.bump(t, conversationId, 1)
		opened.settleLine(t)
		// Still cached: a read long after its retention passed loads nothing from Storage.
		eventuallyTrue(t, func() bool {
			if value := kept.Value(); value == nil || num(value.Value("count")) != 2 {
				return false
			}
			for _, delivery := range seen.all() {
				if delivery.value != nil && num(delivery.value.Value("count")) == 2 {
					return true
				}
			}
			return false
		})
		reads := opened.storage.DocumentReadCount()
		opened.clock.advance(1_000_000)
		opened.touchSessionDoc(t)
		opened.settleLine(t)
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 2))
		if opened.storage.DocumentReadCount() != reads {
			t.Fatal("an observed document is read from the cache")
		}
		_, err := watch.Stop()
		must(t, err)
		opened.clock.advance(1_000_000)
		opened.bump(t, conversationId, 1)
		opened.settleLine(t)
		eventuallyTrue(t, func() bool {
			value := kept.Value()
			return value != nil && num(value.Value("count")) == 3
		})
		_, _ = kept.Stop()
		opened.clock.advance(1_000_000)
		commit(t, opened.session, func(durable.Tx) error { return nil })
		opened.touchSessionDoc(t)
		opened.settleLine(t)
		// The session document was just used; the counter is gone.
		if cached := opened.sizes().Cached; cached != 1 {
			t.Fatalf("cached %d, want 1", cached)
		}
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:185 "never takes a tracker from a transaction that holds it"
	t.Run("never takes a tracker from a transaction that holds it", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		inside := make(chan struct{})
		release := make(chan struct{})
		committed := make(chan error, 1)
		go func() {
			committed <- tryCommit(opened.session, func(tx durable.Tx) error {
				draft := mustDoc(t, tx, retentionCounterDoc, conversationId)
				close(inside)
				<-release
				return draft.Set("count", int(num(draft.Get("count")))+10)
			})
		}()
		<-inside
		// Retention passes while the transaction holds the document; eviction waits on the line behind it.
		opened.clock.advance(1_000_000)
		close(release)
		must(t, <-committed)
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 11))
		opened.bump(t, conversationId, 1)
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 12))
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:208 "writes after a reload continue the stored deltas and bases"
	t.Run("writes after a reload continue the stored deltas and bases", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		before := len(opened.storage.AdmittedCommits())
		for range 5 {
			opened.bump(t, conversationId, 1)
			opened.settleLine(t)
		}
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 5))
		// Every bump after the first is a change of the same incarnation, never a second creation.
		creations := 0
		for _, writes := range opened.storage.AdmittedCommits()[before:] {
			for _, write := range writes {
				if create, ok := write.(durable.DocumentCreateWrite); ok && create.Record.Kind == "test.retention.counter" {
					creations++
				}
			}
		}
		if creations != 1 {
			t.Fatalf("creations %d, want 1", creations)
		}
		opened.close(t)
		// And a fresh Session over the same storage reads the same value.
		opened.storage.Reopen()
		zero := 0.0
		reopened := session.NewSessionImpl(opened.storage, session.Hooks{}, nil, func() *float64 { return &zero })
		expectEqual(t, snapshot(t, reopened, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 5))
		must(t, reopened.Close(ctx))
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:234 "keeps history and forks of rewindable documents after eviction"
	t.Run("keeps history and forks of rewindable documents after eviction", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		appendCount := func(count int) durable.EntryId {
			var id durable.EntryId
			commit(t, opened.session, func(tx durable.Tx) error {
				must(t, mustDoc(t, tx, retentionRewindableDoc, conversationId).Set("count", count))
				id = appendPoint(t, tx, conversationId, "note")
				return nil
			})
			opened.settleLine(t)
			return id
		}
		first := appendCount(1)
		second := appendCount(2)
		asOf := func(at durable.EntryId) obj {
			value, err := opened.session.SnapshotAsOfErased(ctx, retentionRewindableDoc, at, conversationId)
			must(t, err)
			return value
		}
		expectEqual(t, asOf(first), delta.JsonObjectOf("count", 1))
		expectEqual(t, asOf(second), delta.JsonObjectOf("count", 2))
		var forked durable.ConversationId
		commit(t, opened.session, func(tx durable.Tx) error {
			child, err := tx.ForkConversation(conversationId, first, ownerless())
			forked = child.Id
			return err
		})
		opened.settleLine(t)
		expectEqual(t, opened.snapshot(t, retentionRewindableDoc, forked), delta.JsonObjectOf("count", 1))
		expectEqual(t, opened.snapshot(t, retentionRewindableDoc, conversationId), delta.JsonObjectOf("count", 2))
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:259 "bounds a third-party family with a member per event"
	t.Run("bounds a third-party family with a member per event", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		for seq := range 200 {
			commit(t, opened.session, func(tx durable.Tx) error {
				_, err := tx.Doc(retentionEventDoc, conversationId, strconv.Itoa(seq), seq)
				return err
			})
		}
		opened.untilCached(t, 0)
		expectEqual(t, opened.snapshot(t, retentionEventDoc, conversationId, "123"), delta.JsonObjectOf("seq", 123))
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:275 "keeps the cache of a long-lived Session bounded across many conversations"
	t.Run("keeps the cache of a long-lived Session bounded across many conversations", func(t *testing.T) {
		opened := openRetention(10_000)
		for range 300 {
			conversationId := opened.conversation(t)
			opened.bump(t, conversationId, 1)
			opened.clock.advance(1_000)
		}
		opened.settleLine(t)
		// Only what was used in the last 10 s, about ten conversations' worth.
		if cached := opened.sizes().Cached; cached > 12 {
			t.Fatalf("cached %d, want at most 12", cached)
		}
		opened.close(t)
	})

	t.Run("moves a hot document to the back at most once per granule, so it may leave a granule early", func(t *testing.T) {
		opened := openRetention(10_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		// Inside the granule, a tenth of the retention up to 1 s: the use does not move the document back.
		opened.clock.advance(900)
		opened.bump(t, conversationId, 1)
		// 9.2 s after the last use, but 10.1 s after the use that counted.
		opened.clock.advance(9_200)
		opened.touchSessionDoc(t)
		opened.settleLine(t)
		if cached := opened.sizes().Cached; cached != 1 {
			t.Fatalf("cached %d, want only the session document", cached)
		}
		opened.close(t)
	})

	t.Run("keeps a document a commit changed, whatever time the commit took", func(t *testing.T) {
		opened := openRetention(10_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 0)
		commit(t, opened.session, func(tx durable.Tx) error {
			draft := mustDoc(t, tx, retentionCounterDoc, conversationId)
			opened.clock.advance(20_000)
			return draft.Set("count", 1)
		})
		opened.settleLine(t)
		if cached := opened.sizes().Cached; cached != 1 {
			t.Fatalf("cached %d, want the document the commit changed", cached)
		}
		opened.close(t)
	})

	t.Run("closing empties the cache, observed documents included", func(t *testing.T) {
		opened := openRetention(600_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		_, err := opened.session.WatchDocErased(ctx, retentionCounterDoc, conversationId)
		must(t, err)
		opened.close(t)
		if sizes := opened.sizes(); sizes.Cached != 0 || sizes.Idle != 0 || sizes.Attached != 0 {
			t.Fatalf("sizes %+v, want an empty cache", sizes)
		}
	})

	t.Run("unloading drops the idle documents with the cache and keeps the pins", func(t *testing.T) {
		opened := openRetention(600_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		opened.touchSessionDoc(t)
		if idle := opened.sizes().Idle; idle != 2 {
			t.Fatalf("idle %d, want 2", idle)
		}
		watch, err := opened.session.WatchDocErased(ctx, retentionCounterDoc, conversationId)
		must(t, err)
		must(t, opened.session.UnloadDocuments())
		if sizes := opened.sizes(); sizes.Cached != 0 || sizes.Idle != 0 || sizes.Attached != 1 {
			t.Fatalf("sizes %+v, want nothing cached or idle and one pinned incarnation", sizes)
		}
		_, _ = watch.Stop()
		if attached := opened.sizes().Attached; attached != 0 {
			t.Fatalf("attached %d, want 0", attached)
		}
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:288 "never changes the outcome of a commit when the clock throws"
	t.Run("never changes the outcome of a commit when the clock throws", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		opened.clock.fail.Store(true)
		opened.bump(t, conversationId, 42)
		opened.clock.fail.Store(false)
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 42))
		opened.close(t)
	})

	t.Run("never changes the outcome of a commit when the retention source throws, and uses the default", func(t *testing.T) {
		storage := sessiontest.NewControlledStorage()
		kernel := session.NewSessionImpl(storage, session.Hooks{}, nil, func() *float64 { panic(errors.New("settings")) })
		conversationId := sessiontest.CreateConversation(t, kernel)
		must(t, tryCommit(kernel, func(tx durable.Tx) error {
			return mustDoc(t, tx, retentionCounterDoc, conversationId).Set("count", 7)
		}))
		reads := storage.DocumentReadCount()
		expectEqual(t, snapshot(t, kernel, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 7))
		if storage.DocumentReadCount() != reads {
			t.Fatal("the ten-minute default keeps the document cached")
		}
		must(t, kernel.Close(ctx))
	})

	t.Run("createSession takes the retention and the clock as options", func(t *testing.T) {
		// upstream: packages/durable/src/session/session.ts:createSession
		storage := sessiontest.NewControlledStorage()
		clock := &retentionClock{}
		clock.now.Store(1_000)
		keep := math.Inf(1)
		kept := session.CreateSession(storage, session.CreateSessionOptions{Now: clock.read, DocumentRetentionMs: &keep})
		zero := 0.0
		dropped := session.CreateSession(sessiontest.NewControlledStorage(), session.CreateSessionOptions{Now: clock.read, DocumentRetentionMs: &zero})
		defaulted := session.CreateSession(sessiontest.NewControlledStorage())
		for name, kernel := range map[string]*session.SessionImpl{"kept": kept, "dropped": dropped, "defaulted": defaulted} {
			conversationId := sessiontest.CreateConversation(t, kernel)
			must(t, tryCommit(kernel, func(tx durable.Tx) error {
				return mustDoc(t, tx, retentionCounterDoc, conversationId).Set("count", 1)
			}))
			clock.advance(1e12)
			_, err := kernel.ReadOnLine(func() (any, error) { return nil, nil })
			must(t, err)
			want := 1
			if name == "dropped" {
				want = 0
			}
			eventuallyTrue(t, func() bool {
				_, err := kernel.ReadOnLine(func() (any, error) { return nil, nil })
				must(t, err)
				return kernel.DocumentCacheSizes().Cached == want
			})
			must(t, kernel.Close(ctx))
		}
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:307 "evicts after a commit whose settlement fails"
	t.Run("evicts after a commit whose settlement fails", func(t *testing.T) {
		opened := openRetention(10_000)
		failing := defineDoc("test.retention.failing", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0) },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { panic(errors.New("checkpoint failed")) }))
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		// Created first: the predicate runs only for changes to an existing document.
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(failing); return err })
		opened.settleLine(t)
		// Retention passes inside a commit whose checkpoint predicate throws while it settles.
		err := tryCommit(opened.session, func(tx durable.Tx) error {
			draft := mustDoc(t, tx, failing)
			must(t, draft.Set("count", 1))
			opened.clock.advance(1_000_000)
			return nil
		})
		expectErrorContains(t, err, "checkpoint failed")
		if cached := opened.sizes().Cached; cached != 0 {
			t.Fatalf("cached %d, want 0", cached)
		}
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:325 "evicts promptly once the retention is shortened"
	t.Run("evicts promptly once the retention is shortened", func(t *testing.T) {
		opened := openRetention(600_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		zero := 0.0
		opened.retention.Store(&zero)
		opened.clock.advance(2_000)
		// A read, no commit: the sweep pending for ten minutes from now moves up.
		opened.snapshot(t, retentionCounterDoc, conversationId)
		opened.untilCached(t, 0)
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:347 "does not keep a replacement cached for an observer of the retired incarnation"
	t.Run("does not keep a replacement cached for an observer of the retired incarnation", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		watch := watchOf(t, opened, retentionCounterDoc, conversationId)
		watch.Start(func(context.Context, obj, []durable.Op) error { return nil })
		commit(t, opened.session, func(tx durable.Tx) error { return tx.RetireDoc(retentionCounterDoc, conversationId) })
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchRetired {
			t.Fatalf("end %+v, want retired", end)
		}
		if watch.Value() != nil {
			t.Fatalf("watch value %v, want retired", watch.Value())
		}
		// A retired incarnation never changes again: its observer pins nothing.
		if attached := opened.sizes().Attached; attached != 0 {
			t.Fatalf("attached %d, want 0", attached)
		}
		opened.bump(t, conversationId, 5)
		opened.untilCached(t, 0)
		expectEqual(t, opened.snapshot(t, retentionCounterDoc, conversationId), delta.JsonObjectOf("count", 5))
		_, _ = watch.Stop()
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:364 "leaves no pin behind when an observer's load loses a race with close"
	t.Run("leaves no pin behind when an observer's load loses a race with close", func(t *testing.T) {
		opened := openRetention(0)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		opened.untilCached(t, 0)
		gate := opened.storage.HoldFindDocument()
		attaching := make(chan error, 1)
		go func() {
			_, err := opened.session.WatchDocErased(ctx, retentionCounterDoc, conversationId)
			attaching <- err
		}()
		waitClosed(t, gate.Entered())
		closing := make(chan error, 1)
		go func() { closing <- opened.session.Close(ctx) }()
		for !session.IsClosing(opened.session) {
			runtime.Gosched()
		}
		gate.Release()
		if err := <-attaching; err == nil {
			t.Fatal("the attachment that lost the race to close fails")
		}
		must(t, <-closing)
		if sizes := opened.sizes(); sizes.Cached != 0 || sizes.Idle != 0 || sizes.Attached != 0 {
			t.Fatalf("sizes %+v, want an empty cache", sizes)
		}
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:382 "evicts on its timer without a commit, and tolerates a stop called twice"
	t.Run("evicts on its timer without a commit, and tolerates a stop called twice", func(t *testing.T) {
		storage := sessiontest.NewControlledStorage()
		twenty := 20.0
		kernel := session.NewSessionImpl(storage, session.Hooks{}, nil, func() *float64 { return &twenty })
		conversationId := sessiontest.CreateConversation(t, kernel)
		must(t, tryCommit(kernel, func(tx durable.Tx) error {
			return mustDoc(t, tx, retentionCounterDoc, conversationId).Set("count", 1)
		}))
		watch, err := kernel.WatchDocErased(ctx, retentionCounterDoc, conversationId)
		must(t, err)
		_, _ = watch.Stop()
		_, _ = watch.Stop()
		deadline := time.Now().Add(2 * time.Second)
		for kernel.DocumentCacheSizes().Cached > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if cached := kernel.DocumentCacheSizes().Cached; cached != 0 {
			t.Fatalf("cached %d, want 0", cached)
		}
		must(t, kernel.Close(ctx))
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:397 "migrates a document again after its eviction"
	t.Run("migrates a document again after its eviction", func(t *testing.T) {
		v1 := defineDoc("test.retention.migrating", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 1) })
		var migrations atomic.Int32
		v2 := defineDoc("test.retention.migrating", 2, sessionScope, func() obj { return delta.JsonObjectOf("count", 0, "migrated", true) },
			withMigrate(func(value obj, _ int) obj {
				migrations.Add(1)
				return delta.JsonObjectOf("count", value.Value("count"), "migrated", true)
			}))
		opened := openRetention(0)
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(v1); return err })
		opened.settleLine(t)
		expectEqual(t, opened.snapshot(t, v2), delta.JsonObjectOf("count", 1, "migrated", true))
		opened.settleLine(t)
		expectEqual(t, opened.snapshot(t, v2), delta.JsonObjectOf("count", 1, "migrated", true))
		// Not written back, so each cold load migrates again.
		if migrations.Load() != 2 {
			t.Fatalf("migrations %d, want 2", migrations.Load())
		}
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:426 "treats a retention that is not a number of milliseconds as the default, and Infinity as never"
	t.Run("treats a retention that is not a number of milliseconds as the default, and Infinity as never", func(t *testing.T) {
		for _, retention := range []float64{math.NaN(), -5} {
			opened := openRetention(retention)
			conversationId := opened.conversation(t)
			opened.bump(t, conversationId, 1)
			opened.clock.advance(60_000)
			commit(t, opened.session, func(durable.Tx) error { return nil })
			opened.settleLine(t)
			// Not dropped at once, as a retention of 0 would: the ten-minute default holds it.
			if cached := opened.sizes().Cached; cached != 1 {
				t.Fatalf("retention %v: cached %d, want 1", retention, cached)
			}
			opened.close(t)
		}
		opened := openRetention(math.Inf(1))
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		opened.clock.advance(1e15)
		commit(t, opened.session, func(durable.Tx) error { return nil })
		opened.settleLine(t)
		if cached := opened.sizes().Cached; cached != 1 {
			t.Fatalf("Infinity: cached %d, want 1", cached)
		}
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:451 "keeps no timer where timers cannot be unreferenced, and evicts on commits"
	t.Run("keeps no timer where timers cannot be unreferenced, and evicts on commits", func(t *testing.T) {
		t.Skip("JS mechanic: the case replaces globalThis.setTimeout with one returning plain numbers without unref (as in Cloudflare Workers); Go has no global timer function to replace and its timers have no unref, so they never keep the process alive")
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:539 "keeps a reloaded newer version cached while an observer of the older one is attached"
	t.Run("keeps a reloaded newer version cached while an observer of the older one is attached", func(t *testing.T) {
		v1 := defineDoc("test.retention.versioned", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 3) })
		v2 := defineDoc("test.retention.versioned", 2, sessionScope, func() obj { return delta.JsonObjectOf("count", 0, "v2", true) },
			withMigrate(func(value obj, _ int) obj { return delta.JsonObjectOf("count", value.Value("count"), "v2", true) }))
		opened := openRetention(0)
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(v1); return err })
		watch, err := opened.session.WatchDocErased(ctx, v1)
		must(t, err)
		expectEqual(t, opened.snapshot(t, v2), delta.JsonObjectOf("count", 3, "v2", true))
		opened.settleLine(t)
		if cached := opened.sizes().Cached; cached != 1 {
			t.Fatalf("cached %d, want 1", cached)
		}
		_, _ = watch.Stop()
		opened.untilCached(t, 0)
		opened.close(t)
	})

	// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:567 "queues one sweep job, not one per document, while a commit holds the line"
	t.Run("queues one sweep job, not one per document, while a commit holds the line", func(t *testing.T) {
		opened := openRetention(0)
		var conversationIds []durable.ConversationId
		for range 50 {
			id := opened.conversation(t)
			opened.bump(t, id, 1)
			conversationIds = append(conversationIds, id)
		}
		opened.untilCached(t, 0)
		before := opened.sizes().Sweeps
		// One commit loads 50 evicted documents from storage, each touched as it loads; the sweep job stays pending while
		// the line is held.
		commit(t, opened.session, func(tx durable.Tx) error {
			for _, id := range conversationIds {
				draft := mustDoc(t, tx, retentionCounterDoc, id)
				must(t, draft.Set("count", int(num(draft.Get("count")))+1))
				time.Sleep(time.Millisecond)
			}
			return nil
		})
		opened.settleLine(t)
		if cached := opened.sizes().Cached; cached != 0 {
			t.Fatalf("cached %d, want 0", cached)
		}
		if jobs := opened.sizes().Sweeps - before; jobs > 2 {
			t.Fatalf("sweep jobs %d, want at most 2", jobs)
		}
		opened.close(t)
	})
}

func watchOf(t *testing.T, opened *retentionSession, token durable.AnyDocToken, args ...any) durable.WatchHandle[obj] {
	t.Helper()
	watch, err := opened.session.WatchDocErased(ctx, token, args...)
	must(t, err)
	if watch == nil {
		t.Fatal("the document has a watch")
	}
	return watch
}

// Cases the Pi suite does not name but session.ts runs: eviction after a callback that failed (#runCommit catch), a
// transaction's read of a cached document as a use (#loadDocument on a hit), and the eviction timer stopped at close
// (#notifyClose).
func TestSessionDocumentCacheRetentionBookkeeping(t *testing.T) {
	t.Run("evicts after a commit whose callback fails", func(t *testing.T) {
		opened := openRetention(10_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		opened.settleLine(t)
		if opened.sizes().Cached == 0 {
			t.Fatal("the document is cached")
		}
		opened.clock.advance(20_000)
		expectErrorContains(t, tryCommit(opened.session, func(durable.Tx) error { return errors.New("callback failed") }), "callback failed")
		if cached := opened.sizes().Cached; cached != 0 {
			t.Fatalf("cached %d, want 0: the failed commit evicts what expired", cached)
		}
		opened.close(t)
	})

	t.Run("counts a transaction's read of a cached document as a use", func(t *testing.T) {
		opened := openRetention(10_000)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		// Past the granule since the write, inside the retention.
		opened.clock.advance(6_000)
		commit(t, opened.session, func(tx durable.Tx) error {
			_ = mustDoc(t, tx, retentionCounterDoc, conversationId).Get("count")
			return nil
		})
		// 12 s after the write, 6 s after the read.
		opened.clock.advance(6_000)
		opened.touchSessionDoc(t)
		opened.settleLine(t)
		if cached := opened.sizes().Cached; cached != 2 {
			t.Fatalf("cached %d, want the document read 6 s ago and the session document", cached)
		}
		opened.close(t)
	})

	t.Run("stops its eviction timer when the Session closes", func(t *testing.T) {
		opened := openRetention(50)
		conversationId := opened.conversation(t)
		opened.bump(t, conversationId, 1)
		opened.close(t)
		queued := opened.sizes().Sweeps
		time.Sleep(300 * time.Millisecond)
		if after := opened.sizes().Sweeps; after != queued {
			t.Fatalf("%d sweep jobs were queued after close, want none", after-queued)
		}
	})
}
