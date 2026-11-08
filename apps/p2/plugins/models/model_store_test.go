package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestFileModelsStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "models-store.json")
	store := NewFileModelsStore(path)
	defer store.Close()
	if fingerprint, err := store.Fingerprint(); err != nil || fingerprint != "missing" {
		t.Fatalf("fingerprint = %q, %v", fingerprint, err)
	}
	if entry, err := store.Read(ctx, "p"); entry != nil || err != nil {
		t.Fatalf("read of an empty store = %+v, %v", entry, err)
	}
	checked := 5.0
	entry := ai.ModelsStoreEntry{ETag: "e", CheckedAt: &checked, Models: []ai.AnyModel{&ai.Model{ID: "m", DisplayName: "M", ProviderMeta: ai.ProviderMetadata{ProviderID: "p", API: "openai-completions", BaseURL: "https://x"}}}}
	if err := store.Write(ctx, "p", entry); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(ctx, "p")
	if err != nil || got == nil || got.ETag != "e" || len(got.Models) != 1 || got.Models[0].ModelID() != "m" {
		t.Fatalf("read = %+v, %v", got, err)
	}
	// A second store sees what the first wrote, and a delete.
	other := NewFileModelsStore(path)
	defer other.Close()
	if got, _ := other.Read(ctx, "p"); got == nil {
		t.Fatal("not shared")
	}
	if err := other.Delete(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Read(ctx, "p"); got != nil || err != nil {
		t.Fatalf("after delete = %+v, %v", got, err)
	}
	// Invalid content is refused.
	for _, content := range []string{`{`, `[]`, `{"p":1}`, `{"p":{"models":[{"id":"a"}]}}`, `{"p":{}}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewFileModelsStore(path).Read(ctx, "p"); err == nil || err.Error() != "Invalid cached model catalogs" {
			t.Errorf("%s: %v", content, err)
		}
	}
}
