package ai

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// unmakeableAuthDirectories are the states in which the directory of auth.json cannot be made: a regular file where a parent should be (ENOTDIR), and a parent the process cannot write into (EACCES). A read-only file system (EROFS) is not made here: a test cannot mount one, so its row is untested; PiG's mkdir reports it as nodeerrno.MkdirAll does for the other codes.
func unmakeableAuthDirectories(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"file where the directory should be": filepath.Join(file, "agent", "auth.json")}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if probe, err := os.Create(filepath.Join(locked, "probe")); err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	} else {
		cases["unwritable parent"] = filepath.Join(locked, "agent", "auth.json")
	}
	return cases
}

// FileAuthStorageBackend makes the directory of auth.json inside withLock (auth-storage.ts:68,99), and AuthStorage's constructor ends in reload(), whose catch keeps the last snapshot (:379-392): AuthStorage.create(path) never throws for a directory that cannot be made. read and list of a store with no snapshot swallow the same failure (readLatestData, :418-436), and the first modify or delete rejects with Node's mkdir error. Each row runs Pi's own store on the same path first (probed on the Pi oracle: create, read and list succeed; modify and delete reject with `ENOTDIR: not a directory, mkdir '<dir>'`).
func TestNewAuthStorageOpensWhereTheDirectoryCannotBeMadeAndTheFirstWriteReports(t *testing.T) {
	for name, path := range unmakeableAuthDirectories(t) {
		t.Run(name, func(t *testing.T) {
			want := runPiLockedStore(t, "auth-async", path)
			if want == "ok" {
				t.Fatalf("Pi's store wrote under %s", path)
			}
			store, err := NewAuthStorage(path)
			if err != nil || store == nil {
				t.Fatalf("NewAuthStorage(%s) = %v, %v; Pi's AuthStorage.create does not throw", path, store, err)
			}
			if credential, err := store.Read(context.Background(), "openai"); err != nil || credential != nil {
				t.Fatalf("Read = %v, %v; Pi's read resolves undefined", credential, err)
			}
			if infos, err := store.List(context.Background()); err != nil || len(infos) != 0 {
				t.Fatalf("List = %v, %v; Pi's list resolves []", infos, err)
			}
			_, err = store.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
				return &Credential{Type: CredentialAPIKey, Key: "k"}, nil
			})
			if err == nil || err.Error() != want {
				t.Fatalf("Modify error = %v, want Pi's %q", err, want)
			}
			if err := store.Delete(t.Context(), "openai"); err == nil || err.Error() != want {
				t.Fatalf("Delete error = %v, want Pi's %q", err, want)
			}
		})
	}
}

// The directory and the file a store makes keep the permissions they had when NewAuthStorage made the directory: 0700 for every missing directory of the path, 0600 for auth.json, and the first credential write goes through the atomic writer (D651) with the file's 0600.
func TestFirstCredentialWriteMakesTheDirectoryPrivateAndWritesAtomically(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "missing", "agent")
	path := filepath.Join(dir, "auth.json")
	var written []string
	previous := writeAuthFile
	writeAuthFile = func(name string, data []byte, perm os.FileMode) error {
		written = append(written, name)
		if perm != 0o600 {
			t.Errorf("write of %s with mode %o, want 600", name, perm)
		}
		return previous(name, data, perm)
	}
	t.Cleanup(func() { writeAuthFile = previous })
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "k"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != path {
		t.Fatalf("writes through the atomic writer = %v, want [%s]", written, path)
	}
	for _, check := range []struct {
		path string
		want os.FileMode
	}{{filepath.Join(root, "missing"), 0o700}, {dir, 0o700}, {path, 0o600}} {
		info, err := os.Stat(check.path)
		if err != nil {
			t.Fatal(err)
		}
		// Windows file modes carry no group/other bits (Go reports 0777/0666, as Node's
		// mkdirSync/writeFileSync mode options are no-ops on win32), so POSIX mode bits
		// are asserted only outside Windows.
		if runtime.GOOS != "windows" {
			if got := info.Mode().Perm(); got != check.want {
				t.Errorf("%s mode = %o, want %o", check.path, got, check.want)
			}
		}
	}
}
