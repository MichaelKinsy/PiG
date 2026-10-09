package ai

// Ports packages/ai/src/providers/github-copilot.ts filterModels.

import (
	"bytes"
	"encoding/json"
	"slices"
)

// filterGitHubCopilotModels keeps the models an OAuth account's availableModelIds lists. Any other credential, and an availableModelIds value that is not an array of strings, keeps every model; an explicit empty array keeps none.
func filterGitHubCopilotModels(models []*Model, credential *Credential) []*Model {
	if credential == nil || credential.Type != CredentialOAuth {
		return models
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(credential.AvailableModelIDs, &raw); err != nil || bytes.Equal(bytes.TrimSpace(credential.AvailableModelIDs), []byte("null")) {
		return models
	}
	available := make([]string, 0, len(raw))
	for _, element := range raw {
		var id string
		if err := json.Unmarshal(element, &id); err != nil || len(element) == 0 || element[0] != '"' {
			return models
		}
		available = append(available, id)
	}
	return slices.DeleteFunc(slices.Clone(models), func(model *Model) bool { return !slices.Contains(available, model.ID) })
}
