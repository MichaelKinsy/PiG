package pilock_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestStoresUpgradeLegacySidecars(t *testing.T) {
	for _, name := range []string{"auth.json", "models-store.json", "settings.json", "trust.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var write func(context.Context) error
			var verify func()
			switch name {
			case "auth.json":
				store, err := ai.NewAuthStorage(path)
				if err != nil {
					t.Fatal(err)
				}
				write = func(context.Context) error {
					return store.Set("upgrade", ai.Credential{Type: ai.CredentialAPIKey, Key: "preserved"})
				}
				verify = func() {
					got, ok, err := store.GetRaw("upgrade")
					if err != nil || !ok || got.Key != "preserved" {
						t.Fatalf("auth: %+v %v", got, err)
					}
				}
			case "models-store.json":
				store := ai.NewFileModelsStore(path)
				write = func(ctx context.Context) error {
					return store.Write(ctx, "upgrade", ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(`{"id":"preserved"}`)})})
				}
				verify = func() {
					got, err := store.Read(t.Context(), "upgrade")
					var model struct {
						ID string `json:"id"`
					}
					if err != nil || got == nil || len(got.Models) != 1 {
						t.Fatalf("models: %+v %v", got, err)
					}
					if model.ID = got.Models[0].ModelID(); model.ID != "preserved" {
						t.Fatalf("model: %+v %v", model, err)
					}
				}
			case "settings.json":
				store := codingagent.NewSettingsManagerWithProjectTrust(t.TempDir(), dir, false)
				theme := "dark"
				write = func(context.Context) error {
					value := theme
					theme = "light"
					return store.SetTheme(value)
				}
				verify = func() {
					fresh := codingagent.NewSettingsManagerWithProjectTrust(t.TempDir(), dir, false)
					if fresh.GetTheme() != "light" {
						t.Fatalf("settings: %q", fresh.GetTheme())
					}
				}
			case "trust.json":
				store := codingagent.NewProjectTrustStore(dir)
				cwd := t.TempDir()
				write = func(context.Context) error { return store.Set(cwd, new(true)) }
				verify = func() {
					got, err := store.Get(cwd)
					if err != nil || got == nil || !*got {
						t.Fatalf("trust: %v %v", got, err)
					}
				}
			}
			// Exact v0.2.0 lock operation on a stale sidecar. Its Unlock closes the handle but intentionally leaves this zero-byte 0600 regular file on disk.
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Minute)
			if err := os.Chtimes(path+".lock", old, old); err != nil {
				t.Fatal(err)
			}
			legacy := flock.New(path + ".lock")
			ok, err := legacy.TryLock()
			if err != nil || !ok {
				t.Fatalf("legacy lock: %v, %v", ok, err)
			}
			t.Cleanup(func() {
				if err := legacy.Unlock(); err != nil {
					t.Error(err)
				}
			})
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			err = write(ctx)
			cancel()
			if name == "models-store.json" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("cancel held catalog write: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "older PiG") {
				t.Fatalf("held legacy writer: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatalf("write while held: %s %v", after, err)
			}
			if err := legacy.Unlock(); err != nil {
				t.Fatal(err)
			}
			if err := write(t.Context()); err != nil {
				t.Fatalf("upgrade: %v", err)
			}
			verify()
			if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("sidecar remains: %v", err)
			}
		})
	}
}
