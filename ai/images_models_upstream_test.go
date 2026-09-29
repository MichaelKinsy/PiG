package ai

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func imageRuntimeTestModel(provider, id string) ImagesModel {
	return ImagesModel{ID: id, Name: id, API: "test-images", Provider: ImagesProviderId(provider), BaseURL: "https://example.test/v1", Input: []string{"text"}, Output: []string{"image"}}
}

func imageRuntimeResult(model ImagesModel) AssistantImages {
	return AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ContentBlock{ImageContent{Data: "aGk=", MimeType: "image/png"}}, StopReason: ImagesStopReasonStop, Timestamp: time.Now().UnixMilli()}
}

type imageRuntimeCall struct {
	model   ImagesModel
	options ImagesOptions
}

func imageRuntimeTestProvider(id, envVar string, models []ImagesModel, calls *[]imageRuntimeCall) *ImagesProvider {
	if models == nil {
		models = []ImagesModel{imageRuntimeTestModel(id, "model-a")}
	}
	return CreateImagesProvider(CreateImagesProviderOptions{ID: id, Models: models, Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test key", Resolve: func(_ context.Context, input APIKeyAuthInput) (*AuthResult, error) {
		if envVar == "" {
			return &AuthResult{}, nil
		}
		key, _ := input.Ctx.Env(envVar)
		source := envVar
		if input.Credential != nil {
			key = input.Credential.Key
			source = "stored"
		}
		if key == "" {
			return nil, nil
		}
		return &AuthResult{Auth: ModelAuth{APIKey: key}, Source: source}, nil
	}}}, API: ProviderImages{GenerateImages: func(_ context.Context, model ImagesModel, _ ImagesContext, options ImagesOptions) (AssistantImages, error) {
		if calls != nil {
			*calls = append(*calls, imageRuntimeCall{model, options})
		}
		return imageRuntimeResult(model), nil
	}}})
}

func imageRuntimeAuthContext(env map[string]string) *AuthContext {
	return &AuthContext{Env: func(name string) (string, bool) { value, ok := env[name]; return value, ok }, FileExists: func(string) bool { return false }}
}

