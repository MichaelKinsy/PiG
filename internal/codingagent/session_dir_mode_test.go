// Windows has no POSIX permission bits: os.MkdirAll ignores the mode there and FileMode.Perm reports 0777.

//go:build !windows

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// pi-mono #10770: the sessions directory holds every conversation, tool output and secret a model saw, yet Pi creates it with the umask default (0755),
// so other users of the machine can list and read it.
// pig divergence (D132): every directory PiG creates for sessions is 0700.
func TestSessionDirectoriesAreCreatedOwnerOnly(t *testing.T) {
	assertOwnerOnly := func(t *testing.T, root string) {
		t.Helper()
		var seen int
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			seen++
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if mode := info.Mode().Perm(); mode != 0o700 {
				t.Errorf("%s mode = %o, want 700", path, mode)
			}
			return nil
		})
		if err != nil || seen < 3 {
			t.Fatalf("walk %s: %v (%d directories)", root, err, seen)
		}
	}
	fresh := func(t *testing.T) (root string, manager *SessionManager) {
		t.Helper()
		root = filepath.Join(t.TempDir(), "agent")
		return root, NewSessionManagerWithDir(t.TempDir(), GetDefaultSessionDirPath(t.TempDir(), root))
	}
	t.Run("Create", func(t *testing.T) {
		root, manager := fresh(t)
		if _, err := manager.Create("", ""); err != nil {
			t.Fatal(err)
		}
		assertOwnerOnly(t, root)
	})
	t.Run("Clone", func(t *testing.T) {
		source, err := tempSessionMgr(t).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		id := upstreamSessionUser(t, source, "hello")
		root, manager := fresh(t)
		if _, err := manager.Clone(source, id); err != nil {
			t.Fatal(err)
		}
		assertOwnerOnly(t, root)
	})
	t.Run("ForkFromFile", func(t *testing.T) {
		source := tempSessionMgr(t)
		session, err := source.Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		upstreamSessionAssistant(t, session, "hello")
		root, manager := fresh(t)
		if _, err := manager.ForkFromFile(session.Path()); err != nil {
			t.Fatal(err)
		}
		assertOwnerOnly(t, root)
	})
}
