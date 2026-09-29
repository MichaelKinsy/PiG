package ai

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func modelsRuntimeModel(provider, id string) *Model {
	return &Model{ID: id, DisplayName: id, ProviderMeta: ProviderMetadata{ProviderID: provider, API: "test-api", BaseURL: "https://example.test/v1"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
}

func modelsRuntimeContext() Context {
	return Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: time.Now().UnixMilli()}}}
}

type modelsRuntimeCall struct {
	model   *Model
	options StreamOptions
}
type modelsRuntimeProviderInput struct {
	id            string
	models        []*Model
	auth          *ProviderAuth
	getModels     func() ([]*Model, error)
	refreshModels func(RefreshModelsContext) error
	calls         *[]modelsRuntimeCall
}

func modelsRuntimeProvider(input modelsRuntimeProviderInput) *ModelsProvider {
	models := input.models
	if models == nil {
		models = []*Model{modelsRuntimeModel(input.id, "model-a")}
	}
	auth := ProviderAuth{APIKey: &APIKeyAuth{Name: "Ambient", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
	if input.auth != nil {
		auth = *input.auth
	}
	getModels := input.getModels
	if getModels == nil {
		getModels = func() ([]*Model, error) { return models, nil }
	}
	respond := func(_ context.Context, model *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		if input.calls != nil {
			*input.calls = append(*input.calls, modelsRuntimeCall{model, options})
		}
		return modelsRuntimeDone(model, "ok")
	}
	return &ModelsProvider{ID: input.id, Name: input.id, Auth: auth, GetModels: getModels, RefreshModels: input.refreshModels, Stream: respond, StreamSimple: respond}
}

func modelsRuntimeDone(model *Model, text string) (*AssistantMessageEventStream, error) {
	stream := NewAssistantMessageEventStream()
	message := &AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: text}}, API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID, StopReason: StopReasonStop, Timestamp: time.Now().UnixMilli()}
	if err := stream.Push(StartEvent{Partial: message}); err != nil {
		return nil, err
	}
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: message}); err != nil {
		return nil, err
	}
	return stream, nil
}

func modelsRuntimeEnvKey(key string) *APIKeyAuth {
	return &APIKeyAuth{Name: "Test API key", Resolve: func(_ context.Context, input APIKeyAuthInput) (*AuthResult, error) {
		resolved, source := key, "env"
		if input.Credential != nil {
			resolved = input.Credential.Key
			source = "stored"
		}
		if resolved == "" {
			return nil, nil
		}
		return &AuthResult{Auth: ModelAuth{APIKey: resolved}, Source: source}, nil
	}}
}

func modelsRuntimeOAuth() *OAuthAuth {
	return &OAuthAuth{Name: "Test OAuth", Login: func(context.Context, AuthInteraction) (Credential, error) {
		return Credential{}, errors.New("not used")
	}, Refresh: func(_ context.Context, c Credential) (Credential, error) { return c, nil }, ToAuth: func(c Credential) (ModelAuth, error) { return ModelAuth{APIKey: c.Access}, nil }}
}

func modelsRuntimePut(t testing.TB, store CredentialStore, id string, credential Credential) {
	t.Helper()
	if _, err := store.Modify(t.Context(), id, func(*Credential) (*Credential, error) { return &credential, nil }); err != nil {
		t.Fatal(err)
	}
}

func requireModelsError(t *testing.T, err error, code ModelsErrorCode) {
	t.Helper()
	if modelError, ok := errors.AsType[*ModelsError](err); !ok || modelError.Code != code {
		t.Fatalf("error=%v, want code %s", err, code)
	}
}

func TestModelsRuntimeBasicUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:126
	t.Run("applies request-wide pricing tiers above the configured input threshold", func(t *testing.T) {
		model := modelsRuntimeModel("openai", "gpt-5.6-sol")
		model.Capabilities.InputCostPer1M = 5
		model.Capabilities.OutputCostPer1M = 30
		model.Capabilities.CacheReadCostPer1M = .5
		model.Capabilities.CacheWriteCostPer1M = 6.25
		model.Capabilities.CostTiers = []CostTier{{InputTokensAbove: 272000, InputCostPer1M: 10, OutputCostPer1M: 45, CacheReadCostPer1M: 1, CacheWriteCostPer1M: 12.5}}
		usage := Usage{Input: 200000, Output: 100000, CacheRead: 72000, TotalTokens: 372000}
		short := CalculateCost(model, &usage)
		if short.Input != 1 || short.Output != 3 || short.CacheRead != .036 || short.CacheWrite != 0 {
			t.Fatalf("short=%+v", short)
		}
		usage = Usage{Input: 200000, Output: 100000, CacheRead: 72000, CacheWrite: 1, TotalTokens: 372001}
		long := CalculateCost(model, &usage)
		if long.Input != 2 || long.Output != 4.5 || long.CacheRead != .072 || long.CacheWrite != .0000125 {
			t.Fatalf("long=%+v", long)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:162
	t.Run("registers, replaces, and deletes providers", func(t *testing.T) {
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1"}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p2"}))
		list := models.GetProviders()
		if len(list) != 2 || list[0].ID != "p1" || list[1].ID != "p2" {
			t.Fatalf("providers=%v", list)
		}
		replacement := modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1"})
		models.SetProvider(replacement)
		if models.GetProvider("p1") != replacement || len(models.GetProviders()) != 2 {
			t.Fatal("replacement changed identity or size")
		}
		models.DeleteProvider("p1")
		if models.GetProvider("p1") != nil {
			t.Fatal("provider not deleted")
		}
		models.ClearProviders()
		if len(models.GetProviders()) != 0 {
			t.Fatal("providers not cleared")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:180
	t.Run("lists and finds models per provider", func(t *testing.T) {
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", models: []*Model{modelsRuntimeModel("p1", "m1"), modelsRuntimeModel("p1", "m2")}}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p2", models: []*Model{modelsRuntimeModel("p2", "m3")}}))
		ids := func(list []*Model) []string {
			out := []string{}
			for _, model := range list {
				out = append(out, model.ID)
			}
			return out
		}
		if !reflect.DeepEqual(ids(models.GetModels()), []string{"m1", "m2", "m3"}) || !reflect.DeepEqual(ids(models.GetModels("p1")), []string{"m1", "m2"}) || len(models.GetModels("nope")) != 0 {
			t.Fatal("wrong catalog lists")
		}
		found := models.GetModel("p2", "m3")
		if found == nil || found.ID != "m3" || models.GetModel("p2", "missing") != nil || HasApi(found, "openai-completions") || !HasApi(found, "test-api") {
			t.Fatalf("lookup=%v", found)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:201
	t.Run("swallows provider source failures for both all-provider and single-provider listing", func(t *testing.T) {
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "broken", getModels: func() ([]*Model, error) { return nil, errors.New("boom") }}))
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "ok", models: []*Model{modelsRuntimeModel("ok", "m1")}}))
		if list := models.GetModels(); len(list) != 1 || list[0].ID != "m1" || len(models.GetModels("broken")) != 0 {
			t.Fatalf("models=%v", list)
		}
		if _, err := models.GetProvider("broken").GetModels(); err == nil || err.Error() != "boom" {
			t.Fatalf("source error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1052
	t.Run("uses explicit request api key and env during provider auth resolution", func(t *testing.T) {
		var calls []modelsRuntimeCall
		apiKey := &APIKeyAuth{Name: "Scoped", Resolve: func(_ context.Context, input APIKeyAuthInput) (*AuthResult, error) {
			account, _ := input.Ctx.Env("ACCOUNT_ID")
			if input.Credential != nil && input.Credential.Env["ACCOUNT_ID"] != "" {
				account = input.Credential.Env["ACCOUNT_ID"]
			}
			if input.Credential == nil || input.Credential.Key == "" || account == "" {
				return nil, nil
			}
			return &AuthResult{Auth: ModelAuth{APIKey: input.Credential.Key, BaseURL: "https://example.test/" + account}, Env: map[string]string{"ACCOUNT_ID": account}}, nil
		}}
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: apiKey}, calls: &calls}))
		models.CompleteSimple(t.Context(), modelsRuntimeModel("p1", "model-a"), modelsRuntimeContext(), StreamOptions{APIKey: "explicit-key", Env: ProviderEnv{"ACCOUNT_ID": "acct"}})
		if len(calls) != 1 || calls[0].model.ProviderMeta.BaseURL != "https://example.test/acct" || calls[0].options.APIKey != "explicit-key" || !reflect.DeepEqual(calls[0].options.Env, ProviderEnv{"ACCOUNT_ID": "acct"}) {
			t.Fatalf("calls=%+v", calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1076
	t.Run("merges resolved auth into stream options; explicit options win per field", func(t *testing.T) {
		var calls []modelsRuntimeCall
		apiKey := &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
			return &AuthResult{Auth: ModelAuth{APIKey: "resolved-key", Headers: ProviderHeaders{"Authorization": new("Bearer resolved-key"), "x-a": new("auth"), "x-b": new("auth")}, BaseURL: "https://auth.test/v1"}}, nil
		}}
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: apiKey}, calls: &calls}))
		model := modelsRuntimeModel("p1", "model-a")
		result := models.CompleteSimple(t.Context(), model, modelsRuntimeContext(), StreamOptions{APIKey: "explicit-key", Headers: ProviderHeaders{"authorization": new("Explicit token"), "x-b": new("explicit")}})
		want := ProviderHeaders{"authorization": new("Explicit token"), "x-a": new("auth"), "x-b": new("explicit")}
		if result.StopReason != StopReasonStop || len(calls) != 1 || calls[0].options.APIKey != "explicit-key" || !reflect.DeepEqual(calls[0].options.Headers, want) || calls[0].model.ProviderMeta.BaseURL != "https://auth.test/v1" {
			t.Fatalf("result=%+v calls=%+v", result, calls)
		}
		result = models.CompleteSimple(t.Context(), model, modelsRuntimeContext())
		if result.StopReason != StopReasonStop || len(calls) != 2 || calls[1].options.APIKey != "resolved-key" {
			t.Fatalf("result=%+v calls=%+v", result, calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1108
	t.Run("adds model headers only for model auth and transforms assembled headers once", func(t *testing.T) {
		var calls []modelsRuntimeCall
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("key")}, calls: &calls}))
		model := modelsRuntimeModel("p1", "model-a")
		model.ProviderMeta.Headers = map[string]string{"x-model": "model", "x-shared": "model"}
		providerAuth, err := models.GetAuth(t.Context(), "p1")
		if err != nil || providerAuth == nil || providerAuth.Auth.Headers != nil {
			t.Fatalf("auth=%v err=%v", providerAuth, err)
		}
		modelAuth, err := models.GetModelAuth(t.Context(), model)
		if err != nil || modelAuth == nil || !reflect.DeepEqual(modelAuth.Auth.Headers, ProviderHeaders{"x-model": new("model"), "x-shared": new("model")}) {
			t.Fatalf("model auth=%v err=%v", modelAuth, err)
		}
		transforms := 0
		models.CompleteSimple(t.Context(), model, modelsRuntimeContext(), StreamOptions{Headers: ProviderHeaders{"x-explicit": new("explicit"), "X-Shared": new("explicit")}, TransformHeaders: func(_ context.Context, headers ProviderHeaders) (ProviderHeaders, error) {
			transforms++
			want := ProviderHeaders{"x-model": new("model"), "x-explicit": new("explicit"), "X-Shared": new("explicit")}
			if !reflect.DeepEqual(headers, want) {
				t.Errorf("transform input=%v", headers)
			}
			headers["x-transformed"] = new("yes")
			return headers, nil
		}})
		want := ProviderHeaders{"x-model": new("model"), "x-explicit": new("explicit"), "X-Shared": new("explicit"), "x-transformed": new("yes")}
		if transforms != 1 || len(calls) != 1 || !reflect.DeepEqual(calls[0].options.Headers, want) || calls[0].options.TransformHeaders != nil {
			t.Fatalf("transforms=%d calls=%+v", transforms, calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1138
	t.Run("produces an error stream for unknown providers instead of throwing", func(t *testing.T) {
		result := CreateModels().CompleteSimple(t.Context(), modelsRuntimeModel("ghost", "model-a"), modelsRuntimeContext())
		if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "Unknown provider: ghost") {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/models-runtime.test.ts:1145
	t.Run("streams through the provider", func(t *testing.T) {
		models := CreateModels()
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1"}))
		stream := models.StreamSimple(t.Context(), modelsRuntimeModel("p1", "model-a"), modelsRuntimeContext())
		events := []string{}
		for event := range stream.Events(t.Context()) {
			events = append(events, string(event.EventType()))
		}
		if !reflect.DeepEqual(events, []string{"start", "done"}) || stream.Result().StopReason != StopReasonStop {
			t.Fatalf("events=%v result=%+v", events, stream.Result())
		}
	})
}
