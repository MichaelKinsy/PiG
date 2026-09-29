package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/session-id-readonly.test.ts:160 — the exact-ID helper must not build full transcript listings either.
func TestFindByIDDoesNotBuildFullListings(t *testing.T) {
	manager := NewSessionManagerWithDir(t.TempDir(), t.TempDir())
	session, err := manager.Create("unrelated-id", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"type": "session", "id": "unrelated-id", "cwd": manager.CWD()})
	if err != nil {
		t.Fatal(err)
	}
	header := string(data) + "\n"
	if err := os.WriteFile(session.Path(), []byte(header+"large transcript contents must not be loaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := listSessionsInDir
	calls := 0
	listSessionsInDir = func(string) ([]SessionInfo, error) { calls++; return nil, errors.New("unexpected full listing") }
	t.Cleanup(func() { listSessionsInDir = previous })
	if got := manager.FindByID("fresh-id"); got != "" {
		t.Fatalf("fresh ID found: %q", got)
	}
	if got := manager.FindByID("unrelated-id"); got != session.Path() {
		t.Fatalf("header ID=%q want=%q", got, session.Path())
	}
	if calls != 0 {
		t.Fatalf("full listing called %d times", calls)
	}
	if got := manager.FindByID(filepath.Base(session.Path())); got != "" {
		t.Fatalf("filename is not an exact ID: %q", got)
	}
}
