package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// These two cases are copied from gate-close-06 TestAuthStorageBasicCasesUpstream and bind Pi auth-storage.test.ts:142-156 without its unrelated deletion/reload prerequisites.
func TestAuthStorageFileCreationUpstream(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "creates new auth files with owner-only permissions"
		if existing {
			name = "preserves the mode of an existing auth file"
		}
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("upstream skips POSIX file modes on win32")
			}
			path := filepath.Join(t.TempDir(), "auth.json")
			want := os.FileMode(0o600)
			if existing {
				if err := os.WriteFile(path, []byte(`{"anthropic":{"type":"api_key","key":"old"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o660); err != nil {
					t.Fatal(err)
				}
				want = 0o660
			}
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				_, err := store.Modify(t.Context(), "anthropic", func(*Credential) (*Credential, error) {
					return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != want {
				t.Fatalf("mode=%o want=%o", info.Mode().Perm(), want)
			}
		})
	}
}

func TestAuthStorageCreatesEmptyObjectBeforeFirstRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyAuthFile(t, path)
	if store.Path() != path {
		t.Fatalf("path=%q want=%q", store.Path(), path)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(path))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("parent=%v, %v; want mode 0700", info, err)
		}
	}
}

func TestAuthStorageOpeningPreservesExistingBytes(t *testing.T) {
	for _, content := range []string{"", "{}", "{invalid-json", "\xef\xbb\xbf{\n  \"openai\": {\"type\":\"api_key\",\"key\":\"stored\"}\n}"} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewAuthStorage(path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatalf("opening rewrote content: %q, %v; want %q", data, err, content)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("opening replaced or touched file: before=%v after=%v err=%v", before, after, err)
			}
		})
	}
}

func TestAuthStorageReadRecreatesDeletedBackingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", Credential{Type: CredentialAPIKey, Key: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	credential, err := store.Read(t.Context(), "openai")
	if err != nil || credential != nil {
		t.Fatalf("read after removal=%v, %v", credential, err)
	}
	assertEmptyAuthFile(t, path)
}

func TestAuthStoragePreCancelledOperationDoesNotCreateFile(t *testing.T) {
	// FileAuthStorageBackend.withLockAsync checks cancellation before ensureParentDir/ensureFileExists (auth-storage.ts:161-163).
	path := filepath.Join(t.TempDir(), "agent", "auth.json")
	store := &AuthStorage{path: path}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := store.Modify(ctx, "openai", func(*Credential) (*Credential, error) {
		t.Fatal("cancelled callback ran")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("modify=%v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled operation created parent: %v", err)
	}
}
