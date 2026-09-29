package ai

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// Ports packages/coding-agent/test/auth-storage.test.ts: "creates new auth files with owner-only permissions".
// FileAuthStorageBackend.ensureFileExists writes exactly "{}" before the constructor's synchronous reload.
func TestAuthStorageCreatesEmptyFileOnConstruction(t *testing.T) {
	for _, nested := range []string{"", "missing/agent"} {
		t.Run(nested, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), nested, "auth.json")
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if store.Path() != path {
				t.Fatalf("path = %q, want %q", store.Path(), path)
			}
			assertEmptyAuthFile(t, path)
			info, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if nested != "" && runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
				t.Fatalf("parent mode = %o, want 700", info.Mode().Perm())
			}
			if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("constructor left a lock: %v", err)
			}
		})
	}
}

func assertEmptyAuthFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{}" {
		t.Fatalf("auth.json = %q, %v; want exactly {}", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	private, err := ownerfile.OwnerOnly(path, info)
	if err != nil || !private {
		t.Fatalf("auth.json not owner-only: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode = %o, want 600", info.Mode().Perm())
	}
}

// Pi reload reads under the same synchronous lock used for credential writes, without resolving secrets.
func TestAuthStorageConstructorReloadUsesPiLock(t *testing.T) {
	path := seedAuthFile(t, `{"custom":{"type":"api_key","key":"!must-not-run"}}`)
	previous := readAuthFile
	reads := 0
	readAuthFile = func(name string) ([]byte, error) {
		reads++
		info, err := os.Stat(name + ".lock")
		if err != nil || !info.IsDir() {
			t.Fatalf("constructor read without Pi lock: %v", err)
		}
		return previous(name)
	}
	t.Cleanup(func() { readAuthFile = previous })
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("constructor reads = %d, want one locked reload", reads)
	}
	if cred, ok, err := store.GetRaw("custom"); err != nil || !ok || cred.Key != "!must-not-run" {
		t.Fatalf("raw credential = %+v, %v, %v", cred, ok, err)
	}
	if reads != 1 {
		t.Fatalf("unchanged file reread after construction: %d", reads)
	}
}

// Pi ensureFileExists never rewrites existing bytes; reload ignores parse and lock failures.
func TestAuthStorageConstructionPreservesExistingFile(t *testing.T) {
	for _, content := range []string{"", "{invalid-json", "{ }\n", `{"custom":{"type":"api_key","key":"stored"},"oauth":{"type":"oauth","access":"a","refresh":"r","expires":1}}`} {
		t.Run(content, func(t *testing.T) {
			path := seedAuthFile(t, content)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewAuthStorage(path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatalf("existing auth.json changed: %q, %v", data, err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
				t.Fatal("existing auth.json identity, mtime or mode changed")
			}
		})
	}
}

func TestAuthStorageConstructionPreservesHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	lock, err := pilock.AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	})
	reads := countAuthFileReads(t)
	if _, err := NewAuthStorage(path); err != nil {
		t.Fatal(err)
	}
	// Pi ensures the empty file before acquiring the lock and swallows reload's acquisition failure.
	assertEmptyAuthFile(t, path)
	if *reads != 0 {
		t.Fatal("read auth.json while another process held its lock")
	}
	if err := lock.Check(); err != nil {
		t.Fatalf("constructor disturbed existing lock: %v", err)
	}
}

// Both sync and async backend operations ensureFileExists, including reload after external deletion.
func TestAuthStorageRecreatesDeletedFileOnRead(t *testing.T) {
	path := seedAuthFile(t, `{"custom":{"type":"api_key","key":"stored"}}`)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	creds, err := store.Load()
	if err != nil || len(creds) != 0 {
		t.Fatalf("Load after deletion = %v, %v", creds, err)
	}
	assertEmptyAuthFile(t, path)
}
