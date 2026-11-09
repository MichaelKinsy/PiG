package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 1.5.0's provider config capabilities for every SDK against the real subprocess Host: oauth.usesCallbackServer (deprecated, ignored by Pi's auth flows, types.ts:2026), oauth.modifyModels (types.ts:2034, applied by provider-composer.ts:566-575) and refreshModels (types.ts:1930, composed at provider-composer.ts:624-640).
//
// Every SDK registers the provider "dyn" (testfixture/modeltypes/dynamic.go, testdata/model-types.mjs, testdata/modeltypes-rust, the Python fixture in model_types_test.go, testdata/lisp-sdk-fixture/probes-model.lisp) and the callbacks compute values no fallback can: modifyModels drops the id "drop", reverses the order and appends the credential's access token to each name; refreshModels reports every context field it received and whether its publish calls were accepted and ran its update. An SDK that never sets has_modify_models, never forwards the flag, never sends a publish callback or ignores the context fails a row.

// dynamicProviderConfig returns the config of "dyn" the registry holds after the extension's registrations: every SDK declares the provider twice while its factory runs (the models and refreshModels in one declaration, the OAuth capability in the other; Node merges its root itself), Pi queues both and the registry merges the defined values (loader.ts:218-219, runner.ts:463-480, model-runtime.ts:919-940), so one config carries both. A host that kept only the last declaration of a name fails every row that reads it.
func dynamicProviderConfig(t *testing.T, r *modelTypesRig) extension.ProviderConfig {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var config extension.ProviderConfig
	var haveRefresh, haveOAuth bool
	count := 0
	for _, registration := range r.registrations {
		if registration.Name != "dyn" {
			continue
		}
		count++
		if registration.Config.RefreshModels != nil && !haveRefresh {
			config.RefreshModels, haveRefresh = registration.Config.RefreshModels, true
		}
		if registration.Config.OAuth != nil && !haveOAuth {
			config.OAuth, haveOAuth = registration.Config.OAuth, true
		}
	}
	if !haveRefresh || !haveOAuth {
		t.Fatalf("the registry received %d registrations of provider dyn; refreshModels kept = %v, oauth kept = %v", count, haveRefresh, haveOAuth)
	}
	return config
}

func dynamicChatModel(id, name string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: name, Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "dyn", API: "dyn-api", BaseURL: "https://dyn.test/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
}

// types.ts:2026: usesCallbackServer is deprecated and Pi's canonical auth flows ignore it. The host carries the extension's value in the registered config and the OAuth provider it registers never asks for a callback server. Not parallel: the ai OAuth registry is process-wide, and serial tests never overlap the parallel ones.
func TestProviderOAuthUsesCallbackServerAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		config := dynamicProviderConfig(t, r)
		if !config.OAuth.UsesCallbackServer {
			t.Fatalf("oauth.usesCallbackServer = false, the extension set it: %+v", config.OAuth)
		}
		provider, ok := ai.GetOAuthProvider("dyn")
		if !ok {
			t.Fatal("the host registered no OAuth provider for dyn")
		}
		if provider.UsesCallbackServer() {
			t.Fatal("the registered OAuth provider asks for a callback server, which Pi ignores")
		}
	})
}

// types.ts:2034 and provider-composer.ts:566-575: modifyModels runs in the extension with the chat models and the stored OAuth credential and returns the models that replace them; an exception of the callback is the error of the model listing.
func TestProviderOAuthModifyModelsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		config := dynamicProviderConfig(t, r)
		modify := config.OAuth.ModifyModels
		if modify == nil {
			t.Fatalf("oauth.modifyModels was not registered: %+v", config.OAuth)
		}
		models, err := modify([]*ai.Model{dynamicChatModel("a", "A"), dynamicChatModel("drop", "Drop"), dynamicChatModel("b", "B")}, ai.OAuthCredentials{Access: "tok"})
		if err != nil {
			t.Fatal(err)
		}
		var ids, names []string
		for _, model := range models {
			ids = append(ids, model.ID)
			names = append(names, model.DisplayName)
		}
		if want := []string{"b", "a"}; !slices.Equal(ids, want) || !slices.Equal(names, []string{"B+tok", "A+tok"}) {
			t.Fatalf("modified models = %v %v, want ids %v and names [B+tok A+tok]", ids, names, want)
		}
		for _, model := range models {
			if model.ProviderMeta.ProviderID != "dyn" || model.ProviderMeta.BaseURL != "https://dyn.test/v1" {
				t.Fatalf("modified model %s lost its provider metadata: %+v", model.ID, model.ProviderMeta)
			}
		}
		// The credential decides the answer: a second credential is a second projection, not the first one replayed.
		again, err := modify([]*ai.Model{dynamicChatModel("a", "A")}, ai.OAuthCredentials{Access: "other"})
		if err != nil || len(again) != 1 || again[0].DisplayName != "A+other" {
			t.Fatalf("second projection = %+v, %v", again, err)
		}
		if _, err := modify([]*ai.Model{dynamicChatModel("a", "A")}, ai.OAuthCredentials{Access: "boom"}); err == nil || !strings.Contains(err.Error(), "modify boom") {
			t.Fatalf("a failing modifyModels = %v, want the extension's error", err)
		}
	})
}

