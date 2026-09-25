package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/agent/harness/session/testing/conformance"
	"github.com/MichaelKinsy/PiG/ai"
)

const now = 1_700_000_000_000

var background = context.Background()

func fixedNow() int64 { return now }

func TestMemoryStorageConformance(t *testing.T) {
	sessiontesting.RunConformance(t, conformance.CreateStorageConformance(func() (sessiontesting.StorageFixture, error) {
		storage := session.NewMemoryStorage(&session.MemoryStorageOptions{Now: fixedNow})
		return sessiontesting.StorageFixture{Storage: storage, Close: func() error { return storage.Close(background) }}, nil
	}))
}

func memoryRepoAdapter(repo *session.MemorySessionRepo) conformance.RepoAdapter {
	return conformance.RepoAdapter{
		Create: repo.Create,
		Open:   repo.Open,
		List:   repo.List,
		Delete: repo.Delete,
		Fork:   repo.Fork,
	}
}

func TestMemorySessionRepoConformance(t *testing.T) {
	var repo *session.MemorySessionRepo
	factory := func() (conformance.RepoAdapter, error) {
		repo = session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
		return memoryRepoAdapter(repo), nil
	}
	onClose := func() error { return repo.Close(background) }
	sessiontesting.RunConformance(t, conformance.CreateSessionRepoConformance(factory, onClose))
	sessiontesting.RunConformance(t, conformance.CreateSessionRepoStreamingForkConformance(factory, onClose))
}

func TestMemoryStorageIncludesHistoricalTotalsInFirstCommitAfterReopen(t *testing.T) {
	repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
	defer func() { _ = repo.Close(background) }()
	created, err := repo.Create(background, session.SessionCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	usage := ai.Usage{Input: 1, Output: 2, TotalTokens: 3}
	if _, err := session.Mutate(background, created, func(ctx context.Context, mutator session.SessionMutator) (session.CommitResult, error) {
		return mutator.Commit(ctx, []session.Write{
			session.InsertEntry(session.Entry{ID: "history", Type: session.EntryTypeMessage, Message: userText("history", now)}),
			session.InsertUsage(session.UsageRow{ID: "usage", Usage: usage}),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := created.Close(background); err != nil {
		t.Fatal(err)
	}
	reopened, err := repo.Open(background, created.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Mutate(background, reopened, func(ctx context.Context, mutator session.SessionMutator) (session.CommitResult, error) {
		return mutator.Commit(ctx, []session.Write{session.SetValue(session.SessionName, "reopened")})
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, result.Stats, session.SessionStats{MessageCount: 1, Usage: usage})
	stats, err := reopened.GetStats(background)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, result.Stats, stats)
	if err := reopened.Close(background); err != nil {
		t.Fatal(err)
	}
}

func assertJSON(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantJSON, &wantValue); err != nil {
		t.Fatal(err)
	}
	if string(mustJSON(t, gotValue)) != string(mustJSON(t, wantValue)) {
		t.Fatalf("mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
