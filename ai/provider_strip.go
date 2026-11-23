package ai

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

type strippedAPIsSnapshot struct {
	gen  uint64
	apis []API
}

var currentStrippedAPIs atomic.Pointer[strippedAPIsSnapshot]

// strippedAPIs lists the APIs this process runs without. It is empty in Stock PiG.
// The snapshot is keyed by pigstrip.Generation so lookups allocate nothing while the strip state is unchanged; callers must not mutate the returned slice.
func strippedAPIs() []API {
	gen := pigstrip.Generation()
	snap := currentStrippedAPIs.Load()
	for snap == nil || snap.gen < gen {
		ids := pigstrip.IDs(pigstrip.ListAPIs)
		var apis []API
		if len(ids) > 0 {
			apis = make([]API, len(ids))
			for i, id := range ids {
				apis[i] = API(id)
			}
		}
		fresh := &strippedAPIsSnapshot{gen: gen, apis: apis}
		if currentStrippedAPIs.CompareAndSwap(snap, fresh) {
			return fresh.apis
		}
		snap = currentStrippedAPIs.Load()
	}
	return snap.apis
}

// modelOffered reports whether model is offered under the current stripped API set. Non-chat models and nil models are unaffected by API strips.
func modelOffered[T AnyModel](model T) bool {
	chat, ok := any(model).(*Model)
	if !ok || chat == nil {
		return true
	}
	return !slices.Contains(strippedAPIs(), chat.ProviderMeta.API)
}

// OfferedModels returns models without the chat models of a stripped API. The generated catalog listing (ListModels) and every Models collection read omit them (via OfferedModels or modelOffered), so a stripped API's models are offered nowhere: not by --list-models, /model, the --models scope, startup's default model, RPC or the extension model registry. An exact catalog lookup (LookupModel, LookupModelExact) still finds such a model, so that using one reports the strip.
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

// OfferedGeneratedModels returns pointers to the entries of models whose built models OfferedModels keeps, in catalog order.
func OfferedGeneratedModels(models []GeneratedModel) []*GeneratedModel {
	stripped := strippedAPIs()
	offered := make([]*GeneratedModel, 0, len(models))
	for i := range models {
		if !slices.Contains(stripped, models[i].API) {
			offered = append(offered, &models[i])
		}
	}
	return offered
}

// StrippedModelError reports the strip that keeps a catalog model from being offered: the stripped API of providerID's catalog model modelID or, when that catalog lacks modelID, the stripped API of a provider whose catalog models are all on stripped APIs. An empty providerID matches modelID under every provider. Matching ignores case, as model resolution does. It returns nil when no strip explains the model's absence.
// pig additive (D92): Stock PiG strips no API, so this is always nil there.
func StrippedModelError(providerID, modelID string) error {
	if providerID == "" {
		for _, model := range GeneratedModels {
			if strings.EqualFold(model.ID, modelID) && pigstrip.Has(pigstrip.ListAPIs, string(model.API)) {
				return strippedAPIError(model.Provider, model.API)
			}
		}
		return nil
	}
	registryProviderOnce.Do(initRegistryProvider)
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
	registryProviderOnce.Do(initRegistryProvider)
	models := registryByProvider[strings.ToLower(providerID)]
	return len(models) > 0 && !slices.ContainsFunc(models, func(model *GeneratedModel) bool { return !pigstrip.Has(pigstrip.ListAPIs, string(model.API)) })
}

// strippedDirectAPIError is the strip error of api for model, the one the API's provider constructor reports, or nil when api is not stripped. The stream entry points ask it before they look at any credential, so a request for a stripped API ends with the strip whether or not it carries a key.
// pig additive (D92): strips are PiG's own, so Pi has no order of the strip and the missing-key errors to match; Stock PiG strips no API and keeps Pi's order of errors.
func strippedDirectAPIError(api API, model *Model, env ProviderEnv) error {
	if !pigstrip.Has(pigstrip.ListAPIs, string(api)) {
		return nil
	}
	_, err := directAPIProviderFor(api, model, "", env)
	return err
}

// strippedAPIError is the error building providerID's provider reports when its API is stripped.
func strippedAPIError(providerID string, api API) error {
	return pigstrip.Error(fmt.Sprintf("Provider %s: API %s", providerID, api), pigstrip.ListAPIs, string(api))
}

func strippedGoogleVertexError(cfg *GoogleVertexConfig) error {
	return strippedAPIError(cmp.Or(cfg.ProviderID, string(APIGoogleVertex)), APIGoogleVertex)
}

func strippedMistralError(cfg *MistralConfig) error {
	return strippedAPIError(cmp.Or(cfg.ProviderID, "mistral"), APIMistralConversations)
}
