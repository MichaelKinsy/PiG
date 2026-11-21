package ai

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// fileWhereDirectory replaces dir with a regular file.
func fileWhereDirectory(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// linkedReadOnlyStore creates a read-only file named name under a linked parent directory (link -> real) and returns the path through the link.
func linkedParentReadOnlyStore(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	linkDir := filepath.Join(root, "link")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, realDir, linkDir)
	target := filepath.Join(realDir, name)
	if err := os.WriteFile(target, []byte("{}"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })
	return filepath.Join(linkDir, name)
}

// symlinkedReadOnlyStore creates a symlink named name pointing to a read-only target file and returns the symlink path.
func symlinkedReadOnlyStore(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "target-"+name)
	if err := os.WriteFile(target, []byte("{}"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })
	link := filepath.Join(root, name)
	testenv.Symlink(t, target, link)
	return link
}

// danglingStoreInMissingDir creates a symlink named name pointing into a non-existent directory and returns the symlink path.
func danglingStoreInMissingDir(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	link := filepath.Join(root, name)
	testenv.Symlink(t, filepath.Join(root, "missing", name), link)
	return link
}

// FileAuthStorageBackend creates the store's directory with mkdirSync(dir, {recursive: true}) and the store with writeFileSync when existsSync finds neither, reads the store with readFileSync, and rethrows their errors as Node reports them (auth-storage.ts:55-68,181). A directory path that is a file therefore fails at the store's open, and a store that is a directory at its read, which Node reports without a path. PiG's auth store returns the same messages, without its "auth: create" and "auth: read" prefixes. Each row runs Pi's own store on the same state first.
func TestAuthStorageFSErrorsAreNodeErrorsUpstream(t *testing.T) {
	t.Run("directory is a file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "agent")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "auth.json")
		storage, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		fileWhereDirectory(t, dir)
		want := runPiLockedStore(t, "auth-async", path)
		_, err = storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
		})
		if err == nil || err.Error() != want {
			t.Fatalf("Modify error = %v, want Pi's %q", err, want)
		}
	})
	t.Run("read a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		want := runPiLockedStore(t, "auth-async", path)
		storage, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
		})
		if err == nil || err.Error() != want {
			t.Fatalf("Modify error = %v, want Pi's %q", err, want)
		}
	})
	// Pi passes this.authPath unresolved to writeFileSync (auth-storage.ts:77,108,189), so a failed write or create names the given path even when a parent directory or the store itself is a symbolic link.
	for _, tc := range []struct {
		name     string
		setup    func(*testing.T) string
		wantErr  error
		mayWrite bool
	}{
		{"write a read-only store under a linked parent directory", func(t *testing.T) string { return linkedParentReadOnlyStore(t, "auth.json") }, syscall.EACCES, true},
		{"write a read-only symlinked store", func(t *testing.T) string { return symlinkedReadOnlyStore(t, "auth.json") }, syscall.EACCES, true},
		{"create a dangling store symlink whose target directory is missing", func(t *testing.T) string { return danglingStoreInMissingDir(t, "auth.json") }, syscall.ENOENT, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			wantAsync := runPiLockedStore(t, "auth-async", path)
			wantSync := runPiLockedStore(t, "auth-sync", path)
			if tc.mayWrite && (wantAsync == "ok" || wantSync == "ok") {
				t.Skip("this user can write a read-only file")
			}
			storage, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
				return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
			})
			if err == nil || err.Error() != wantAsync {
				t.Fatalf("Modify error = %v, want Pi's %q", err, wantAsync)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Modify error %v does not wrap %v", err, tc.wantErr)
			}
			err = storage.Set("openai", Credential{Type: CredentialAPIKey, Key: "new"})
			if err == nil || err.Error() != wantSync {
				t.Fatalf("Set error = %v, want Pi's %q", err, wantSync)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Set error %v does not wrap %v", err, tc.wantErr)
			}
		})
	}
}

// FileModelsStore goes through the same backend, so its directory, create and read errors are Node's too, without PiG's "models store: ensure dir", "models store: create" and "models store: read" prefixes. A recursive mkdir under a file fails with ENOTDIR for the requested path.
func TestFileModelsStoreFSErrorsAreNodeErrorsUpstream(t *testing.T) {
	t.Run("mkdir under a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "agent")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(file, "models", "models-store.json")
		want := runPiLockedStore(t, "models-write", path)
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}})
		if err == nil || err.Error() != want {
			t.Fatalf("Write error = %v, want Pi's %q", err, want)
		}
	})
	t.Run("directory is a file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "agent")
		if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "models-store.json")
		want := runPiLockedStore(t, "models-write", path)
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}})
		if err == nil || err.Error() != want {
			t.Fatalf("Write error = %v, want Pi's %q", err, want)
		}
	})
	t.Run("read a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models-store.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		want := runPiLockedStore(t, "models-write", path)
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}})
		if err == nil || err.Error() != want {
			t.Fatalf("Write error = %v, want Pi's %q", err, want)
		}
	})
	// The store's writeFileSync fails at its open, which Node reports with the path (EPERM on Windows, EACCES elsewhere).
	t.Run("write a read-only store", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models-store.json")
		if err := os.WriteFile(path, []byte("{}"), 0o400); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		want := runPiLockedStore(t, "models-write", path)
		if want == "ok" {
			t.Skip("this user can write a read-only file")
		}
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}})
		if err == nil || err.Error() != want {
			t.Fatalf("Write error = %v, want Pi's %q", err, want)
		}
	})
	for _, tc := range []struct {
		name     string
		setup    func(*testing.T) string
		wantErr  error
		mayWrite bool
	}{
		{"write a read-only store under a linked parent directory", func(t *testing.T) string { return linkedParentReadOnlyStore(t, "models-store.json") }, syscall.EACCES, true},
		{"write a read-only symlinked store", func(t *testing.T) string { return symlinkedReadOnlyStore(t, "models-store.json") }, syscall.EACCES, true},
		{"create a dangling store symlink whose target directory is missing", func(t *testing.T) string { return danglingStoreInMissingDir(t, "models-store.json") }, syscall.ENOENT, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			want := runPiLockedStore(t, "models-write", path)
			if tc.mayWrite && want == "ok" {
				t.Skip("this user can write a read-only file")
			}
			err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}})
			if err == nil || err.Error() != want {
				t.Fatalf("Write error = %v, want Pi's %q", err, want)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Write error %v does not wrap %v", err, tc.wantErr)
			}
		})
	}
}
