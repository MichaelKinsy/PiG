package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestEveryRegistryReadAnswersTheTLSOptInTheSame (D36, owner decision 29): the entry a request is built from comes out of several registry reads (a model the
// models.json entry lists, one it does not list, the catalog layering, the fallback entry of an unknown id, the full list, ProviderInsecure). Each takes its
// answer from providerTLSOptIn, so one combination of a file entry and a registration opts in on all of them or on none.
func TestEveryRegistryReadAnswersTheTLSOptInTheSame(t *testing.T) {
	const fileURL, ownURL = "https://corp.test/v1", "https://ext.test/v1"
	for _, tc := range []struct {
		name         string
		file         string // the entry's insecure member
		registration string // "none", "omitted", "true" or "false"
		endpoint     string // the registration's baseUrl: "" (none), "own" or "file"
		want         bool
	}{
		{"file opt-in, no registration", `,"insecure":true`, "none", "", true},
		{"file opt-in, registration that states nothing", `,"insecure":true`, "omitted", "", true},
		{"file opt-in, silent registration at its own endpoint", `,"insecure":true`, "omitted", "own", false},
		{"file opt-in, silent registration at the file's endpoint", `,"insecure":true`, "omitted", "file", true},
		{"file opt-in, registration that opts in", `,"insecure":true`, "true", "", true},
		{"file without the opt-in, no registration", ``, "none", "", false},
		{"file without the opt-in, silent registration", ``, "omitted", "", false},
		{"file without the opt-in, registration that opts in", ``, "true", "", true},
		{"file false, registration that opts in", `,"insecure":false`, "true", "", true},
		{"file opt-in, registration that turns it off", `,"insecure":true`, "false", "", false},
		{"file without the opt-in, registration that turns it off", ``, "false", "", false},
		{"file opt-in, registration that opts in at its own endpoint", `,"insecure":true`, "true", "own", true},
	} {
		for _, listsModels := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, registration lists models %v", tc.name, listsModels), func(t *testing.T) {
				dir := t.TempDir()
				models := `{"providers":{"corp":{"baseUrl":"` + fileURL + `","api":"openai-completions","apiKey":"file-key","models":[{"id":"listed","name":"Listed","contextWindow":128,"maxTokens":16}]` + tc.file + `}}}`
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
					t.Fatal(err)
				}
				r := NewModelRegistry(dir)
				if tc.registration != "none" {
					raw := `{"apiKey":"registered-key"`
					if listsModels {
						raw += `,"models":[{"id":"registered","name":"Registered"}]`
					}
					if tc.registration != "omitted" {
						raw += `,"insecure":` + tc.registration
					}
					switch tc.endpoint {
					case "own":
						raw += `,"baseUrl":"` + ownURL + `"`
					case "file":
						raw += `,"baseUrl":"` + fileURL + `"`
					}
					var config extension.ProviderConfig
					if err := json.Unmarshal([]byte(raw+`}`), &config); err != nil {
						t.Fatal(err)
					}
					if err := r.RegisterExtensionProvider("corp", config); err != nil {
						t.Fatal(err)
					}
				}
				check := func(read string, got bool) {
					t.Helper()
					if got != tc.want {
						t.Errorf("%s: Insecure = %v, want %v", read, got, tc.want)
					}
				}
				listed, _ := r.Resolve("corp", "listed")
				check("Resolve of a model the file lists", listed.Insecure)
				unlisted, _ := r.Resolve("corp", "unlisted")
				check("Resolve of a model the file does not list", unlisted.Insecure)
				if tc.registration != "none" && listsModels {
					registered, _ := r.Resolve("corp", "registered")
					check("Resolve of a model the registration lists", registered.Insecure)
				}
				check("layerCatalogEntry", r.layerCatalogEntry("corp", "unlisted", ModelEntry{ProviderID: "corp", ModelID: "unlisted"}).Insecure)
				check("ResolveFallbackModel", r.ResolveFallbackModel(&ai.Model{ID: "unlisted", ProviderMeta: ai.ProviderMetadata{ProviderID: "corp"}}).Insecure)
				for _, entry := range r.GetAll() {
					if entry.ProviderID == "corp" {
						check("GetAll "+entry.ModelID, entry.Insecure)
					}
				}
				check("ProviderInsecure", r.ProviderInsecure("corp"))
			})
		}
	}
}
