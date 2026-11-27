package ai

// Ports packages/ai/src/providers/github-copilot.ts filterModels and filterAllModels.

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// githubCopilotV1 is providers/github-copilot.ts copilotV1. Copilot serves its classifier APIs under /v1
// (/v1/systemone, /v1/decisions), while its chat APIs have no version prefix. The login replaces the catalog
// base URL with the account's endpoint, so the prefix is added per request.
func githubCopilotV1(classifier *ProviderClassifier) *ProviderClassifier {
	return &ProviderClassifier{Classify: func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		versioned := *model
		versioned.BaseURL = strings.TrimRight(model.BaseURL, "/") + "/v1"
		return classifier.Classify(ctx, &versioned, request, options)
	}}
}

func copilotAccountModelIDs(rawIDs json.RawMessage) ([]string, bool) {
	var raw []json.RawMessage
	if err := json.Unmarshal(rawIDs, &raw); err != nil || bytes.Equal(bytes.TrimSpace(rawIDs), []byte("null")) {
		return nil, false
	}
	available := make([]string, 0, len(raw))
	for _, element := range raw {
		var id string
		if err := json.Unmarshal(element, &id); err != nil || len(element) == 0 || element[0] != '"' {
			return nil, false
		}
		available = append(available, id)
	}
	return available, true
}

// filterGitHubCopilotModels keeps the models an OAuth account's availableModelIds lists. Any other credential, and an availableModelIds value that is not an array of strings, keeps every model; an explicit empty array keeps none.
func filterGitHubCopilotModels(models []*Model, credential *Credential) []*Model {
	if credential == nil || credential.Type != CredentialOAuth {
		return models
	}
	available, ok := copilotAccountModelIDs(credential.AvailableModelIDs)
	if !ok {
		return models
	}
	return slices.DeleteFunc(slices.Clone(models), func(model *Model) bool { return !slices.Contains(available, model.ID) })
}

// filterGitHubCopilotAllModels filters chat models by availableModelIds and classifier models by availableClassifierModelIds.
func filterGitHubCopilotAllModels(models []AnyModel, credential *Credential) []AnyModel {
	if credential == nil || credential.Type != CredentialOAuth {
		return models
	}
	chat, hasChat := copilotAccountModelIDs(credential.AvailableModelIDs)
	classifiers, hasClassifiers := copilotAccountModelIDs(credential.AvailableClassifierModelIDs)
	return slices.DeleteFunc(slices.Clone(models), func(model AnyModel) bool {
		if IsModelType(model, ModelTypeClassifier) {
			return hasClassifiers && !slices.Contains(classifiers, model.ModelID())
		}
		return hasChat && !slices.Contains(chat, model.ModelID())
	})
}
