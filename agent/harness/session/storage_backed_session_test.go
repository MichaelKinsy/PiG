package session_test

import (
	"context"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/ai"
)

const backedEntryID = "00000000-0000-7000-8000-000000000001"

var backedMetadata = session.SessionMetadata{ID: "session", CreatedAt: now, StorageVersion: 1, Cwd: "/workspace"}

func fixedMemoryStorage() *session.MemoryStorage {
	return session.NewMemoryStorage(&session.MemoryStorageOptions{Now: fixedNow})
}

func newSessionOn(storage session.Storage) *session.StorageBackedSession {
	return session.NewStorageBackedSession(backedMetadata, storage, nil)
}

func TestStorageBackedSessionDelegatesTypedValuesWithoutValidationOrCloning(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	data := map[string]any{"nested": []any{"original"}}
	var entryData session.JsonValue = data
	state := session.MustValue[any]("test.value", "state")
	transaction := []session.Write{
		session.InsertEntry(session.Entry{ID: backedEntryID, Type: session.EntryTypeCustom, CustomType: "note", Data: &entryData}),
		session.SetValue(state, any(data)),
	}
	result := commitAll(t, backed, transaction...)
	if attempts := storage.GetCommitAttempts(); len(attempts) != 1 || &attempts[0][0] != &transaction[0] {
		t.Fatal("session did not pass the transaction reference to storage")
	}
	entries, err := backed.GetEntries(background, []string{backedEntryID})
	mustNoErr(t, err)
	entry := entries[backedEntryID]
	if entry.Seq != result.Seqs[0] || entry.Timestamp != now || entry.Data != &entryData {
		t.Fatalf("entry = %+v", entry)
	}
	stored, err := session.GetValue(background, backed, state)
	mustNoErr(t, err)
	if reflect.ValueOf(stored.Value).Pointer() != reflect.ValueOf(data).Pointer() {
		t.Fatal("stored value was cloned")
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionComposesValuesAndListsAtomicallyWithEntriesAndUsage(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	scalar := session.MustValue[string]("test.application.scalar", "")
	events := session.MustList[string]("test.application.events", "")
	result := commitAll(t, backed,
		session.InsertEntry(session.Entry{ID: backedEntryID, Type: session.EntryTypeCustom, CustomType: "note"}),
		session.SetValue(scalar, "state"),
		session.AppendList(events, "event"),
		session.InsertUsage(session.UsageRow{ID: "usage", Usage: ai.Usage{Input: 1, Output: 1, TotalTokens: 2}}),
	)
	if len(result.Seqs) != 4 {
		t.Fatalf("seqs = %v", result.Seqs)
	}
	stored, err := session.GetValue(background, backed, scalar)
	mustNoErr(t, err)
	if stored.Seq != result.Seqs[1] {
		t.Fatalf("scalar seq = %d", stored.Seq)
	}
	elements, err := session.ReadList(background, backed, events, nil)
	mustNoErr(t, err)
	if !reflect.DeepEqual(elements, []session.ListElement[string]{{Seq: result.Seqs[2], Value: "event"}}) {
		t.Fatalf("elements = %+v", elements)
	}
	if len(storage.GetCommitAttempts()) != 1 {
		t.Fatalf("attempts = %d", len(storage.GetCommitAttempts()))
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionSerializesReadModifyWriteCallbacks(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	counter := session.MustValue[int]("test.counter", "")
	increment := func() (int, error) {
		return session.Mutate(background, backed, func(ctx context.Context, mutator session.SessionMutator) (int, error) {
			stored, err := session.GetValue(ctx, mutator, counter)
			if err != nil {
				return 0, err
			}
			next := 1
			if stored != nil {
				next = stored.Value + 1
			}
			_, err = mutator.Commit(ctx, []session.Write{session.SetValue(counter, next)})
			return next, err
		})
	}
	results := make([]int, 2)
	var workers sync.WaitGroup
	for index := range results {
		workers.Go(func() {
			value, err := increment()
			if err != nil {
				t.Error(err)
			}
			results[index] = value
		})
	}
	workers.Wait()
	if results[0]+results[1] != 3 || results[0] == results[1] {
		t.Fatalf("results = %v", results)
	}
	stored, err := session.GetValue(background, backed, counter)
	mustNoErr(t, err)
	if stored.Value != 2 {
		t.Fatalf("counter = %d", stored.Value)
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionKeepsSeparateDirectReadsAndWritesNonAtomic(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	counter := session.MustValue[int]("test.counter", "")
	var reads sync.WaitGroup
	reads.Add(2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			stored, err := session.GetValue(background, backed, counter)
			if err != nil {
				t.Error(err)
			}
			next := 1
			if stored != nil {
				next = stored.Value + 1
			}
			reads.Done()
			reads.Wait()
			if err := backed.SetValue(background, counter.StoredAddressBase, next); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	stored, err := session.GetValue(background, backed, counter)
	mustNoErr(t, err)
	if stored.Value != 1 {
		t.Fatalf("counter = %d", stored.Value)
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionQueuesANestedPublicWriterUntilItsCallbackReturns(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	nested := make(chan error, 1)
	_, err := backed.Mutate(background, func(context.Context, session.SessionMutator) (any, error) {
		go func() { nested <- backed.SetName(background, new("nested")) }()
		select {
		case <-nested:
			t.Error("nested writer settled inside its owning callback")
		case <-time.After(20 * time.Millisecond):
		}
		return nil, nil
	})
	mustNoErr(t, err)
	mustNoErr(t, <-nested)
	name, err := backed.GetName(background)
	mustNoErr(t, err)
	if name == nil || *name != "nested" {
		t.Fatalf("name = %v", name)
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionExposesEitherSideOfAnAtomicMultiWriteCommit(t *testing.T) {
	first := session.MustValue[string]("test.atomic", "first")
	second := session.MustValue[string]("test.atomic", "second")
	storage := sessiontesting.NewGatingStorage(fixedMemoryStorage())
	_, err := storage.Commit(background, []session.Write{session.SetValue(first, "old"), session.SetValue(second, "old")})
	mustNoErr(t, err)
	storage.Arm()
	backed := newSessionOn(storage)
	committing := make(chan error, 1)
	go func() {
		_, err := backed.Mutate(background, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
			return mutator.Commit(ctx, []session.Write{session.SetValue(first, "new"), session.SetValue(second, "new")})
		})
		committing <- err
	}()
	mustNoErr(t, storage.WaitPending(1))
	assertStringValue(t, backed, first, "old")
	assertStringValue(t, backed, second, "old")
	mustNoErr(t, storage.Next(1))
	mustNoErr(t, <-committing)
	assertStringValue(t, backed, first, "new")
	assertStringValue(t, backed, second, "new")
	mustNoErr(t, backed.Close(background))
}

func assertStringValue(t *testing.T, reader session.SessionReader, address session.Value[string], want string) {
	t.Helper()
	stored, err := session.GetValue(background, reader, address)
	mustNoErr(t, err)
	if stored == nil || stored.Value != want {
		t.Fatalf("%s = %+v, want %q", address.Key, stored, want)
	}
}

func startQueuedMutation(backed session.Session) (started chan struct{}, done chan error) {
	started, done = make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := backed.Mutate(background, func(context.Context, session.SessionMutator) (any, error) {
			close(started)
			return nil, nil
		})
		done <- err
	}()
	return started, done
}

func assertNotStarted(t *testing.T, started chan struct{}) {
	t.Helper()
	select {
	case <-started:
		t.Fatal("queued mutation started while the barrier was held")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestStorageBackedSessionHoldsTheExplicitBarrierThroughCommitUntilEnd(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	mutation, err := backed.BeginMutation(background)
	mustNoErr(t, err)
	started, done := startQueuedMutation(backed)
	assertNotStarted(t, started)
	name, err := session.GetValue(background, mutation, session.SessionName)
	mustNoErr(t, err)
	if name != nil {
		t.Fatal("unexpected name")
	}
	result, err := mutation.Commit(background, []session.Write{})
	mustNoErr(t, err)
	if len(result.Seqs) != 0 {
		t.Fatalf("seqs = %v", result.Seqs)
	}
	assertNotStarted(t, started)
	mustNoErr(t, mutation.End(background))
	mustNoErr(t, <-done)
	<-started
	if attempts := storage.GetCommitAttempts(); len(attempts) != 1 || len(attempts[0]) != 0 {
		t.Fatalf("attempts = %v", attempts)
	}
	_, err = mutation.GetEntries(background, nil)
	wantErrContains(t, err, "outside its mutation callback")
	_, err = mutation.Commit(background, nil)
	wantErrContains(t, err, "outside its mutation callback")
	mustNoErr(t, mutation.End(background))
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionAllowsDirectReadsOfACommitBeforeTheScopeEnds(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	mutation, err := backed.BeginMutation(background)
	mustNoErr(t, err)
	name, err := backed.GetName(background)
	mustNoErr(t, err)
	if name != nil {
		t.Fatal("unexpected name")
	}
	_, err = mutation.Commit(background, []session.Write{session.SetValue(session.SessionName, "visible")})
	mustNoErr(t, err)
	started, done := startQueuedMutation(backed)
	assertStringValue(t, backed, session.SessionName, "visible")
	assertNotStarted(t, started)
	mustNoErr(t, mutation.End(background))
	mustNoErr(t, <-done)
	<-started
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionEndsAnExplicitMutationWithoutCommittingAndLetsCloseFinish(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	mutation, err := backed.BeginMutation(background)
	mustNoErr(t, err)
	closed := make(chan error, 1)
	go func() { closed <- backed.Close(background) }()
	select {
	case <-closed:
		t.Fatal("close finished while the explicit mutation was open")
	case <-time.After(20 * time.Millisecond):
	}
	mustNoErr(t, mutation.End(background))
	mustNoErr(t, <-closed)
	if len(storage.GetCommitAttempts()) != 0 {
		t.Fatal("ending without commit committed")
	}
}

func TestStorageBackedSessionExposesExplicitBranchScansThroughSessionAndMutator(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	childID := "00000000-0000-7000-8000-000000000002"
	commitAll(t, backed,
		session.InsertEntry(session.Entry{ID: backedEntryID, Type: session.EntryTypeCustom, CustomType: "root"}),
		session.InsertEntry(session.Entry{ID: childID, ParentID: new(backedEntryID), Type: session.EntryTypeCustom, CustomType: "child"}),
	)
	entries, err := backed.ScanBranch(background, session.StorageBranchScan{Start: childID, Order: session.OrderOldestFirst})
	mustNoErr(t, err)
	if len(entries) != 2 || entries[0].ID != backedEntryID || entries[1].ID != childID {
		t.Fatalf("entries = %+v", entries)
	}
	var captured session.SessionMutator
	_, err = backed.Mutate(background, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		captured = mutator
		scanned, err := mutator.ScanBranch(ctx, session.StorageBranchScan{Start: childID, Limit: new(1)})
		if err != nil || len(scanned) != 1 || scanned[0].ID != childID {
			t.Errorf("mutator scan = %+v, %v", scanned, err)
		}
		return nil, nil
	})
	mustNoErr(t, err)
	_, err = captured.ScanBranch(background, session.StorageBranchScan{Start: childID})
	wantErrContains(t, err, "outside its mutation callback")
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionRejectsPendingAssistantEntriesAtTheWriteBoundary(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	pending := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-sonnet-4-5", Usage: &ai.Usage{}, StopReason: ai.StopReasonPending, Timestamp: now}}
	_, err := backed.Mutate(background, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		return mutator.Commit(ctx, []session.Write{session.InsertEntry(session.Entry{ID: backedEntryID, Type: session.EntryTypeMessage, Message: pending})})
	})
	wantErrContains(t, err, "Cannot persist a pending assistant message")
	if len(storage.GetCommitAttempts()) != 0 {
		t.Fatal("pending assistant reached storage")
	}
	entries, err := backed.GetEntries(background, []string{backedEntryID})
	mustNoErr(t, err)
	if len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionTrustsTypedCustomMessagesWithoutSchemaRegistration(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	message := agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "notice", "content": "maintenance", "display": true, "timestamp": now}}
	entry := session.Entry{ID: backedEntryID, Type: session.EntryTypeMessage, Message: message}
	result := commitAll(t, backed, session.InsertEntry(entry))
	entries, err := backed.GetEntries(background, []string{backedEntryID})
	mustNoErr(t, err)
	entry.Seq, entry.Timestamp = result.FirstSeq, result.Timestamp
	assertJSON(t, entries[backedEntryID], entry)
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionPermitsOneCommitAttemptAndInvalidatesTheMutator(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	var captured session.SessionMutator
	_, err := backed.Mutate(background, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		captured = mutator
		if stored, err := session.GetValue(ctx, mutator, session.SessionName); err != nil || stored != nil {
			t.Errorf("name before = %+v, %v", stored, err)
		}
		if _, err := mutator.Commit(ctx, []session.Write{session.SetValue(session.SessionName, "committed")}); err != nil {
			return nil, err
		}
		_, err := mutator.Commit(ctx, nil)
		wantErrContains(t, err, "commit already attempted")
		return nil, nil
	})
	mustNoErr(t, err)
	if len(storage.GetCommitAttempts()) != 1 {
		t.Fatalf("attempts = %d", len(storage.GetCommitAttempts()))
	}
	assertStringValue(t, backed, session.SessionName, "committed")
	_, err = captured.GetEntries(background, nil)
	wantErrContains(t, err, "outside its mutation callback")
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionConsumesTheCommitGuardWhenTheFirstCommitFails(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(fixedMemoryStorage())
	backed := newSessionOn(storage)
	_, err := backed.Mutate(background, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		_, err := mutator.Commit(ctx, []session.Write{session.InsertEntry(session.Entry{ID: backedEntryID, ParentID: new("missing"), Type: session.EntryTypeCustom, CustomType: "note"})})
		wantErrContains(t, err, "Missing parent entry")
		_, err = mutator.Commit(ctx, nil)
		wantErrContains(t, err, "commit already attempted")
		return nil, nil
	})
	mustNoErr(t, err)
	if len(storage.GetCommitAttempts()) != 1 {
		t.Fatalf("attempts = %d", len(storage.GetCommitAttempts()))
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionMintsDistinctFollowerIDsWithTheLeaderTimestamp(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	leaderTimestamp := int64(0x0123456789ab)
	ids := []string{backed.IdGenerator().Next(&leaderTimestamp), backed.IdGenerator().Next(&leaderTimestamp), backed.IdGenerator().Next(&leaderTimestamp)}
	seen := map[string]bool{}
	for _, id := range ids {
		if uuidTimestamp(t, id) != leaderTimestamp {
			t.Fatalf("id %s timestamp = %x", id, uuidTimestamp(t, id))
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("ids = %v", ids)
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionAcceptsAnInjectedIDGenerator(t *testing.T) {
	next := 0
	generator := session.IdGeneratorFunc(func(timestampMs *int64) string {
		next++
		if timestampMs == nil {
			return "now:" + strconv.Itoa(next)
		}
		return strconv.Itoa(int(*timestampMs)) + ":" + strconv.Itoa(next)
	})
	backed := session.NewStorageBackedSession(backedMetadata, fixedMemoryStorage(), &session.StorageBackedSessionOptions{IdGenerator: generator})
	seven := int64(7)
	if got := backed.IdGenerator().Next(&seven); got != "7:1" {
		t.Fatalf("next(7) = %q", got)
	}
	if got := backed.IdGenerator().Next(nil); got != "now:2" {
		t.Fatalf("next() = %q", got)
	}
	mustNoErr(t, backed.Close(background))
}

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestStorageBackedSessionExposesMetadataAndTheSharedUUIDv7Generator(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	if !reflect.DeepEqual(backed.Metadata(), backedMetadata) {
		t.Fatalf("metadata = %+v", backed.Metadata())
	}
	if id := backed.IdGenerator().Next(nil); !uuidV7Pattern.MatchString(id) {
		t.Fatalf("id = %q", id)
	}
	mustNoErr(t, backed.Close(background))
}

func TestStorageBackedSessionClosesIdempotentlyAndRejectsLaterOperations(t *testing.T) {
	backed := newSessionOn(fixedMemoryStorage())
	var closes sync.WaitGroup
	for range 2 {
		closes.Go(func() {
			if err := backed.Close(background); err != nil {
				t.Error(err)
			}
		})
	}
	closes.Wait()
	_, err := backed.Mutate(background, func(context.Context, session.SessionMutator) (any, error) { return nil, nil })
	wantErrContains(t, err, "Session is closed")
	_, err = backed.GetEntries(background, nil)
	wantErrContains(t, err, "Session is closed")
	_, err = backed.GetValue(background, session.SessionName.StoredAddressBase)
	wantErrContains(t, err, "Session is closed")
	_, err = backed.ScanValues(background, session.SessionName.StoredAddressBase)
	wantErrContains(t, err, "Session is closed")
	_, err = backed.ScanBranch(background, session.StorageBranchScan{Start: backedEntryID})
	wantErrContains(t, err, "Session is closed")
}
