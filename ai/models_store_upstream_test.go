package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

func upstreamStoredModel(provider, id string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"id": id, "name": id, "api": "openai-completions", "provider": provider, "baseUrl": "https://example.test/v1", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100})
	return data
}

func TestFileModelsStoreUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/models-store.test.ts:40
	t.Run("persists provider catalogs without replacing unrelated providers", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models-store.json")
		store := NewFileModelsStore(path)
		one := ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "m1")}), CheckedAt: new(100.0)}
		two := ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("two", "m2")}), CheckedAt: new(200.0)}
		if err := store.Write(t.Context(), "one", one); err != nil {
			t.Fatal(err)
		}
		if err := store.Write(t.Context(), "two", two); err != nil {
			t.Fatal(err)
		}
		reloaded := NewFileModelsStore(path)
		for id, want := range map[string]ModelsStoreEntry{"one": one, "two": two} {
			got, err := reloaded.Read(t.Context(), id)
			if err != nil || got == nil {
				t.Fatalf("read %s=%v, %v", id, got, err)
			}
			assertCatalogJSON(t, got, mustStoreJSON(t, want))
		}
		if err := reloaded.Delete(t.Context(), "one"); err != nil {
			t.Fatal(err)
		}
		if got, err := reloaded.Read(t.Context(), "one"); err != nil || got != nil {
			t.Fatalf("deleted read=%v, %v", got, err)
		}
		got, err := reloaded.Read(t.Context(), "two")
		if err != nil {
			t.Fatal(err)
		}
		assertCatalogJSON(t, got, mustStoreJSON(t, two))
	})
	// .upstream/v0.87.1/packages/coding-agent/test/models-store.test.ts:56
	t.Run("preserves the mode of an existing models file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("upstream excludes Windows POSIX mode assertions")
		}
		path := filepath.Join(t.TempDir(), "managed-mode.json")
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o660); err != nil {
			t.Fatal(err)
		}
		if err := NewFileModelsStore(path).Write(t.Context(), "one", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "m1")}), CheckedAt: new(100.0)}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o660 {
			t.Fatalf("mode=%o", info.Mode().Perm())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/models-store.test.ts:130
	t.Run("cancels a catalog write waiting for a held file lock without writing later", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models-store.json")
		initial := map[string]ModelsStoreEntry{"one": {Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "existing")})}}
		if err := os.WriteFile(path, []byte(mustStoreJSON(t, initial)), 0o600); err != nil {
			t.Fatal(err)
		}
		synctest.Test(t, func(t *testing.T) {
			lock, err := pilock.Acquire(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			release := func() {
				if err := lock.Release(); err != nil {
					t.Error(err)
				}
			}
			defer release()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- NewFileModelsStore(path).Write(ctx, "two", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("two", "cancelled")})})
			}()
			synctest.Wait()
			cancel()
			synctest.Wait()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("write error=%v, want cancellation", err)
				}
				release()
			default:
				t.Error("cancelled write still waits for the held lock")
				release()
				<-done
			}
			synctest.Wait()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertCatalogJSON(t, json.RawMessage(data), mustStoreJSON(t, initial))
			fmt.Println("MODELS-STORE:" + string(data))
		})
	})
}

func mustStoreJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
