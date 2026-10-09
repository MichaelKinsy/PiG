package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Ports .upstream/v0.99.1/packages/ai/test/images-models.test.ts, the source-only cases of
// model-types.test.ts, and the added case of models-runtime.test.ts.
// "rejects chat models at the image entry point at runtime" and the compile-time case of model-types.test.ts
// are unrepresentable in Go: Models.GenerateImages takes *ImageModel.

func typedChatModel(provider, id string) *Model {
	return &Model{ID: id, DisplayName: id, ProviderMeta: ProviderMetadata{ProviderID: provider, API: "test-chat", BaseURL: "https://example.test/v1"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
}

func typedImageModel(provider, id string) *ImageModel {
	return &ImageModel{ID: id, Name: id, API: "test-images", Provider: provider, BaseURL: "https://example.test/v1", Input: []string{"text"}, Output: []string{"image"}}
}

func typedOKResult(model *ImageModel) AssistantImages {
	return AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ContentBlock{ImageContent{Data: "aGk=", MimeType: "image/png"}}, StopReason: ImagesStopReasonStop, Timestamp: time.Now().UnixMilli()}
}

type typedGenerateCall struct {
	model   *ImageModel
	options ImagesOptions
}

type typedProviderInput struct {
	id     string
	models []AnyModel
	envVar string
	calls  *[]typedGenerateCall
	images []ImageAPI
	// chat replaces the default chat implementation, whose streams never settle.
	chat *ProviderStreams
}

func typedTestProvider(input typedProviderInput) *ModelsProvider {
	generate := func(_ context.Context, model *ImageModel, _ ImagesContext, options ImagesOptions) (AssistantImages, error) {
		if input.calls != nil {
			*input.calls = append(*input.calls, typedGenerateCall{model, options})
		}
		return typedOKResult(model), nil
	}
	imageAPIs := input.images
	if imageAPIs == nil {
		imageAPIs = []ImageAPI{"test-images"}
	}
	images := ProviderImageAPIMap{}
	for _, api := range imageAPIs {
		images[api] = &ProviderImages{GenerateImages: generate}
	}
	models := input.models
	if models == nil {
		models = []AnyModel{typedImageModel(input.id, "model-a")}
	}
	chat := input.chat
	if chat == nil {
		chat = typedChatStreams()
	}
	return CreateProvider(CreateProviderOptions{ID: input.id, Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test key", Resolve: func(_ context.Context, request APIKeyAuthInput) (*AuthResult, error) {
		if input.envVar == "" {
			return &AuthResult{}, nil
		}
		key, _ := request.Ctx.Env(input.envVar)
		source := input.envVar
		if request.Credential != nil {
			key = request.Credential.Key
			source = "stored"
		}
		if key == "" {
			return nil, nil
		}
		return &AuthResult{Auth: ModelAuth{APIKey: key}, Source: source}, nil
	}}}, Models: models, API: ProviderAPIMap{"test-chat": chat}, Images: images})
}

func typedChatStreams() *ProviderStreams {
	stream := func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
		return NewAssistantMessageEventStream(), nil
	}
	return &ProviderStreams{Stream: stream, StreamSimple: stream}
}

func typedAuthContext(env map[string]string) *AuthContext {
	return &AuthContext{Env: func(name string) (string, bool) { value, ok := env[name]; return value, ok }, FileExists: func(string) bool { return false }}
}

func modelIDs[T AnyModel](models []T) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ModelID())
	}
	return ids
}

