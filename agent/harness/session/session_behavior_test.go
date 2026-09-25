package session_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func mustNoErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErrContains(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("err = %v, want containing %q", err, text)
	}
}

func commitAll(t *testing.T, target session.Session, writes ...session.Write) session.CommitResult {
	t.Helper()
	result, err := session.Mutate(background, target, func(ctx context.Context, mutator session.SessionMutator) (session.CommitResult, error) {
		return mutator.Commit(ctx, writes)
	})
	mustNoErr(t, err)
	return result
}

func TestMemoryStorageUsesTheInjectedClockOncePerTransaction(t *testing.T) {
	timestamp := int64(now)
	storage := session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 {
		current := timestamp
		timestamp++
		return current
	}})
	first, err := storage.Commit(background, []session.Write{
		session.InsertEntry(session.Entry{ID: "first", Type: session.EntryTypeCustom, CustomType: "note"}),
		session.SetValue(session.SessionName, "first"),
	})
	mustNoErr(t, err)
	second, err := storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "second")})
	mustNoErr(t, err)
	if first.Timestamp != now || second.Timestamp != now+1 {
		t.Fatalf("timestamps = %d, %d", first.Timestamp, second.Timestamp)
	}
	entries, err := storage.GetEntries(background, []string{"first"})
	mustNoErr(t, err)
	if entries["first"].Timestamp != first.Timestamp {
		t.Fatalf("entry timestamp = %d", entries["first"].Timestamp)
	}
	mustNoErr(t, storage.Close(background))
}

func uuidTimestamp(t *testing.T, id string) int64 {
	t.Helper()
	value, err := strconv.ParseInt(strings.ReplaceAll(id, "-", "")[:12], 16, 64)
	mustNoErr(t, err)
	return value
}

func TestMemorySessionRepoUsesItsInjectedClockForGeneratedIdentityAndMetadata(t *testing.T) {
	repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
	created, err := repo.Create(background, session.SessionCreateOptions{})
	mustNoErr(t, err)
	if created.Metadata().CreatedAt != now || uuidTimestamp(t, created.Metadata().ID) != now {
		t.Fatalf("metadata = %+v", created.Metadata())
	}
	mustNoErr(t, created.Close(background))
	mustNoErr(t, repo.Close(background))
}

func TestMemorySessionRepoReturnsAFreshFacadeAfterCloseRetainingOneSession(t *testing.T) {
	repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
	first, err := repo.Create(background, session.SessionCreateOptions{ID: "session"})
	mustNoErr(t, err)
	firstBranch, err := first.CreateBranch(background, "main", nil)
	mustNoErr(t, err)
	mustNoErr(t, first.SetName(background, new("preserved")))

	_, err = repo.Open(background, first.Metadata())
	wantErrContains(t, err, "already open")
	mustNoErr(t, first.Close(background))
	_, err = first.GetName(background)
	wantErrContains(t, err, "Session is closed")
	_, err = first.ScanBranch(background, session.StorageBranchScan{Start: "entry"})
	wantErrContains(t, err, "Session is closed")
	_, err = firstBranch.GetTipID(background)
	wantErrContains(t, err, "Session is closed")

	second, err := repo.Open(background, first.Metadata())
	mustNoErr(t, err)
	if second == first {
		t.Fatal("reopen returned the closed facade")
	}
	name, err := second.GetName(background)
	mustNoErr(t, err)
	if name == nil || *name != "preserved" {
		t.Fatalf("name = %v", name)
	}
	mustNoErr(t, second.Close(background))
	mustNoErr(t, repo.Close(background))
}

func TestMemorySessionRepoWaitsForAnExplicitMutationBeforeClosingItsFacade(t *testing.T) {
	repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
	created, err := repo.Create(background, session.SessionCreateOptions{ID: "session"})
	mustNoErr(t, err)
	mutation, err := created.BeginMutation(background)
	mustNoErr(t, err)
	closed := make(chan error, 1)
	go func() { closed <- created.Close(background) }()
	select {
	case <-closed:
		t.Fatal("facade closed while an explicit mutation was open")
	case <-time.After(20 * time.Millisecond):
	}
	mustNoErr(t, mutation.End(background))
	mustNoErr(t, <-closed)
	reopened, err := repo.Open(background, created.Metadata())
	mustNoErr(t, err)
	mustNoErr(t, reopened.Close(background))
	mustNoErr(t, repo.Close(background))
}