func TestImagesModelsUpstream(t *testing.T) {
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}
	// .upstream/v0.87.1/packages/ai/test/images-models.test.ts:74
	t.Run("registers providers and reads models synchronously", func(t *testing.T) {
		models := CreateImagesModels()
		models.SetProvider(imageRuntimeTestProvider("p1", "", []ImagesModel{imageRuntimeTestModel("p1", "m1"), imageRuntimeTestModel("p1", "m2")}, nil))
		models.SetProvider(imageRuntimeTestProvider("p2", "", []ImagesModel{imageRuntimeTestModel("p2", "m3")}, nil))
		providers := models.GetProviders()
		if len(providers) != 2 || providers[0].ID != "p1" || providers[1].ID != "p2" {
			t.Fatalf("providers=%v", providers)
		}
		ids := func(list []ImagesModel) []string {
			out := []string{}
			for _, m := range list {
				out = append(out, m.ID)
			}
			return out
		}
		if got := ids(models.GetModels()); !reflect.DeepEqual(got, []string{"m1", "m2", "m3"}) {
			t.Fatalf("models=%v", got)
		}
		if got := ids(models.GetModels("p1")); !reflect.DeepEqual(got, []string{"m1", "m2"}) {
			t.Fatalf("p1=%v", got)
		}
		if got := models.GetModel("p2", "m3"); got == nil || got.ID != "m3" {
			t.Fatalf("model=%v", got)
		}
		if models.GetModel("p2", "missing") != nil {
			t.Fatal("found missing model")
		}
		models.DeleteProvider("p1")
		if models.GetProvider("p1") != nil {
			t.Fatal("provider not deleted")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/images-models.test.ts:89
	t.Run("resolves auth through the provider and merges it into requests; explicit options win", func(t *testing.T) {
		var calls []imageRuntimeCall
		models := CreateImagesModels(CreateModelsOptions{AuthContext: imageRuntimeAuthContext(map[string]string{"TEST_KEY": "env-key"})})
		models.SetProvider(imageRuntimeTestProvider("p1", "TEST_KEY", nil, &calls))
		model := *models.GetModel("p1", "model-a")
		modelAuth, err := models.GetModelAuth(t.Context(), model)
		if err != nil || modelAuth == nil || modelAuth.Auth.APIKey != "env-key" {
			t.Fatalf("model auth=%v, err=%v", modelAuth, err)
		}
		providerAuth, err := models.GetAuth(t.Context(), string(model.Provider))
		if err != nil || providerAuth == nil || providerAuth.Auth.APIKey != "env-key" {
			t.Fatalf("provider auth=%v, err=%v", providerAuth, err)
		}
		explicit, err := models.GetModelAuth(t.Context(), model, AuthResolutionOverrides{APIKey: new("explicit-key")})
		if err != nil || explicit == nil || explicit.Auth.APIKey != "explicit-key" {
			t.Fatalf("explicit auth=%v, err=%v", explicit, err)
		}
		result := models.GenerateImages(t.Context(), model, request)
		if result.StopReason != ImagesStopReasonStop || len(calls) != 1 || calls[0].options.APIKey != "env-key" {
			t.Fatalf("result=%+v calls=%+v", result, calls)
		}
		models.GenerateImages(t.Context(), model, request, ImagesOptions{APIKey: "explicit"})
		if len(calls) != 2 || calls[1].options.APIKey != "explicit" {
			t.Fatalf("calls=%+v", calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/images-models.test.ts:107
	t.Run("merges provider-resolved env into image options", func(t *testing.T) {
		var calls []imageRuntimeCall
		models := CreateImagesModels()
		provider := imageRuntimeTestProvider("p1", "", nil, &calls)
		provider.Auth.APIKey.Resolve = func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
			return &AuthResult{Auth: ModelAuth{APIKey: "provider-key"}, Env: map[string]string{"PROVIDER_ONLY": "provider", "SHARED": "provider"}}, nil
		}
		models.SetProvider(provider)
		models.GenerateImages(t.Context(), *models.GetModel("p1", "model-a"), request, ImagesOptions{APIKey: "request-key", Env: map[string]string{"REQUEST_ONLY": "request", "SHARED": "request"}})
		want := map[string]string{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}
		if len(calls) != 1 || calls[0].options.APIKey != "request-key" || !reflect.DeepEqual(calls[0].options.Env, want) {
			t.Fatalf("calls=%+v", calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/images-models.test.ts:146
	t.Run("returns an error result for unknown providers and unconfigured auth rejections", func(t *testing.T) {
		models := CreateImagesModels(CreateModelsOptions{AuthContext: imageRuntimeAuthContext(nil)})
		ghost := models.GenerateImages(t.Context(), imageRuntimeTestModel("ghost", "m"), request)
		if ghost.StopReason != ImagesStopReasonError || ghost.ErrorMessage != "Unknown provider: ghost" {
			t.Fatalf("ghost=%+v", ghost)
		}
		var calls []imageRuntimeCall
		models.SetProvider(imageRuntimeTestProvider("p1", "MISSING", nil, &calls))
		model := *models.GetModel("p1", "model-a")
		if auth, err := models.GetModelAuth(t.Context(), model); auth != nil || err != nil {
			t.Fatalf("auth=%v, err=%v", auth, err)
		}
		models.GenerateImages(t.Context(), model, request)
		if len(calls) != 1 || calls[0].options.APIKey != "" || calls[0].options.APIKeySet {
			t.Fatalf("calls=%+v", calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/images-models.test.ts:161
	t.Run("supports dynamic providers via refresh with in-flight dedupe", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var fetches atomic.Int32
			models := CreateImagesModels()
			provider := CreateImagesProvider(CreateImagesProviderOptions{ID: "dyn", Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}, Models: []ImagesModel{}, RefreshModels: func() ([]ImagesModel, error) {
				fetches.Add(1)
				time.Sleep(5 * time.Millisecond)
				return []ImagesModel{imageRuntimeTestModel("dyn", "listed")}, nil
			}, API: ProviderImages{GenerateImages: func(_ context.Context, m ImagesModel, _ ImagesContext, _ ImagesOptions) (AssistantImages, error) {
				return imageRuntimeResult(m), nil
			}}})
			models.SetProvider(provider)
			if got := models.GetModels("dyn"); !reflect.DeepEqual(got, []ImagesModel{}) {
				t.Fatalf("initial=%v", got)
			}
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i := range errs {
				wg.Go(func() { errs[i] = models.Refresh("dyn") })
			}
			wg.Wait()
			if errs[0] != nil || errs[1] != nil || fetches.Load() != 1 || models.GetModel("dyn", "listed") == nil {
				t.Fatalf("errors=%v fetches=%d model=%v", errs, fetches.Load(), models.GetModel("dyn", "listed"))
			}
			models.SetProvider(CreateImagesProvider(CreateImagesProviderOptions{ID: "flaky", Auth: provider.Auth, Models: []ImagesModel{}, RefreshModels: func() ([]ImagesModel, error) { return nil, errors.New("fetch failed") }, API: ProviderImages{GenerateImages: provider.GenerateImages}}))
			err := models.Refresh("flaky")
			if coded, ok := errors.AsType[*ModelsError](err); !ok || coded.Code != ModelsErrorModelSource {
				t.Fatalf("error=%v", err)
			}
			if err := models.Refresh(); err != nil {
				t.Fatal(err)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/images-models.test.ts:198
	t.Run("builtinImagesModels registers the openrouter provider with its catalog", func(t *testing.T) {
		models := BuiltinImagesModels(CreateModelsOptions{AuthContext: imageRuntimeAuthContext(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
		providers := models.GetProviders()
		if len(providers) != 1 || providers[0].ID != "openrouter" {
			t.Fatalf("providers=%v", providers)
		}
		list := models.GetModels("openrouter")
		if len(list) == 0 {
			t.Fatal("empty builtin catalog")
		}
		for _, model := range list {
			if model.API != APIImagesOpenRouter {
				t.Fatalf("API=%s", model.API)
			}
		}
		auth, err := models.GetModelAuth(t.Context(), list[0])
		if err != nil || auth == nil || auth.Auth.APIKey != "or-key" {
			t.Fatalf("auth=%v, err=%v", auth, err)
		}
	})
}
