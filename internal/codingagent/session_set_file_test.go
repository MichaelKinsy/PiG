package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// session-manager.ts:1025-1054 setSessionFile: a file with entries loads as flushed, a missing path starts a new session at that exact path, an empty file gets a header, and a non-empty non-session file is rejected without changing the manager.
func TestSetSessionFileSwitchesThePersistedManager(t *testing.T) {
	dir := t.TempDir()
	manager := func() *Session {
		session, err := NewSessionManagerWithDir(dir, dir).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		return session
	}

	t.Run("a file with entries is loaded as flushed", func(t *testing.T) {
		path := fileOperationWrite(t, dir, "loaded.jsonl", fileOperationHeader(dir, "loaded")+fileOperationUser+"\n")
		s := manager()
		if err := s.SetSessionFile(path); err != nil {
			t.Fatal(err)
		}
		if s.ID() != "loaded" || s.Path() != path || s.GetEntryCount() != 1 {
			t.Fatalf("id=%q path=%q entries=%d, want the loaded session", s.ID(), s.Path(), s.GetEntryCount())
		}
		upstreamSessionAssistant(t, s, "answer")
		if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session", "user", "assistant"}) {
			t.Fatalf("roles = %v, want the answer appended to the loaded file", got)
		}
	})

	t.Run("a missing path keeps the explicit path and writes on the first message", func(t *testing.T) {
		path := filepath.Join(dir, "explicit.jsonl")
		s := manager()
		if err := s.SetSessionFile(path); err != nil {
			t.Fatal(err)
		}
		if s.Path() != path || s.GetEntryCount() != 0 {
			t.Fatalf("path=%q entries=%d", s.Path(), s.GetEntryCount())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the file exists before the first message: %v", err)
		}
		upstreamSessionUser(t, s, "hello")
		if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session", "user"}) {
			t.Fatalf("roles = %v", got)
		}
	})

	t.Run("an empty file is initialised with a header", func(t *testing.T) {
		path := fileOperationWrite(t, dir, "empty.jsonl", "")
		s := manager()
		if err := s.SetSessionFile(path); err != nil {
			t.Fatal(err)
		}
		if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session"}) {
			t.Fatalf("roles = %v, want only the header", got)
		}
	})

	t.Run("a file that is not a session is rejected after it became the session file", func(t *testing.T) {
		// Pi 1.0.4 assigns sessionFile before it reads the file (session-manager.ts:1030), so the rejected path stays the session file: a probe of SessionManager.create + setSessionFile shows getSessionFile() === the rejected path, the entries unchanged, and the next append failing (EEXIST from openSync "wx") or, once flushed, appending to the rejected file.
		path := fileOperationWrite(t, dir, "invalid.jsonl", "not a session\n")
		s := manager()
		before := s.ID()
		err := s.SetSessionFile(path)
		if err == nil || !strings.Contains(err.Error(), "Session file is not a valid pi session: "+path) {
			t.Fatalf("err = %v", err)
		}
		if s.ID() != before || s.Path() != path {
			t.Fatalf("id=%q path=%q, want the old session at the rejected path", s.ID(), s.Path())
		}
		m := mkUserMsg("hello")
		if _, err := s.AppendMessage(m); err == nil {
			t.Fatal("the first message flushed over the rejected file")
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "not a session\n" {
			t.Fatalf("rejected file = %q, %v", got, err)
		}
	})

	t.Run("a flushed manager appends to the rejected file", func(t *testing.T) {
		path := fileOperationWrite(t, dir, "invalid-flushed.jsonl", "nope\n")
		s := manager()
		upstreamSessionUser(t, s, "x")
		if err := s.SetSessionFile(path); err == nil {
			t.Fatal("want an error")
		}
		upstreamSessionUser(t, s, "y")
		got, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(got), "nope\n{\"type\":\"message\"") {
			t.Fatalf("rejected file = %q, %v; want the message appended", got, err)
		}
	})

	t.Run("the manager keeps its cwd and session directory", func(t *testing.T) {
		// Pi 1.0.4 _loadEntries (session-manager.ts:1085-1101) replaces the entries and session id but not this.cwd; a probe shows getCwd() is the manager's cwd while getHeader().cwd is the file's.
		other := t.TempDir()
		path := fileOperationWrite(t, dir, "elsewhere.jsonl", fileOperationHeader(other, "elsewhere")+fileOperationUser+"\n")
		s := manager()
		sessionDir := s.GetSessionDir()
		if err := s.SetSessionFile(path); err != nil {
			t.Fatal(err)
		}
		if s.CWD() != dir || s.GetHeader().CWD != other || s.GetSessionDir() != sessionDir {
			t.Fatalf("cwd=%q header cwd=%q sessionDir=%q, want cwd %q, header cwd %q, sessionDir %q", s.CWD(), s.GetHeader().CWD, s.GetSessionDir(), dir, other, sessionDir)
		}
	})
}

// A manager built with persist=false loads the entries but never writes (session-manager.ts:1172 _persist returns without persist).
func TestSetSessionFileOnAnInMemoryManagerDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	path := fileOperationWrite(t, dir, "loaded.jsonl", fileOperationHeader(dir, "loaded")+fileOperationUser+"\n")
	s := NewSession("memory", dir)
	if err := s.SetSessionFile(path); err != nil {
		t.Fatal(err)
	}
	if s.IsPersisted() || s.GetEntryCount() != 1 {
		t.Fatalf("persisted=%v entries=%d, want a loaded in-memory manager", s.IsPersisted(), s.GetEntryCount())
	}
	upstreamSessionAssistant(t, s, "answer")
	if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session", "user"}) {
		t.Fatalf("roles = %v, an in-memory manager must not append to the file", got)
	}
}

// A manager that does not persist never writes the file it switches to: no header for an empty file and no migration rewrite (session-manager.ts:1124-1125 _rewriteFile returns without persist; a Pi 1.0.4 probe of SessionManager.inMemory + setSessionFile leaves both files byte-identical).
func TestSetSessionFileOnAnInMemoryManagerWritesNothing(t *testing.T) {
	dir := t.TempDir()
	empty := fileOperationWrite(t, dir, "empty.jsonl", "")
	s := NewSession("memory", dir)
	if err := s.SetSessionFile(empty); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(empty); err != nil || len(got) != 0 {
		t.Fatalf("empty file = %q, %v; want it untouched", got, err)
	}
	old := fileOperationWrite(t, dir, "old.jsonl", fileOperationLegacyHeader+`{"type":"message","timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}`+"\n")
	before, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	s = NewSession("memory", dir)
	if err := s.SetSessionFile(old); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(old); err != nil || string(after) != string(before) {
		t.Fatalf("old file rewritten:\n%s", after)
	}
	if s.GetEntryCount() != 1 || s.IsPersisted() {
		t.Fatalf("entries=%d persisted=%v", s.GetEntryCount(), s.IsPersisted())
	}
}
