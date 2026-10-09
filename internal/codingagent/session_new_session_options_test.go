package codingagent

import (
	"path/filepath"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/core/session-manager.ts:1057 newSession(options) validates options.id, uses it as the
// session id, records parentSession in the header, and returns the new session file for a persisted manager.
func TestSessionNewSessionOptions(t *testing.T) {
	t.Run("persisted manager takes the id and parent and returns the file", func(t *testing.T) {
		dir := t.TempDir()
		sess, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("first", "")
		if err != nil {
			t.Fatal(err)
		}
		id := "custom-id.1"
		path, err := sess.NewSession(&NewSessionOptions{ID: &id, ParentSession: "/parent.jsonl"})
		if err != nil {
			t.Fatal(err)
		}
		if sess.ID() != id || sess.GetHeader().ParentSession != "/parent.jsonl" {
			t.Fatalf("id %q parent %q", sess.ID(), sess.GetHeader().ParentSession)
		}
		if path == "" || path != sess.Path() || !strings.HasSuffix(path, "_custom-id.1.jsonl") {
			t.Fatalf("path = %q, want the new session file ending _custom-id.1.jsonl (manager path %q)", path, sess.Path())
		}
	})
	t.Run("in-memory manager returns no file", func(t *testing.T) {
		sess := NewSession("first", t.TempDir())
		path, err := sess.NewSession(nil)
		if err != nil || path != "" {
			t.Fatalf("path = %q, err %v; want empty and nil", path, err)
		}
		if sess.ID() == "first" || sess.ID() == "" {
			t.Fatalf("a nil ID must generate a new id, got %q", sess.ID())
		}
	})
	t.Run("an invalid id is rejected and leaves the session unchanged", func(t *testing.T) {
		dir := t.TempDir()
		sess, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("keep", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"", "-lead", "trail.", "has space", "a/b"} {
			if _, err := sess.NewSession(&NewSessionOptions{ID: &bad}); err == nil || !strings.HasPrefix(err.Error(), "Session id must be non-empty") {
				t.Fatalf("id %q: err = %v, want the upstream assertValidSessionId message", bad, err)
			}
			if sess.ID() != "keep" {
				t.Fatalf("id %q replaced the session: %q", bad, sess.ID())
			}
		}
	})
}
