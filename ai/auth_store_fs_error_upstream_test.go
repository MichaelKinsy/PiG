package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []json.RawMessage{}})
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
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []json.RawMessage{}})
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
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []json.RawMessage{}})
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
		err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []json.RawMessage{}})
		if err == nil || err.Error() != want {
			t.Fatalf("Write error = %v, want Pi's %q", err, want)
		}
	})
}
