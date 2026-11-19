package codingagent

import "testing"

// TestRadiusCompositionTakesTheTLSOptInFromTheFileEntry (D36, owner decision 29): the entries a Radius provider composes from its models.json entry carry the
// answer of providerTLSOptIn like every other registry read. A Radius provider has no extension registration (radiusProviderLocked is nil for a provider an
// extension registered), so the answer is the file entry's own setting.
func TestRadiusCompositionTakesTheTLSOptInFromTheFileEntry(t *testing.T) {
	for _, tc := range []struct {
		name, insecure string
		want           bool
	}{
		{"opted in", `,"insecure":true`, true},
		{"not stated", ``, false},
		{"stated false", `,"insecure":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := `{"providers":{"radius":{"oauth":"radius","baseUrl":"https://local.invalid","api":"openai-completions","models":[{"id":"extra","name":"Extra","contextWindow":128,"maxTokens":16}]` + tc.insecure + `}}}`
			registry, _, _ := radiusTestRegistry(t, models, nil)
			entry, found := registry.Resolve("radius", "extra")
			if !found {
				t.Fatal("radius/extra is not resolved")
			}
			if entry.Insecure != tc.want {
				t.Fatalf("composed entry Insecure = %v, want %v", entry.Insecure, tc.want)
			}
		})
	}
}
