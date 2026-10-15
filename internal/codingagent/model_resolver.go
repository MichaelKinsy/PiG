package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ModelResolverRuntime supplies the catalog and configured-auth observations used by CLI resolution. Selection does not resolve credentials or refresh the catalog.
type ModelResolverRuntime interface {
	GetModels(providerID string) []RuntimeModel
	HasConfiguredAuth(providerID string) bool
}

// ResolveCliModelResult carries a model selection, optional thinking suffix, warning, or CLI diagnostic.
type ResolveCliModelResult struct {
	Model         *RuntimeModel
	ThinkingLevel ai.ThinkingLevel
	Warning       string
	Error         string
}

// buildFallbackModel copies a provider's default model, or its first model, under the requested custom ID.
// upstream: model-resolver.ts buildFallbackModel
func buildFallbackModel(providerModels []RuntimeModel, modelID string) *RuntimeModel {
	base := providerModels[0]
	if defaultID, ok := DefaultModelPerProvider()[base.Provider]; ok {
		if index := slices.IndexFunc(providerModels, func(model RuntimeModel) bool { return model.ID == defaultID }); index >= 0 {
			base = providerModels[index]
		}
	}
	base.ID = modelID
	base.Name = modelID
	if source := base.SourceModel(); source != nil {
		native := *source
		native.ID, native.DisplayName = modelID, modelID
		base.NativeModel = &native
	}
	return &base
}

// ResolveCliModel resolves explicit provider/model selections against all catalog models, not only available models. It parses a thinking suffix without applying it to a Session.
// inlineApiKey is --api-key supplied for the selected provider.
func ResolveCliModel(cliProvider, cliModel, cliThinking string, inlineApiKey bool, runtime ModelResolverRuntime) ResolveCliModelResult {
	if cliModel == "" {
		return ResolveCliModelResult{}
	}
	// Important: use *all* models here, not just models with pre-configured auth.
	// This allows "--api-key" to be used for first-time setup.
	models := runtime.GetModels("")
	if len(models) == 0 {
		return ResolveCliModelResult{Error: "No models available. Check your installation or add models to models.json."}
	}
	provider := ""
	if cliProvider != "" {
		for _, model := range models {
			if jsstring.ToLower(model.Provider) == jsstring.ToLower(cliProvider) {
				provider = model.Provider
				break
			}
		}
		if provider == "" {
			if err := strippedCliModelError(cliProvider, cliModel); err != nil {
				return ResolveCliModelResult{Error: err.Error()}
			}
			return ResolveCliModelResult{Error: fmt.Sprintf(`Unknown provider "%s". Use --list-models to see available providers/models.`, cliProvider)}
		}
	}

	usable := runtime.HasConfiguredAuth
	if inlineApiKey {
		// An inline key goes to whichever provider is selected, so stored credentials must not choose it
		usable = func(string) bool { return false }
	}
	result := ResolveModelReference(ModelReferenceRequest{Text: cliModel, Provider: provider, ThinkingSuffix: ThinkingSuffixStrict, Fuzzy: true}, models, usable)
	switch result.Status {
	case ModelReferenceResolved:
		return ResolveCliModelResult{Model: result.Model, ThinkingLevel: ai.ThinkingLevel(result.ThinkingLevel), Warning: result.Warning}
	case ModelReferenceAmbiguous:
		refs := make([]string, 0, len(result.Candidates))
		for _, model := range result.Candidates {
			refs = append(refs, modelRef(model))
		}
		collator := jsstring.NewCollator()
		slices.SortStableFunc(refs, collator.CompareString)
		hint := "More than one matching provider is authenticated."
		if inlineApiKey {
			hint = "--api-key does not choose a provider."
		} else if result.UsableCount == 0 {
			hint = "No matching provider is authenticated."
		}
		return ResolveCliModelResult{Error: fmt.Sprintf(`Model "%s" is ambiguous across providers: %s. %s Use --provider or provider/model.`, cliModel, strings.Join(refs, ", "), hint)}
	}

	target := result.Target
	if target == nil {
		if err := strippedCliModelError(cliProvider, cliModel); err != nil {
			return ResolveCliModelResult{Error: err.Error()}
		}
		return ResolveCliModelResult{Error: fmt.Sprintf(`Model "%s" not found. Use --list-models to see available models.`, cliModel)}
	}

	// Use the thinking level suffix only when --thinking is not explicitly provided; otherwise it
	// stays part of the custom model id.
	// e.g. "zai-org/GLM-5.1-FP8:high" -> modelId="zai-org/GLM-5.1-FP8", fallbackThinking="high"
	modelID, fallbackThinking := target.ModelID, target.ThinkingLevel
	if cliThinking != "" {
		modelID, fallbackThinking = target.ModelText, ""
	}
	model := buildFallbackModel(target.ProviderModels, modelID)
	requestedThinking := cliThinking
	if requestedThinking == "" {
		requestedThinking = fallbackThinking
	}
	if requestedThinking != "" && requestedThinking != "off" {
		model.Reasoning = true
		if model.NativeModel != nil {
			model.NativeModel.ProviderMeta.Reasoning = true
		}
	}
	return ResolveCliModelResult{Model: model, ThinkingLevel: ai.ThinkingLevel(fallbackThinking), Warning: fmt.Sprintf(`Model "%s" not found for provider "%s". Using custom model id.`, modelID, target.Provider)}
}

// strippedCliModelError is the strip that keeps an unresolved --provider/--model selection out of the offered catalog, or nil. Using a model of a stripped API reports the strip, so naming one does too instead of reporting an unknown provider or model.
// pig additive (D92): Stock PiG strips no API, so this is always nil there.
func strippedCliModelError(cliProvider, cliModel string) error {
	if cliProvider != "" {
		modelID := cliModel
		provider := jsstring.ToLower(cliProvider)
		for i := 0; i < len(cliModel); i++ {
			if cliModel[i] == '/' && jsstring.ToLower(cliModel[:i]) == provider {
				modelID = cliModel[i+1:]
				break
			}
		}
		return ai.StrippedModelError(cliProvider, modelID)
	}
	if provider, modelID, ok := strings.Cut(cliModel, "/"); ok {
		if err := ai.StrippedModelError(provider, modelID); err != nil {
			return err
		}
	}
	return ai.StrippedModelError("", cliModel)
}
