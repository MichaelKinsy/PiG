package codingagent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Pi's selector deletes whichever listed session the user confirms (session-selector.ts:842-866); it has no directory check. PiG bounds deletion to the directory the All scope lists, so every listed session stays deletable, including another project's.
func TestSessionManagerDeletesEveryListedSession(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Keep deletion local; do not invoke an installed desktop trash service.
	for _, custom := range []bool{false, true} {
		name := "default root"
		if custom {
			name = "custom session dir"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(ENV_AGENT_DIR, t.TempDir())
			cwdA, cwdB := t.TempDir(), t.TempDir()
			dirA, dirB := defaultSessionDir(cwdA), defaultSessionDir(cwdB)
			sm := NewSessionManager(cwdA)
			if custom {
				dirA = t.TempDir()
				dirB = dirA
				sm = NewSessionManagerWithDir(cwdA, dirA)
			}
			own := listingPersistedSession(t, dirA, cwdA, "own project")
			other := listingPersistedSession(t, dirB, cwdB, "other project")
			listed, err := sm.ListAllSessions()
			if err != nil {
				t.Fatal(err)
			}
			paths := make([]string, 0, len(listed))
			for _, info := range listed {
				paths = append(paths, info.Path)
			}
			if !slices.Contains(paths, own) || !slices.Contains(paths, other) {
				t.Fatalf("All scope listed %q, want %q and %q", paths, own, other)
			}
			for _, path := range paths {
				if err := sm.DeleteSession(path); err != nil {
					t.Errorf("delete listed %s: %v", path, err)
				}
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("listed session %s remains after deletion: %v", path, err)
				}
			}
		})
	}
}

// The bound compares whole path elements: a sibling that merely shares the root's name prefix, or a path that climbs out of the root, stays outside it. Windows compares case-insensitively.
func TestSessionManagerDeleteBoundRespectsPathBoundaries(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	agentDir := t.TempDir()
	t.Setenv(ENV_AGENT_DIR, agentDir)
	base, cwd := t.TempDir(), t.TempDir()
	custom := filepath.Join(base, "custom")
	write := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name string
		sm   *SessionManager
		path string
	}{
		{"sibling of the sessions root", NewSessionManager(cwd), write(filepath.Join(agentDir, "sessions-other", "x.jsonl"))},
		{"escape from the sessions root", NewSessionManager(cwd), filepath.Join(agentDir, "sessions", "--x--", "..", "..", filepath.Base(write(filepath.Join(agentDir, "escape.jsonl"))))},
		{"sibling of a custom dir", NewSessionManagerWithDir(cwd, custom), write(filepath.Join(base, "custom-other", "y.jsonl"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if result := tc.sm.deleteListedSession(tc.path); result.ok {
				t.Fatalf("deleted %s outside the listed root", tc.path)
			}
			if _, err := os.Stat(tc.path); err != nil {
				t.Fatalf("refused path was changed: %v", err)
			}
		})
	}
	if runtime.GOOS == "windows" {
		t.Run("case-insensitive root on Windows", func(t *testing.T) {
			path := listingPersistedSession(t, defaultSessionDir(cwd), cwd, "case")
			upper := strings.ToUpper(path)
			if result := NewSessionManager(cwd).deleteListedSession(upper); !result.ok {
				t.Fatalf("delete %s: %+v", upper, result)
			}
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("session remains: %v", err)
			}
		})
	}
}