func TestMemorySessionRepoRejectsAnExplicitScopeThatHadNotAcquiredBeforeFacadeClose(t *testing.T) {
	repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
	created, err := repo.Create(background, session.SessionCreateOptions{ID: "session"})
	mustNoErr(t, err)
	first, err := created.BeginMutation(background)
	mustNoErr(t, err)
	second := make(chan error, 1)
	go func() {
		mutation, err := created.BeginMutation(background)
		if mutation != nil {
			_ = mutation.End(background)
		}
		second <- err
	}()
	// Let the second scope queue behind the first before the facade closes.
	time.Sleep(20 * time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- created.Close(background) }()
	time.Sleep(20 * time.Millisecond)
	mustNoErr(t, first.End(background))
	wantErrContains(t, <-second, "Session is closed")
	mustNoErr(t, <-closed)
	reopened, err := repo.Open(background, created.Metadata())
	mustNoErr(t, err)
	mustNoErr(t, reopened.Close(background))
	mustNoErr(t, repo.Close(background))
}

func newBackedSession(idGenerator session.IdGenerator) *session.StorageBackedSession {
	return session.NewStorageBackedSession(
		session.SessionMetadata{ID: "session", CreatedAt: 1, StorageVersion: 1},
		session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 10 }}),
		&session.StorageBackedSessionOptions{IdGenerator: idGenerator},
	)
}

func TestCreateBranchCreatesOnlyTheDataBranchAtAValidatedTarget(t *testing.T) {
	backed := newBackedSession(nil)
	commitAll(t, backed, session.InsertEntry(session.Entry{ID: "target", Type: session.EntryTypeCustom, CustomType: "target"}))
	branch, err := backed.CreateBranch(background, "main", new("target"))
	mustNoErr(t, err)
	tip, err := branch.GetTipID(background)
	mustNoErr(t, err)
	if tip == nil || *tip != "target" {
		t.Fatalf("tip = %v", tip)
	}
	stored, err := session.GetValue(background, backed, session.BranchTip("main"))
	mustNoErr(t, err)
	if stored == nil || stored.Value == nil || *stored.Value != "target" {
		t.Fatalf("stored tip = %+v", stored)
	}
	config, err := session.GetValue(background, backed, session.LaneConfig("main"))
	mustNoErr(t, err)
	state, err := session.GetValue(background, backed, session.LaneStateValue("main"))
	mustNoErr(t, err)
	if config != nil || state != nil {
		t.Fatal("createBranch wrote lane configuration or state")
	}
	mustNoErr(t, backed.Close(background))
}

func TestCreateBranchValidatesNamesAndNonNullTargets(t *testing.T) {
	backed := newBackedSession(nil)
	var invalid *session.SessionInvalidBranchError
	if _, err := backed.CreateBranch(background, "", nil); !errors.As(err, &invalid) {
		t.Fatalf("empty name err = %v", err)
	}
	if _, err := backed.CreateBranch(background, "bad\x00name", nil); !errors.As(err, &invalid) {
		t.Fatalf("NUL name err = %v", err)
	}
	var unknown *session.SessionUnknownTargetError
	if _, err := backed.CreateBranch(background, "main", new("missing")); !errors.As(err, &unknown) {
		t.Fatalf("missing target err = %v", err)
	}
	branch, err := backed.Branch(background, "main")
	mustNoErr(t, err)
	if branch != nil {
		t.Fatal("failed creation left a Branch")
	}
	mustNoErr(t, backed.Close(background))
}

func TestCreateBranchRejectsDuplicatesAtomicallyIncludingConcurrentCreation(t *testing.T) {
	backed := newBackedSession(nil)
	results := make([]error, 2)
	var creators sync.WaitGroup
	for index := range results {
		creators.Go(func() {
			_, results[index] = backed.CreateBranch(background, "main", nil)
		})
	}
	creators.Wait()
	var exists *session.SessionBranchExistsError
	fulfilled := 0
	for _, err := range results {
		switch {
		case err == nil:
			fulfilled++
		case !errors.As(err, &exists):
			t.Fatalf("unexpected err = %v", err)
		}
	}
	if fulfilled != 1 {
		t.Fatalf("fulfilled = %d", fulfilled)
	}
	branch, err := backed.Branch(background, "main")
	mustNoErr(t, err)
	if branch == nil {
		t.Fatal("Branch missing after creation")
	}
	mustNoErr(t, backed.Close(background))
}

func sequentialIDs() session.IdGenerator {
	var mu sync.Mutex
	next := 1
	return session.IdGeneratorFunc(func(*int64) string {
		mu.Lock()
		defer mu.Unlock()
		id := fmt.Sprintf("entry-%d", next)
		next++
		return id
	})
}