// Pi: packages/ai/src/auth/resolve.ts:20 (AuthResolutionOverrides.apiKey).
func TestModelTypesUpstream(t *testing.T) {
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:125
	t.Run("treats models without a type as chat models", func(t *testing.T) {
		chat := typedChatModel("p", "c")
		typed := *chat
		typed.Type = ModelTypeChat
		image := typedImageModel("p", "i")
		if chat.Type != "" || GetModelType(chat) != ModelTypeChat || GetModelType(&typed) != ModelTypeChat || GetModelType(image) != ModelTypeImage {
			t.Fatalf("types: chat=%q typed=%q image=%q", GetModelType(chat), GetModelType(&typed), GetModelType(image))
		}
		if !IsModelType(chat, ModelTypeChat) || IsModelType(chat, ModelTypeImage) || !IsModelType(image, ModelTypeImage) {
			t.Fatal("IsModelType disagrees with the model type")
		}
		// HasApi never matches an image model, even on an equal api string.
		sameAPI := *image
		sameAPI.API = "test-chat"
		if HasApi(&sameAPI, "test-chat") || !HasApi(chat, "test-chat") {
			t.Fatal("HasApi matched a non-chat model or missed a chat model")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/model-types.test.ts:83
	t.Run("narrow mixed lists with isModelType", func(t *testing.T) {
		typed := typedChatModel("p", "typed")
		typed.Type = ModelTypeChat
		mixed := []AnyModel{typedChatModel("p", "c"), typed, typedImageModel("p", "i")}
		pick := func(modelType ModelType) []string {
			out := []string{}
			for _, model := range mixed {
				if IsModelType(model, modelType) {
					out = append(out, model.ModelID())
				}
			}
			return out
		}
		if got := pick(ModelTypeChat); !reflect.DeepEqual(got, []string{"c", "typed"}) {
			t.Fatalf("chat=%v", got)
		}
		if got := pick(ModelTypeImage); !reflect.DeepEqual(got, []string{"i"}) {
			t.Fatalf("image=%v", got)
		}
		if got := pick(ModelTypeClassifier); len(got) != 0 {
			t.Fatalf("classifier=%v", got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/model-types.test.ts:54
	t.Run("chat models without a type work through a handwritten provider without GetAllModels", func(t *testing.T) {
		faux := NewFauxProvider(FauxConfig{ProviderID: "handwritten"})
		definition := faux.makeDefinition()
		handwritten := &ModelsProvider{ID: "handwritten", Name: "Handwritten", Auth: definition.Auth, GetModels: func() ([]*Model, error) { return faux.Models(), nil }, Stream: definition.Stream, StreamSimple: definition.StreamSimple}
		models := CreateModels()
		models.SetProvider(handwritten)
		model := models.GetModel("handwritten", faux.Models()[0].ID)
		if model == nil || model.Type != "" || GetModelType(model) != ModelTypeChat || !HasApi(model, model.ProviderMeta.API) {
			t.Fatalf("model=%+v", model)
		}
		typed := *model
		typed.Type = ModelTypeChat
		if !ModelsAreEqual(model, &typed) || ModelsAreEqual(model, typedImageModel(model.ProviderID(), model.ID)) {
			t.Fatal("ModelsAreEqual must compare type, id and provider")
		}
		// toEqual(faux.models) compares the listed models, not only their ids.
		want := AnyModels(faux.Models())
		if got := models.GetModelsOfType(ModelTypeChat, "handwritten"); !reflect.DeepEqual(got, want) {
			t.Fatalf("chat=%v want %v", modelIDs(got), modelIDs(want))
		}
		if got := models.GetAllModels("handwritten"); !reflect.DeepEqual(got, want) {
			t.Fatalf("all=%v want %v", modelIDs(got), modelIDs(want))
		}
		if got := models.GetModelsOfType(ModelTypeImage, "handwritten"); len(got) != 0 {
			t.Fatalf("image=%v", got)
		}
		faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("hi")}})})
		result := models.Complete(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 0}}})
		if result.StopReason != StopReasonStop {
			t.Fatalf("stop=%s error=%q", result.StopReason, result.ErrorMessage)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:144
	t.Run("lists models without a type as chat models at createProvider boundaries", func(t *testing.T) {
		provider := CreateProvider(CreateProviderOptions{ID: "legacy", Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}},
			Models: []AnyModel{typedChatModel("legacy", "static"), typedImageModel("legacy", "static")},
			FetchModels: func(RefreshModelsContext) ([]AnyModel, error) {
				return []AnyModel{typedChatModel("legacy", "dynamic")}, nil
			},
			API: typedChatStreams()})
		models := CreateModels()
		models.SetProvider(provider)
		chatIDs := func() []string {
			list, err := provider.GetModels()
			if err != nil {
				t.Fatal(err)
			}
			for _, model := range list {
				if model.Type != "" {
					t.Fatalf("model %s type=%q", model.ID, model.Type)
				}
			}
			return modelIDs(list)
		}
		if got := chatIDs(); !reflect.DeepEqual(got, []string{"static"}) {
			t.Fatalf("before refresh=%v", got)
		}
		models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{provider.ID}})
		if got := chatIDs(); !reflect.DeepEqual(got, []string{"static", "dynamic"}) {
			t.Fatalf("after refresh=%v", got)
		}
		if got := modelIDs(models.GetModelsOfType(ModelTypeImage, "legacy")); !reflect.DeepEqual(got, []string{"static"}) {
			t.Fatalf("image=%v", got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:165
	t.Run("lists chat, image, and all models through typed accessors", func(t *testing.T) {
		models := CreateModels()
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p1", models: []AnyModel{typedChatModel("p1", "c1"), typedImageModel("p1", "i1"), typedImageModel("p1", "i2")}}))
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p2", models: []AnyModel{typedImageModel("p2", "i3")}}))
		check := func(name string, got []string, want []string) {
			t.Helper()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s=%v want %v", name, got, want)
			}
		}
		check("GetModels", modelIDs(models.GetModels()), []string{"c1"})
		check("chat", modelIDs(models.GetModelsOfType(ModelTypeChat)), []string{"c1"})
		check("image", modelIDs(models.GetModelsOfType(ModelTypeImage)), []string{"i1", "i2", "i3"})
		check("image p1", modelIDs(models.GetModelsOfType(ModelTypeImage, "p1")), []string{"i1", "i2"})
		check("classifier", modelIDs(models.GetModelsOfType(ModelTypeClassifier)), []string{})
		check("all", modelIDs(models.GetAllModels()), []string{"c1", "i1", "i2", "i3"})
		if got := models.GetModel("p1", "c1"); got == nil || got.ID != "c1" {
			t.Fatalf("GetModel=%v", got)
		}
		if models.GetModel("p1", "i1") != nil {
			t.Fatal("GetModel returned an image model")
		}
		if got := models.GetModelOfType(ModelTypeChat, "p1", "c1"); got == nil || got.ModelID() != "c1" {
			t.Fatalf("chat of type=%v", got)
		}
		if got := models.GetModelOfType(ModelTypeImage, "p1", "i1"); got == nil || got.ModelID() != "i1" {
			t.Fatalf("image of type=%v", got)
		}
		if models.GetModelOfType(ModelTypeImage, "p1", "c1") != nil {
			t.Fatal("GetModelOfType matched a chat id with the image type")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:189
	t.Run("splits available models by type", func(t *testing.T) {
		models := CreateModels(CreateModelsOptions{AuthContext: typedAuthContext(map[string]string{"KEY": "k"})})
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p1", envVar: "KEY", models: []AnyModel{typedChatModel("p1", "c1"), typedImageModel("p1", "i1")}}))
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p2", envVar: "MISSING", models: []AnyModel{typedImageModel("p2", "i2")}}))
		chat, err := models.GetAvailable(t.Context())
		if err != nil || !reflect.DeepEqual(modelIDs(chat), []string{"c1"}) {
			t.Fatalf("GetAvailable=%v err=%v", modelIDs(chat), err)
		}
		chatOfType, err := models.GetAvailableOfType(t.Context(), ModelTypeChat)
		if err != nil || !reflect.DeepEqual(modelIDs(chatOfType), []string{"c1"}) {
			t.Fatalf("chat=%v err=%v", modelIDs(chatOfType), err)
		}
		images, err := models.GetAvailableOfType(t.Context(), ModelTypeImage)
		if err != nil || !reflect.DeepEqual(modelIDs(images), []string{"i1"}) {
			t.Fatalf("image=%v err=%v", modelIDs(images), err)
		}
		all, err := models.GetAllAvailable(t.Context())
		if err != nil || !reflect.DeepEqual(modelIDs(all), []string{"c1", "i1"}) {
			t.Fatalf("all=%v err=%v", modelIDs(all), err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:202
	t.Run("resolves auth through the provider and merges it into image requests; explicit options win", func(t *testing.T) {
		var calls []typedGenerateCall
		models := CreateModels(CreateModelsOptions{AuthContext: typedAuthContext(map[string]string{"TEST_KEY": "env-key"})})
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p1", envVar: "TEST_KEY", calls: &calls}))
		model := imageModelOf(t, models, "p1", "model-a")
		if auth, err := models.GetModelAuth(t.Context(), model); err != nil || auth == nil || auth.Auth.APIKey != "env-key" {
			t.Fatalf("model auth=%v err=%v", auth, err)
		}
		if auth, err := models.GetAuth(t.Context(), model.ProviderID()); err != nil || auth == nil || auth.Auth.APIKey != "env-key" {
			t.Fatalf("provider auth=%v err=%v", auth, err)
		}
		if auth, err := models.GetModelAuth(t.Context(), model, AuthResolutionOverrides{APIKey: new("explicit-key")}); err != nil || auth == nil || auth.Auth.APIKey != "explicit-key" {
			t.Fatalf("explicit auth=%v err=%v", auth, err)
		}
		result := models.GenerateImages(t.Context(), model, request)
		if result.StopReason != ImagesStopReasonStop || len(calls) != 1 || calls[0].options.APIKey != "env-key" {
			t.Fatalf("result=%+v calls=%+v", result, calls)
		}
		models.GenerateImages(t.Context(), model, request, ModelsImagesOptions{ImagesOptions: ImagesOptions{APIKey: "explicit"}})
		if len(calls) != 2 || calls[1].options.APIKey != "explicit" {
			t.Fatalf("calls=%+v", calls)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:220
	t.Run("merges provider-resolved env and applies header transforms", func(t *testing.T) {
		var calls []typedGenerateCall
		models := CreateModels()
		// Pi's provider here has only an images implementation and no chat api.
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "p1",
			Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test key", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
				return &AuthResult{Auth: ModelAuth{APIKey: "provider-key", Headers: ProviderHeaders{"x-base": new("1")}}, Env: map[string]string{"PROVIDER_ONLY": "provider", "SHARED": "provider"}}, nil
			}}},
			Models: []AnyModel{typedImageModel("p1", "model-a")},
			Images: ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ImageModel, _ ImagesContext, options ImagesOptions) (AssistantImages, error) {
				calls = append(calls, typedGenerateCall{model, options})
				return typedOKResult(model), nil
			}}}}))
		model := imageModelOf(t, models, "p1", "model-a")
		models.GenerateImages(t.Context(), model, request, ModelsImagesOptions{
			ImagesOptions: ImagesOptions{APIKey: "request-key", Env: map[string]string{"REQUEST_ONLY": "request", "SHARED": "request"}},
			// Pi's transform returns a new object ({ ...headers, "x-extra": "2" }) and leaves its input unchanged.
			TransformHeaders: func(_ context.Context, headers ProviderHeaders) (ProviderHeaders, error) {
				out := maps.Clone(headers)
				if out == nil {
					out = ProviderHeaders{}
				}
				out["x-extra"] = new("2")
				return out, nil
			},
		})
		if len(calls) != 1 || calls[0].options.APIKey != "request-key" {
			t.Fatalf("calls=%+v", calls)
		}
		if want := map[string]string{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}; !reflect.DeepEqual(calls[0].options.Env, want) {
			t.Fatalf("env=%v", calls[0].options.Env)
		}
		headers := map[string]string{}
		for name, value := range calls[0].options.Headers {
			if value != nil {
				headers[name] = *value
			}
		}
		if want := map[string]string{"x-base": "1", "x-extra": "2"}; !reflect.DeepEqual(headers, want) {
			t.Fatalf("headers=%v", headers)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:263
	t.Run("returns error results instead of rejecting", func(t *testing.T) {
		models := CreateModels(CreateModelsOptions{AuthContext: typedAuthContext(nil)})
		ghost := models.GenerateImages(t.Context(), typedImageModel("ghost", "m"), request)
		if ghost.StopReason != ImagesStopReasonError || !strings.Contains(ghost.ErrorMessage, "Unknown provider: ghost") {
			t.Fatalf("ghost=%+v", ghost)
		}
		// Unconfigured auth is an error, matching Stream.
		var calls []typedGenerateCall
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p1", envVar: "MISSING", calls: &calls}))
		model := imageModelOf(t, models, "p1", "model-a")
		if auth, err := models.GetModelAuth(t.Context(), model); err != nil || auth != nil {
			t.Fatalf("unconfigured auth=%+v err=%v", auth, err)
		}
		unconfigured := models.GenerateImages(t.Context(), model, request)
		if unconfigured.StopReason != ImagesStopReasonError || !strings.Contains(unconfigured.ErrorMessage, "not configured") || len(calls) != 0 {
			t.Fatalf("unconfigured=%+v calls=%d", unconfigured, len(calls))
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		aborted := models.GenerateImages(cancelled, model, request)
		if aborted.StopReason != ImagesStopReasonAborted || len(calls) != 0 {
			t.Fatalf("aborted=%+v calls=%d", aborted, len(calls))
		}
		// A provider without any images implementation rejects image models it lists.
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "chat-only", Auth: typedNoAuth(), Models: []AnyModel{typedImageModel("chat-only", "i")}, API: typedChatStreams()}))
		unsupported := models.GenerateImages(t.Context(), imageModelOf(t, models, "chat-only", "i"), request)
		if unsupported.StopReason != ImagesStopReasonError || !strings.Contains(unsupported.ErrorMessage, "does not support image generation") {
			t.Fatalf("unsupported=%+v", unsupported)
		}
		// An images map without the model's api yields a provider error result.
		models.SetProvider(typedTestProvider(typedProviderInput{id: "wrong-api", models: []AnyModel{typedImageModel("wrong-api", "i")}, images: []ImageAPI{"other-images"}}))
		missing := models.GenerateImages(t.Context(), imageModelOf(t, models, "wrong-api", "i"), request)
		if missing.StopReason != ImagesStopReasonError || !strings.Contains(missing.ErrorMessage, `no image generation implementation for "test-images"`) {
			t.Fatalf("missing=%+v", missing)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:321 (the unrepresentable image model is a chat Model whose Type names another operation)
	t.Run("rejects models of another type at the stream entry points at runtime", func(t *testing.T) {
		models := CreateModels()
		// The stream would settle if the entry point did not reject the model, so a missing check fails instead of hanging.
		settles := func(_ context.Context, model *Model, _ TranscriptContext, _ StreamOptions) (*AssistantMessageEventStream, error) {
			return modelsRuntimeDone(model, "ok")
		}
		models.SetProvider(typedTestProvider(typedProviderInput{id: "p1", chat: &ProviderStreams{Stream: settles, StreamSimple: settles}}))
		notChat := typedChatModel("p1", "model-a")
		notChat.Type = ModelTypeImage
		result := models.CompleteSimple(t.Context(), notChat, Context{})
		if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "is not a chat model") {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:331
	t.Run("requires at least one concrete operation implementation", func(t *testing.T) {
		const message = `at least one of "api", "images", or "classifiers"`
		create := func(options CreateProviderOptions) (recovered any) {
			defer func() { recovered = recover() }()
			options.ID, options.Auth, options.Models = "empty", typedNoAuth(), []AnyModel{}
			CreateProvider(options)
			return nil
		}
		for name, options := range map[string]CreateProviderOptions{
			"none":        {},
			"empty api":   {API: ProviderAPIMap{}},
			"empty image": {Images: ProviderImageAPIMap{}},
			"empty class": {Classifiers: ProviderClassifierMap{}},
		} {
			if got := create(options); got == nil || !strings.Contains(fmt.Sprint(got), message) {
				t.Fatalf("%s: panic=%v", name, got)
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:347
	t.Run("supports dynamic providers listing image models via refresh", func(t *testing.T) {
		fetches := 0
		store := NewInMemoryModelsStore()
		models := CreateModels(CreateModelsOptions{ModelsStore: store})
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "dyn", Auth: typedNoAuth(), Models: []AnyModel{},
			FetchModels: func(RefreshModelsContext) ([]AnyModel, error) {
				fetches++
				return []AnyModel{typedImageModel("dyn", "listed"), typedChatModel("dyn", "chat")}, nil
			},
			Images: ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ImageModel, _ ImagesContext, _ ImagesOptions) (AssistantImages, error) {
				return typedOKResult(model), nil
			}}}}))
		if got := models.GetAllModels("dyn"); len(got) != 0 {
			t.Fatalf("before refresh=%v", modelIDs(got))
		}
		result := models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dyn"}})
		if len(result.Errors) != 0 || fetches != 1 {
			t.Fatalf("errors=%v fetches=%d", result.Errors, fetches)
		}
		if models.GetModelOfType(ModelTypeImage, "dyn", "listed") == nil || models.GetModel("dyn", "chat") == nil {
			t.Fatal("refreshed models are missing")
		}
		stored, err := store.Read(t.Context(), "dyn")
		if err != nil || stored == nil {
			t.Fatalf("stored=%v err=%v", stored, err)
		}
		if got := storedModelIDs(t, stored.Models); !reflect.DeepEqual(got, []string{"listed", "chat"}) {
			t.Fatalf("stored ids=%v", got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:373
	t.Run("keeps existing built-in and compat model reads chat-only", func(t *testing.T) {
		chat := GetBuiltinModels("openrouter")
		images := GetImageModels("openrouter")
		all := GetAllBuiltinModels("openrouter")
		compat := GetBuiltinModels("openrouter")

		for _, model := range chat {
			if !IsModelType(model, ModelTypeChat) || model.Capabilities.ContextWindow <= 0 {
				t.Fatalf("chat model %s: type=%s contextWindow=%d", model.ID, GetModelType(model), model.Capabilities.ContextWindow)
			}
		}
		for i := range images {
			if !IsModelType(&images[i], ModelTypeImage) {
				t.Fatalf("image model %s: type=%s", images[i].ID, GetModelType(&images[i]))
			}
		}
		if !slices.ContainsFunc(all, func(model AnyModel) bool { return IsModelType(model, ModelTypeImage) }) {
			t.Fatal("GetAllBuiltinModels(openrouter) has no image model")
		}
		if !reflect.DeepEqual(compat, chat) {
			t.Fatalf("compat reads differ from chat reads")
		}
		if got, want := len(chat)+len(images)+len(GetBuiltinClassifierModels("openrouter")), len(all); got != want {
			t.Fatalf("chat+image+classifier = %d, all = %d", got, want)
		}
		flux, ok := GetImageModel("openrouter", "black-forest-labs/flux.2-pro")
		if !ok || GetModelType(&flux) != ModelTypeImage {
			t.Fatalf("flux.2-pro = %+v ok=%v", flux, ok)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/images-models.test.ts:390
	t.Run("builtinModels exposes OpenRouter image models under the openrouter provider", func(t *testing.T) {
		models := BuiltinModels(CreateModelsOptions{AuthContext: typedAuthContext(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
		provider := models.GetProvider("openrouter")
		if provider == nil {
			t.Fatal("missing openrouter provider")
		}
		images := models.GetModelsOfType(ModelTypeImage, "openrouter")
		if len(images) == 0 {
			t.Fatal("no openrouter image models")
		}
		chatModels, err := provider.GetModels()
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range chatModels {
			if !IsModelType(model, ModelTypeChat) {
				t.Fatalf("GetModels returned %s %s", GetModelType(model), model.ID)
			}
		}
		allModels, err := provider.GetAllModels()
		if err != nil || !slices.ContainsFunc(allModels, func(model AnyModel) bool { return IsModelType(model, ModelTypeImage) }) {
			t.Fatalf("GetAllModels = %v, %v", modelIDs(allModels), err)
		}
		for _, model := range images {
			image, ok := model.(*ImageModel)
			if !ok || GetModelType(image) != ModelTypeImage || image.API != APIImagesOpenRouter {
				t.Fatalf("image model %#v", model)
			}
		}
		for _, model := range models.GetModelsOfType(ModelTypeImage) {
			if model.ProviderID() != "openrouter" {
				t.Fatalf("image model of provider %s", model.ProviderID())
			}
		}
		// One upstream id can expose separate chat and image operations.
		chat := models.GetModel("openrouter", "google/gemini-3-pro-image")
		image, _ := models.GetModelOfType(ModelTypeImage, "openrouter", "google/gemini-3-pro-image").(*ImageModel)
		if chat == nil || image == nil || chat.ProviderMeta.API != APIOpenAICompletions || image.API != APIImagesOpenRouter {
			t.Fatalf("chat=%+v image=%+v", chat, image)
		}
		// One credential covers both.
		for _, model := range []AnyModel{images[0], chat} {
			if auth, err := models.GetModelAuth(t.Context(), model); err != nil || auth == nil || auth.Auth.APIKey != "or-key" {
				t.Fatalf("auth for %s = %v, %v", model.ModelID(), auth, err)
			}
		}
		if provider.GenerateImages == nil {
			t.Fatal("openrouter provider has no GenerateImages")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/model-types.test.ts:92: upstream guards the compile-time return type; in Go the
	// runtime half is that successive catalog reads within one api return models with their own ids.
	t.Run("built-in catalog getters return model shapes that can be reassigned within one api", func(t *testing.T) {
		read := func(id string) *Model {
			t.Helper()
			generated, ok := LookupModelExact("openai/" + id)
			if !ok {
				t.Fatalf("missing built-in model openai/%s", id)
			}
			return generated.ToModel()
		}
		for _, name := range []string{"built-in", "compat"} {
			model := read("gpt-4o-mini")
			if model.ID != "gpt-4o-mini" {
				t.Fatalf("%s first read = %s", name, model.ID)
			}
			model = read("gpt-4o")
			if model.ID != "gpt-4o" {
				t.Fatalf("%s reassigned read = %s", name, model.ID)
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/model-types.test.ts:105
	t.Run("stored and fetched models of unknown types are dropped instead of failing the refresh", func(t *testing.T) {
		store := NewInMemoryModelsStore()
		record := func(id, api, kind string, extra string) json.RawMessage {
			typeField := ""
			if kind != "" {
				typeField = fmt.Sprintf(`"type":%q,`, kind)
			}
			return json.RawMessage(fmt.Sprintf(`{%s"id":%q,"name":%q,"api":%q,"provider":"dyn","baseUrl":"https://example.test/v1",%s,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}`, typeField, id, id, api, extra))
		}
		chatExtra := `"reasoning":false,"contextWindow":1000,"maxTokens":100`
		imageExtra := `"output":["image"]`
		stored := ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{
			record("stored-chat", "test-chat", "", chatExtra),
			record("stored-image", "test-images", "image", imageExtra),
			record("future-embedding", "test-chat", "embedding", chatExtra),
			record("future-video", "test-images", "video", imageExtra),
		})}
		if err := store.Write(t.Context(), "dyn", stored); err != nil {
			t.Fatal(err)
		}
		var fetched []AnyModel
		models := CreateModels(CreateModelsOptions{ModelsStore: store})
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "dyn", Auth: typedNoAuth(), Models: []AnyModel{},
			FetchModels: func(RefreshModelsContext) ([]AnyModel, error) { return fetched, nil }, API: typedChatStreams()}))
		restored := models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dyn"}, AllowNetwork: new(false)})
		if len(restored.Errors) != 0 {
			t.Fatalf("errors=%v", restored.Errors)
		}
		if got := modelIDs(models.GetAllModels("dyn")); !reflect.DeepEqual(got, []string{"stored-chat", "stored-image"}) {
			t.Fatalf("restored=%v", got)
		}
		// Pi fetches { ...imageModel("dyn", "fetched-video"), type: "video" }. ImageModel has no Type field, so the only Go
		// value with an unknown type is a *Model whose Type names it.
		unknown := typedChatModel("dyn", "fetched-video")
		unknown.Type = "video"
		fetched = []AnyModel{typedChatModel("dyn", "fetched-chat"), unknown}
		refreshed := models.Refresh(t.Context(), ModelsRefreshOptions{Providers: []string{"dyn"}})
		if len(refreshed.Errors) != 0 {
			t.Fatalf("errors=%v", refreshed.Errors)
		}
		if got := modelIDs(models.GetAllModels("dyn")); !reflect.DeepEqual(got, []string{"fetched-chat"}) {
			t.Fatalf("refreshed=%v", got)
		}
		entry, err := store.Read(t.Context(), "dyn")
		if err != nil || entry == nil || !slices.Equal(storedModelIDs(t, entry.Models), []string{"fetched-chat"}) {
			t.Fatalf("stored=%+v err=%v", entry, err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/models-runtime.test.ts:201
	t.Run("keeps chat reads independent from the all-model catalog", func(t *testing.T) {
		provider := modelsRuntimeProvider(modelsRuntimeProviderInput{id: "chat-only"})
		provider.GetAllModels = func() ([]AnyModel, error) { return nil, errors.New("all models unavailable") }
		models := CreateModels()
		models.SetProvider(provider)
		if got := modelIDs(models.GetModels("chat-only")); !reflect.DeepEqual(got, []string{"model-a"}) {
			t.Fatalf("GetModels=%v", got)
		}
		if got := models.GetModel("chat-only", "model-a"); got == nil || got.ID != "model-a" {
			t.Fatalf("GetModel=%v", got)
		}
		available, err := models.GetAvailable(t.Context(), "chat-only")
		if err != nil || !reflect.DeepEqual(modelIDs(available), []string{"model-a"}) {
			t.Fatalf("GetAvailable=%v err=%v", modelIDs(available), err)
		}
		if got := models.GetAllModels("chat-only"); len(got) != 0 {
			t.Fatalf("GetAllModels=%v", modelIDs(got))
		}
	})
}

func typedNoAuth() ProviderAuth {
	return ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
}

func storedModelIDs(t *testing.T, models []AnyModel) []string {
	t.Helper()
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ModelID())
	}
	return ids
}

func imageModelOf(t testing.TB, models *Models, provider, id string) *ImageModel {
	t.Helper()
	model, ok := models.GetModelOfType(ModelTypeImage, provider, id).(*ImageModel)
	if !ok || model == nil {
		t.Fatalf("image model %s/%s is missing", provider, id)
	}
	return model
}
