package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi disables catalog network refresh for credential printing, but reads its file store.
func TestCredentialPrintUsesSavedCatalog(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	const provider, key = ai.RadiusProviderID, "fixture-value"
	setup := func(t *testing.T) (string, func(...string)) {
		t.Helper()
		dir := t.TempDir()
		writeAuthJSON(t, dir, map[string]ai.Credential{provider: {Type: ai.CredentialAPIKey, Key: key}})
		return filepath.Join(dir, "models-store.json"), func(args ...string) {
			t.Helper()
			code, stdout, stderr := runAuthForTest(t, dir, append([]string{"print-api-key"}, args...)...)
			matches := stdout == key+"\n"
			if code != 0 || stderr != "" || !matches {
				t.Fatalf("print-api-key exit = %d, stderr = %q, stdout matches = %t", code, stderr, matches)
			}
		}
	}

	t.Run("initializes a missing store", func(t *testing.T) {
		path, printKey := setup(t)
		printKey("--provider", provider)
		if got, err := os.ReadFile(path); err != nil || string(got) != "{}" {
			t.Fatalf("models-store.json = %q, %v; want an empty catalog", got, err)
		}
	})

	t.Run("resolves a saved-only model without rewriting the store", func(t *testing.T) {
		path, printKey := setup(t)
		model := &ai.Model{
			ID: "fixture-catalog-only", DisplayName: "Catalog only", Input: []string{"text"},
			ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: ai.APIPiMessages, BaseURL: "https://example.invalid"},
			Capabilities: ai.ModelCapabilities{ContextWindow: 64000, MaxOutputTokens: 8000},
		}
		store := ai.NewFileModelsStore(path)
		if err := store.Write(t.Context(), provider, ai.ModelsStoreEntry{Models: []ai.AnyModel{model}, CheckedAt: new(float64(1))}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// With no --provider, there is no provider/model fallback if the catalog misses.
		printKey("--model", model.ID)
		if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
			t.Fatalf("credential print changed or removed the saved catalog (read error: %v)", err)
		}
	})
}