func TestBranchIsAbsentUntilExplicitlyCreated(t *testing.T) {
	backed := newBackedSession(sequentialIDs())
	branch, err := backed.Branch(background, "main")
	mustNoErr(t, err)
	if branch != nil {
		t.Fatal("implicit main Branch")
	}
	var surface any = backed
	if _, ok := surface.(interface {
		GetTipID(context.Context) (*string, error)
	}); ok {
		t.Fatal("Session exposes an implicit Branch tip")
	}
	if _, ok := surface.(interface {
		AppendMessage(context.Context, agent.AgentMessage) (string, error)
	}); ok {
		t.Fatal("Session exposes an implicit Branch append")
	}
	created, err := backed.CreateBranch(background, "main", nil)
	mustNoErr(t, err)
	tip, err := created.GetTipID(background)
	mustNoErr(t, err)
	if created.Name() != "main" || tip != nil {
		t.Fatalf("name=%q tip=%v", created.Name(), tip)
	}
	mustNoErr(t, backed.Close(background))
}

func TestBranchDirectlyAppendsImmutableEntriesAndAdvancesOnlyItsOwnTip(t *testing.T) {
	backed := newBackedSession(sequentialIDs())
	main, err := backed.CreateBranch(background, "main", nil)
	mustNoErr(t, err)
	review, err := backed.CreateBranch(background, "review", nil)
	mustNoErr(t, err)
	message := userText("hello", 1)
	messageID, err := main.AppendMessage(background, message)
	mustNoErr(t, err)
	var ok session.JsonValue = map[string]any{"ok": true}
	customID, err := main.AppendCustomEntry(background, "note", &ok)
	mustNoErr(t, err)
	reviewID, err := review.AppendCustomEntry(background, "review", nil)
	mustNoErr(t, err)

	assertTipID(t, main, customID)
	assertTipID(t, review, reviewID)
	entries, err := main.FindEntries(background, &session.BranchScan{Order: session.OrderOldestFirst})
	mustNoErr(t, err)
	if len(entries) != 2 || entries[0].ID != messageID || entries[1].ID != customID {
		t.Fatalf("entries = %+v", entries)
	}
	found, err := main.FindEntry(background, &session.BranchScan{Type: session.EntryTypeMessage})
	mustNoErr(t, err)
	if found == nil || found.ID != messageID || found.Type != session.EntryTypeMessage {
		t.Fatalf("found = %+v", found)
	}
	assertJSON(t, found.Message, message)
	mustNoErr(t, backed.Close(background))
}

func assertTipID(t *testing.T, branch session.Branch, want string) {
	t.Helper()
	tip, err := branch.GetTipID(background)
	mustNoErr(t, err)
	if tip == nil || *tip != want {
		t.Fatalf("tip = %v, want %s", tip, want)
	}
}

func TestBranchSupportsExplicitStartsWithoutMovingTheTip(t *testing.T) {
	backed := newBackedSession(sequentialIDs())
	commitAll(t, backed,
		session.InsertEntry(session.Entry{ID: "root", Type: session.EntryTypeCustom, CustomType: "root"}),
		session.InsertEntry(session.Entry{ID: "left", ParentID: new("root"), Type: session.EntryTypeCustom, CustomType: "left"}),
		session.InsertEntry(session.Entry{ID: "right", ParentID: new("root"), Type: session.EntryTypeCustom, CustomType: "right"}),
		session.SetValue(session.BranchTip("main"), new("left")),
	)
	branch, err := backed.Branch(background, "main")
	mustNoErr(t, err)
	entries, err := branch.FindEntries(background, &session.BranchScan{Start: new("right"), Order: session.OrderOldestFirst})
	mustNoErr(t, err)
	if len(entries) != 2 || entries[0].ID != "root" || entries[1].ID != "right" {
		t.Fatalf("entries = %+v", entries)
	}
	assertTipID(t, branch, "left")
	mustNoErr(t, backed.Close(background))
}

func TestBranchRejectsPendingAssistantMessagesBeforeCommitting(t *testing.T) {
	backed := newBackedSession(sequentialIDs())
	branch, err := backed.CreateBranch(background, "main", nil)
	mustNoErr(t, err)
	pending := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: "test", Provider: "test", ModelID: "test", Usage: &ai.Usage{}, StopReason: ai.StopReasonPending, Timestamp: 1}}
	var pendingErr *session.SessionPendingAssistantMessageError
	if _, err := branch.AppendMessage(background, pending); !errors.As(err, &pendingErr) {
		t.Fatalf("err = %v", err)
	}
	tip, err := branch.GetTipID(background)
	mustNoErr(t, err)
	if tip != nil {
		t.Fatalf("tip moved to %v", *tip)
	}
	mustNoErr(t, backed.Close(background))
}