// types.ts:1930 and provider-composer.ts:624-640: refreshModels runs in the extension with the refresh context, its publish calls reach the host's refresh while it runs (a persisted catalog, then a publication whose update runs in the extension and whose acceptance the callback sees) and the models it returns are the result.
func TestProviderRefreshModelsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		config := dynamicProviderConfig(t, r)
		type published struct {
			persisted *ai.ModelsStoreEntry
			update    bool
		}
		refresh := func(context ai.RefreshModelsContext, accept bool) (string, []published, error) {
			var publications []published
			context.Signal = t.Context()
			context.Publish = func(publication ai.ModelsPublication) (bool, error) {
				publications = append(publications, published{persisted: publication.Persist, update: publication.Update != nil})
				if publication.Update != nil {
					publication.Update()
				}
				return accept, nil
			}
			models, err := config.RefreshModels(context)
			if err != nil {
				return "", publications, err
			}
			if len(models) != 1 || models[0].ID != "r1" {
				t.Fatalf("refreshed models = %+v", models)
			}
			return models[0].Name, publications, nil
		}

		force := true
		name, publications, err := refresh(ai.RefreshModelsContext{
			Credential: &ai.Credential{Type: ai.CredentialOAuth, Access: "tok-1"}, Stored: &ai.ModelsStoreEntry{CheckedAt: new(float64(77))},
			AllowNetwork: true, Force: &force, BaseURL: "https://dyn.test/v1",
		}, true)
		if err != nil {
			t.Fatal(err)
		}
		if want := "refresh|net=true|force=true|cred=tok-1|stored=77|base=https://dyn.test/v1|updated=true|accepted=true"; name != want {
			t.Fatalf("refreshed model name = %q, want %q", name, want)
		}
		if len(publications) != 2 || publications[0].persisted == nil || publications[0].persisted.CheckedAt == nil || *publications[0].persisted.CheckedAt != 99 || len(publications[0].persisted.Models) != 0 || publications[0].update || publications[1].persisted != nil || !publications[1].update {
			t.Fatalf("publications = %+v, want a persisted catalog (checkedAt 99) and then an update", publications)
		}

		// An empty context reaches the callback as absent members, and a refused publication reaches it as false.
		name, _, err = refresh(ai.RefreshModelsContext{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if want := "refresh|net=false|force=none|cred=none|stored=none|base=none|updated=true|accepted=false"; name != want {
			t.Fatalf("refreshed model name = %q, want %q", name, want)
		}

		// A callback's exception is the refresh's error.
		if _, _, err := refresh(ai.RefreshModelsContext{Credential: &ai.Credential{Type: ai.CredentialOAuth, Access: "boom"}}, true); err == nil || !strings.Contains(err.Error(), "refresh boom") {
			t.Fatalf("a failing refreshModels = %v, want the extension's error", err)
		}

		// A cancelled refresh returns without the callback's answer.
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := config.RefreshModels(ai.RefreshModelsContext{Signal: cancelled, Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }}); err == nil {
			t.Fatal("a refresh with a cancelled signal returned no error")
		}
	})
}

// provider-composer.ts:564-569 spreads modifyModels' result into the model list, so a result that is not iterable is the TypeError of that spread. The Node runtime runs the same spread and reports V8's text; the typed SDKs cannot return a non-iterable value.
func TestProviderOAuthModifyModelsNonIterableNode(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacementOf(t, []string{"node"}, func(t *testing.T, r *modelTypesRig) {
		config := dynamicProviderConfig(t, r)
		if config.OAuth.ModifyModels == nil {
			t.Fatalf("oauth.modifyModels was not registered: %+v", config.OAuth)
		}
		_, err := config.OAuth.ModifyModels([]*ai.Model{dynamicChatModel("a", "A")}, ai.OAuthCredentials{Access: "nonarray"})
		if err == nil || !strings.Contains(err.Error(), "is not iterable") || strings.Contains(err.Error(), "must return an array") {
			t.Fatalf("a non-iterable modifyModels result = %v, want Pi's TypeError (\"... is not iterable\")", err)
		}
	})
}
