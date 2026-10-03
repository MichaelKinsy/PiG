package experimental

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Session ID rule and the catalog layout are the contract of session-catalog.ts:19-24,44-60: a directory named by the ID that holds meta.json.
func TestSessionIDs(t *testing.T) {
	valid := []string{"a", "A", "0", "demo-1", "a.b_c-d", "a" + strings.Repeat("b", 127)}
	invalid := []string{"", ".", "..", ".hidden", "-x", "_x", "a/b", "../x", "a b", "a\x00", "é", "a" + strings.Repeat("b", 128)}
	for _, id := range valid {
		if !IsSessionID(id) {
			t.Errorf("IsSessionID(%q) = false; want true", id)
		}
	}
	for _, id := range invalid {
		if IsSessionID(id) {
			t.Errorf("IsSessionID(%q) = true; want false", id)
		}
	}
}

func TestCreateSessionWritesTheCatalogLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	created, err := CreateSession(root, CreateSessionOptions{ID: new("demo-1"), Cwd: "/work/<a&b>"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "demo-1" || created.Cwd != "/work/<a&b>" || created.Path != filepath.Join(root, "demo-1") || created.CreatedAt <= 0 {
		t.Fatalf("created = %+v", created)
	}
	raw, err := os.ReadFile(filepath.Join(created.Path, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	// JSON.stringify({createdAt, cwd}, null, "\t") + "\n"; the HTML characters stay unescaped.
	want := "{\n\t\"createdAt\": " + strconv.FormatInt(int64(created.CreatedAt), 10) + ",\n\t\"cwd\": \"/work/<a&b>\"\n}\n"
	if string(raw) != want {
		t.Fatalf("meta.json = %q; want %q", raw, want)
	}
	if got := SessionStoragePath(created); got != filepath.Join(root, "demo-1", "session.sqlite") {
		t.Fatalf("storage path = %q", got)
	}
	if _, err := os.Stat(SessionStoragePath(created)); !os.IsNotExist(err) {
		t.Fatalf("storage exists before a worker opened it: %v", err)
	}
	read := ReadSession(root, "demo-1")
	if read == nil || *read != created {
		t.Fatalf("ReadSession = %+v; want %+v", read, created)
	}
}

func TestCreateSessionGeneratesAUniqueValidID(t *testing.T) {
	root := t.TempDir()
	first, err := CreateSession(root, CreateSessionOptions{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateSession(root, CreateSessionOptions{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if !IsSessionID(first.ID) || !IsSessionID(second.ID) || first.ID == second.ID {
		t.Fatalf("generated IDs %q, %q", first.ID, second.ID)
	}
}

// remote-runtime.test.ts:792 expects the catalog to reject a duplicate Session ID with this message.
func TestCreateSessionRejectsDuplicateAndInvalidIDs(t *testing.T) {
	root := t.TempDir()
	if _, err := CreateSession(root, CreateSessionOptions{ID: new("demo-1"), Cwd: "/w"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSession(root, CreateSessionOptions{ID: new("demo-1"), Cwd: "/other"}); err == nil || err.Error() != "Session demo-1 already exists" {
		t.Fatalf("duplicate error = %v", err)
	}
	if kept := ReadSession(root, "demo-1"); kept == nil || kept.Cwd != "/w" {
		t.Fatalf("duplicate create changed the Session: %+v", kept)
	}
	for _, id := range []string{"", "../escape", ".hidden"} {
		if _, err := CreateSession(root, CreateSessionOptions{ID: &id, Cwd: "/w"}); err == nil || err.Error() != "Invalid session ID: "+id {
			t.Errorf("CreateSession(%q) error = %v", id, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(root)); len(entries) != 1 {
		t.Fatalf("an invalid ID created entries outside the directory: %v", entries)
	}
}

func TestListSessionsSkipsEntriesWithoutValidMetadata(t *testing.T) {
	root := t.TempDir()
	good, err := CreateSession(root, CreateSessionOptions{ID: new("good"), Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	for name, meta := range map[string]string{
		"null-cwd":       `{"createdAt":1,"cwd":null}`,
		"null-created":   `{"createdAt":null,"cwd":"/w"}`,
		"string-created": `{"createdAt":"1","cwd":"/w"}`,
		"number-cwd":     `{"createdAt":1,"cwd":2}`,
		"missing-cwd":    `{"createdAt":1}`,
		"array":          `[1,"/w"]`,
		"null":           `null`,
		"malformed":      `{"createdAt":`,
	} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "no-meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "meta.json"), []byte(`{"createdAt":1,"cwd":"/w"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0] != good {
		t.Fatalf("sessions = %+v; want only %+v", sessions, good)
	}
}

func TestReadSessionAcceptsAnyFiniteCreatedAt(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "s", "meta.json"), []byte(`{"createdAt":-1.5e3,"cwd":"","extra":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	read := ReadSession(root, "s")
	if read == nil || read.CreatedAt != -1500 || read.Cwd != "" {
		t.Fatalf("ReadSession = %+v", read)
	}
}

func TestListSessionsOfAnAbsentDirectoryIsEmpty(t *testing.T) {
	sessions, err := ListSessions(filepath.Join(t.TempDir(), "missing"))
	if err != nil || sessions == nil || len(sessions) != 0 {
		t.Fatalf("ListSessions = %v, %v", sessions, err)
	}
	if ReadSession(filepath.Join(t.TempDir(), "missing"), "x") != nil {
		t.Fatal("ReadSession found a Session in an absent directory")
	}
}

func TestListSessionsPropagatesOtherDirectoryErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListSessions(file); err == nil {
		t.Fatal("ListSessions of a file succeeded")
	}
}

func TestDeleteSessionRemovesTheDirectoryAndTolleratesAnAbsentOne(t *testing.T) {
	root := t.TempDir()
	created, err := CreateSession(root, CreateSessionOptions{ID: new("gone"), Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SessionStoragePath(created), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(created); err != nil {
		t.Fatal(err)
	}
	if ReadSession(root, "gone") != nil {
		t.Fatal("deleted Session is still readable")
	}
	if err := DeleteSession(created); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}
