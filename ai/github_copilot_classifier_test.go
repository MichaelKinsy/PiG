package ai

import (
	"encoding/json"
	"testing"
)

func TestGitHubCopilotClassifierFiltering(t *testing.T) {
	t.Run("parseGitHubCopilotModelCatalog records non-disabled decision models", func(t *testing.T) {
		body := []byte(`{
			"data": [
				{"id": "gpt-5", "model_picker_enabled": true, "capabilities": {"type": "chat"}, "policy": {"state": "enabled"}},
				{"id": "gpt-6-luna-decisions", "model_picker_enabled": false, "capabilities": {"type": "decisions"}, "policy": {"state": "enabled"}},
				{"id": "microsoft-decision-1", "model_picker_enabled": false, "capabilities": {"type": "decisions"}, "policy": {"state": "disabled"}}
			]
		}`)
		catalog, err := parseGitHubCopilotModelCatalog(body, false)
		if err != nil {
			t.Fatalf("parseGitHubCopilotModelCatalog: %v", err)
		}
		if len(catalog.AvailableModelIDs) != 1 || catalog.AvailableModelIDs[0] != "gpt-5" {
			t.Fatalf("AvailableModelIDs = %v, want [gpt-5]", catalog.AvailableModelIDs)
		}
		if len(catalog.ClassifierModelIDs) != 1 || catalog.ClassifierModelIDs[0] != "gpt-6-luna-decisions" {
			t.Fatalf("ClassifierModelIDs = %v, want [gpt-6-luna-decisions]", catalog.ClassifierModelIDs)
		}
	})

	t.Run("filterGitHubCopilotAllModels filters chat and classifier models independently", func(t *testing.T) {
		models := []AnyModel{
			&Model{ID: "gpt-5"},
			&Model{ID: "gpt-4o"},
			&ClassifierModel{ID: "gpt-6-luna-decisions", API: ClassifierAPIOpenAIDecisions},
			&ClassifierModel{ID: "microsoft-decision-1", API: ClassifierAPITypesafeSystemOne},
		}
		cred := &Credential{
			Type:                        CredentialOAuth,
			AvailableModelIDs:           json.RawMessage(`["gpt-5"]`),
			AvailableClassifierModelIDs: json.RawMessage(`["gpt-6-luna-decisions"]`),
		}
		filtered := filterGitHubCopilotAllModels(models, cred)
		if len(filtered) != 2 || filtered[0].ModelID() != "gpt-5" || filtered[1].ModelID() != "gpt-6-luna-decisions" {
			t.Fatalf("filtered = %v", filtered)
		}

		// Legacy credential without availableClassifierModelIds keeps all classifiers.
		legacyCred := &Credential{
			Type:              CredentialOAuth,
			AvailableModelIDs: json.RawMessage(`["gpt-5"]`),
		}
		legacyFiltered := filterGitHubCopilotAllModels(models, legacyCred)
		if len(legacyFiltered) != 3 {
			t.Fatalf("legacyFiltered len = %d, want 3", len(legacyFiltered))
		}
	})

	t.Run("github-copilot registers both openai-decisions and typesafe-system-one", func(t *testing.T) {
		cls := builtinClassifiers("github-copilot")
		if cls[ClassifierAPIOpenAIDecisions] == nil || cls[ClassifierAPITypesafeSystemOne] == nil {
			t.Fatalf("expected both openai-decisions and typesafe-system-one classifiers, got %v", cls)
		}
	})
}
