package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi's loadEntriesFromFile and _setSessionFile test existsSync, which is false for any stat failure
// (session-manager.ts:629, :1031). A session path under a regular file is therefore a new session, not an ENOTDIR error.
func TestSessionPathUnderAFileIsANewSessionLikePi(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(file, "s.jsonl")

	entries, err := LoadEntriesFromFile(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("LoadEntriesFromFile = %v, %v; want no entries and no error", entries, err)
	}
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatalf("Open = %v; want a new session at the explicit path", err)
	}
	if session.Path() != path || len(session.GetEntries()) != 0 {
		t.Fatalf("session path=%q entries=%d; want %q and none", session.Path(), len(session.GetEntries()), path)
	}
}

// existsSync is true for a file that exists but cannot be read, so opening it fails as Pi's openSync does.
func TestUnreadableSessionFileStillFails(t *testing.T) {
	if os.Geteuid() == 0 || os.PathSeparator == '\\' {
		t.Skip("needs a file the user cannot read")
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEntriesFromFile(path); err == nil {
		t.Fatal("LoadEntriesFromFile of an unreadable file succeeded; Pi's openSync throws EACCES")
	}
}

// SessionManager.open creates the manager's session directory before it loads anything, so a switch to a path under a
// missing directory creates it, and an uncreatable one fails with Node's mkdir error (session-manager.ts:1011-1013).
func TestOpenCreatesTheSessionDirectoryLikePi(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	path := filepath.Join(dir, "s.jsonl")
	if _, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("session directory was not created: %v", err)
	}
	if os.Geteuid() == 0 || os.PathSeparator == '\\' {
		return
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(locked, "x")
	_, err := NewSessionManagerWithDir(t.TempDir(), missing).Open(filepath.Join(missing, "s.jsonl"))
	if want := "EACCES: permission denied, mkdir '" + missing + "'"; err == nil || err.Error() != want {
		t.Fatalf("Open err = %v, want %q", err, want)
	}
}

// existsSync is false for any stat failure, so a session directory under a regular file still reaches mkdirSync, which
// fails with Node's ENOTDIR text (session-manager.ts:1011-1013).
func TestOpenUnderAFileFailsWithNodesMkdirError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(file, "x", "y")
	_, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(filepath.Join(dir, "s.jsonl"))
	if want := "ENOTDIR: not a directory, mkdir '" + dir + "'"; err == nil || err.Error() != want {
		t.Fatalf("Open err = %v, want %q", err, want)
	}
}
