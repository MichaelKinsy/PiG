package ai

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// strippedAPIs lists the APIs this process runs without. It is empty in Stock PiG.
func strippedAPIs() []API {
	ids := pigstrip.IDs(pigstrip.ListAPIs)
	apis := make([]API, len(ids))
	for i, id := range ids {
		apis[i] = API(id)
	}
	return apis
}

// OfferedModels returns models without the chat models of a stripped API. The generated catalog listing (ListModels) and every Models collection read pass their models through it, so a stripped API's models are offered nowhere: not by --list-models, /model, the --models scope, startup's default model, RPC or the extension model registry. An exact catalog lookup (LookupModel, LookupModelExact) still finds such a model, so that using one reports the strip.
// pig additive (D92): Stock PiG strips no API and returns models unchanged.
func OfferedModels[T AnyModel](models []T) []T {
	stripped := strippedAPIs()
	if len(stripped) == 0 {
		return models
	}
	return slices.DeleteFunc(slices.Clone(models), func(model T) bool {
		chat, ok := any(model).(*Model)
		return ok && chat != nil && slices.Contains(stripped, chat.ProviderMeta.API)
	})
}

// StrippedModelError reports the strip that keeps a catalog model from being offered: the stripped API of providerID's catalog model modelID or, when that catalog lacks modelID, the stripped API of a provider whose catalog models are all on stripped APIs. An empty providerID matches modelID under every provider. Matching ignores case, as model resolution does. It returns nil when no strip explains the model's absence.
// pig additive (D92): Stock PiG strips no API, so this is always nil there.
func StrippedModelError(providerID, modelID string) error {
	registryOnce.Do(initRegistry)
	if providerID == "" {
		for _, model := range GeneratedModels {
			if strings.EqualFold(model.ID, modelID) && pigstrip.Has(pigstrip.ListAPIs, string(model.API)) {
				return strippedAPIError(model.Provider, model.API)
			}
		}
		return nil
	}
	models := registryByProvider[strings.ToLower(providerID)]
	if index := slices.IndexFunc(models, func(model *GeneratedModel) bool { return strings.EqualFold(model.ID, modelID) }); index >= 0 {
		if model := models[index]; pigstrip.Has(pigstrip.ListAPIs, string(model.API)) {
			return strippedAPIError(model.Provider, model.API)
		}
		return nil
	}
	if !ProviderStripped(providerID) {
		return nil
	}
	return strippedAPIError(models[0].Provider, models[0].API)
}

// ProviderStripped reports whether every generated catalog model of providerID is on a stripped API, so the provider
// offers no model and is no login provider. A provider without catalog models is never stripped. Matching ignores case.
// pig additive (D92): Stock PiG strips no API, so this is always false there.
func ProviderStripped(providerID string) bool {
	if len(strippedAPIs()) == 0 {
		return false
	}
	registryOnce.Do(initRegistry)
	models := registryByProvider[strings.ToLower(providerID)]
	return len(models) > 0 && !slices.ContainsFunc(models, func(model *GeneratedModel) bool { return !pigstrip.Has(pigstrip.ListAPIs, string(model.API)) })
}

// strippedAPIError is the error building providerID's provider reports when its API is stripped.
func strippedAPIError(providerID string, api API) error {
	return pigstrip.Error(fmt.Sprintf("Provider %s: API %s", providerID, api), pigstrip.ListAPIs, string(api))
}

func strippedBedrockError(model *Model) error {
	return strippedAPIError(cmp.Or(modelProviderID(model), "amazon-bedrock"), APIBedrockConverseStream)
}

func strippedGoogleVertexError(cfg *GoogleVertexConfig) error {
	return strippedAPIError(cmp.Or(cfg.ProviderID, string(APIGoogleVertex)), APIGoogleVertex)
}

func strippedMistralError(cfg *MistralConfig) error {
	return strippedAPIError(cmp.Or(cfg.ProviderID, "mistral"), APIMistralConversations)
}
