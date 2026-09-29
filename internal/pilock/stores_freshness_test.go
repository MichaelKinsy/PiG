package pilock_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// A fresh process must retain its configured credentials and settings when an earlier PiG left a stale regular sidecar.
func TestStoresReadThroughStaleRegularLocks(t *testing.T) {
	for _, name := range []string{"auth.json", "settings.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			data := `{"theme":"dark","defaultProvider":"configured","defaultModel":"preserved"}`
			if name == "auth.json" {
				data = `{"configured":{"type":"api_key","key":"preserved"}}`
			}
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Minute)
			if err := os.Chtimes(path+".lock", old, old); err != nil {
				t.Fatal(err)
			}
			if name == "auth.json" {
				store, err := ai.NewAuthStorage(path)
				if err != nil {
					t.Fatal(err)
				}
				got, ok, err := store.GetRaw("configured")
				if err != nil || !ok || got.Key != "preserved" {
					t.Fatalf("credentials lost during upgrade: %+v, %v", got, err)
				}
			} else {
				store := codingagent.NewSettingsManagerWithProjectTrust(t.TempDir(), dir, false)
				if got := store.GetTheme(); got != "dark" {
					t.Fatalf("settings lost during upgrade: %q", got)
				}
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != data {
				t.Fatalf("persisted store changed: %s, %v", got, err)
			}
			if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("sidecar remains: %v", err)
			}
		})
	}
}

// Pi's FileAuthStorageBackend and FileSettingsStorage do not run a writer while the lock mtime is fresh, even when it is a regular file (proper-lockfile 4.1.2 lib/lockfile.js:51-85).
func TestStoresWaitForFreshRegularLocks(t *testing.T) {
	for _, name := range []string{"auth.json", "settings.json", "trust.json", "models-store.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var write func(context.Context) error
			switch name {
			case "auth.json":
				store, err := ai.NewAuthStorage(path)
				if err != nil {
					t.Fatal(err)
				}
				write = func(context.Context) error {
					return store.Set("probe", ai.Credential{Type: ai.CredentialAPIKey, Key: "must-not-write"})
				}
			case "settings.json":
				store := codingagent.NewSettingsManagerWithProjectTrust(t.TempDir(), dir, false)
				write = func(context.Context) error { return store.SetTheme("dark") }
			case "trust.json":
				store := codingagent.NewProjectTrustStore(dir)
				cwd := t.TempDir()
				write = func(context.Context) error { return store.Set(cwd, new(true)) }
			case "models-store.json":
				store := ai.NewFileModelsStore(path)
				write = func(ctx context.Context) error { return store.Write(ctx, "probe", ai.ModelsStoreEntry{}) }
			}
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := write(ctx)
			if name == "models-store.json" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("fresh-file async write: %v", err)
				}
			} else if err == nil || time.Since(start) < (pilock.SyncAttempts-1)*pilock.SyncDelay {
				t.Errorf("fresh-file sync write bypassed retry budget: %v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != `{}` {
				t.Errorf("write while contended: %s, %v", data, err)
			}
			if info, err := os.Lstat(path + ".lock"); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
				t.Errorf("fresh sidecar changed: %v, %v", info, err)
			}
		})
	}
}
