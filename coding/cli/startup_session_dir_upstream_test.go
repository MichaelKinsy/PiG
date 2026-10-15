package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Pi's SessionManager constructor creates a persisted session's directory when createSessionManager opens it at
// startup, before any model or auth check (session-manager.ts:1011-1013 existsSync then mkdirSync; open() at
// :1784-1785 passes the explicit session directory, else the file's parent).
func TestStartupSessionOpenCreatesTheSessionDirectoryLikePi(t *testing.T) {
	t.Run("a new session file's parent", func(t *testing.T) {
		launchCWD := t.TempDir()
		path := filepath.Join(launchCWD, "x", "y", "s.jsonl")
		if _, err := resolveStartupSessionSelection(Args{Session: path}, launchCWD, ""); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
			t.Fatalf("session file parent was not created: %v", err)
		}
	})
	t.Run("the explicit session directory, not the file's parent", func(t *testing.T) {
		launchCWD := t.TempDir()
		path := filepath.Join(launchCWD, "x", "s.jsonl")
		sessionDir := filepath.Join(launchCWD, "sd")
		if _, err := resolveStartupSessionSelection(Args{Session: path}, launchCWD, sessionDir); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(sessionDir); err != nil || !info.IsDir() {
			t.Fatalf("explicit session directory was not created: %v", err)
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("file parent stat = %v, want it left uncreated as Pi does", err)
		}
	})
	t.Run("an existing path is left alone even when it is a file", func(t *testing.T) {
		launchCWD := t.TempDir()
		file := filepath.Join(launchCWD, "f")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveStartupSessionSelection(Args{Session: filepath.Join(file, "s.jsonl")}, launchCWD, ""); err != nil {
			t.Fatalf("Pi's existsSync skips the mkdir and opens a new session there; got %v", err)
		}
	})
	t.Run("an uncreatable directory fails with Node's mkdir error", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a directory the user cannot write")
		}
		launchCWD := t.TempDir()
		locked := filepath.Join(launchCWD, "locked")
		if err := os.Mkdir(locked, 0o555); err != nil {
			t.Fatal(err)
		}
		_, err := resolveStartupSessionSelection(Args{Session: filepath.Join(locked, "a", "s.jsonl")}, launchCWD, "")
		want := "EACCES: permission denied, mkdir '" + filepath.Join(locked, "a") + "'"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	})
}
