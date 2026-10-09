package ai

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func classifierTestModel(provider, id string) *ClassifierModel {
	return &ClassifierModel{ID: id, Name: id, API: "test-classifier", Provider: provider, BaseURL: "https://example.test/v1", Input: []string{"text"}, ContextWindow: 1000}
}

func classifierTestContext() ClassifierContext {
	return ClassifierContext{State: JsonObject{"text": "yes"}, Questions: ClassifierQuestions{{ID: "approved", Question: ClassifierBoolQuestion{Instructions: "Does this express approval?", Criteria: ClassifierBoolCriteria{True: "Approval", False: "No approval"}}}}}
}

// Ports packages/ai/test/classifier-models.test.ts.
func TestClassifierModelsUpstream(t *testing.T) {
	auth := ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
	// .upstream/v0.99.1/packages/ai/test/classifier-models.test.ts:53
	t.Run("keeps chat and classifier entries with the same provider and id separate", func(t *testing.T) {
		chat := typedChatModel("test", "shared")
		classifier := classifierTestModel("test", "shared")
		provider := CreateProvider(CreateProviderOptions{ID: "test", Auth: auth, Models: []AnyModel{chat, classifier}, API: ProviderAPIMap{"test-chat": typedChatStreams()},
			Classifiers: ProviderClassifierMap{"test-classifier": {Classify: func(_ context.Context, model *ClassifierModel, _ ClassifierContext, _ ClassifierOptions) (ClassifierResult, error) {
				return ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, Answers: ClassifierAnswers{{ID: "approved", Answer: ClassifierBoolAnswer{Probability: 0.9}}}, StopReason: "stop", Timestamp: time.Now().UnixMilli()}, nil
			}}}})
		models := CreateModels()
		models.SetProvider(provider)

		listedChat := models.GetModel("test", "shared")
		if listedChat == nil || GetModelType(listedChat) != ModelTypeChat {
			t.Fatalf("chat=%v", listedChat)
		}
		if got := models.GetModelOfType(ModelTypeClassifier, "test", "shared"); got == nil || got.ModelType() != ModelTypeClassifier {
			t.Fatalf("classifier=%v", got)
		}
		if got := models.GetModelsOfType(ModelTypeClassifier); !reflect.DeepEqual(got, []AnyModel{classifier}) {
			t.Fatalf("classifiers=%v", got)
		}
		if got := models.GetAllModels(); len(got) != 2 {
			t.Fatalf("all=%v", got)
		}
		available, err := models.GetAvailableOfType(t.Context(), ModelTypeClassifier)
		if err != nil || !reflect.DeepEqual(available, []AnyModel{classifier}) {
			t.Fatalf("available=%v err=%v", available, err)
		}
		result := models.Classify(t.Context(), classifier, classifierTestContext())
		if got := clsAnswer(t, result.Answers, "approved"); got != (ClassifierBoolAnswer{Probability: 0.9}) {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/classifier-models.test.ts:94 casts a chat model to ClassifierModel to reach the
	// runtime check. Models.Classify takes *ClassifierModel, so a chat model cannot reach it: the case is unrepresentable
	// in Go and the compile-time type is the check. The provider without classification support is the reachable runtime
	// rejection.
	t.Run("rejects a classifier model whose provider has no classifier implementation", func(t *testing.T) {
		classifier := classifierTestModel("test", "cls")
		models := CreateModels()
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "test", Auth: auth, Models: []AnyModel{typedChatModel("test", "chat"), classifier}, API: ProviderAPIMap{"test-chat": typedChatStreams()}}))

		result := models.Classify(t.Context(), classifier, classifierTestContext())

		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "does not support classification") {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/classifier-models.test.ts:114
	t.Run("exposes Jev only through classifier catalog accessors", func(t *testing.T) {
		jev := GetBuiltinClassifierModel("typesafe", "jev-latest")
		if jev == nil || jev.ModelType() != ModelTypeClassifier || jev.API != "typesafe-system-one" || jev.Provider != "typesafe" || jev.ContextWindow != 64000 {
			t.Fatalf("jev=%+v", jev)
		}
		if got := GetBuiltinClassifierModels("typesafe"); !reflect.DeepEqual(got, []*ClassifierModel{jev}) {
			t.Fatalf("classifiers=%v", got)
		}
		if got := GetAllBuiltinModels("typesafe"); !reflect.DeepEqual(got, []AnyModel{jev}) {
			t.Fatalf("all=%v", got)
		}
		models := BuiltinModels()
		if models.GetModel("typesafe", "jev-latest") != nil {
			t.Error("chat lookup returned Jev")
		}
		if got := models.GetModelOfType(ModelTypeClassifier, "typesafe", "jev-latest"); !reflect.DeepEqual(got, AnyModel(jev)) {
			t.Errorf("of type=%v", got)
		}
	})
	for _, tc := range []struct{ provider, id, url string }{
		{"vercel-ai-gateway", "typesafe-ai/jev", "https://ai-gateway.vercel.sh/typesafe/v1/systemone"},
		{"opencode", "jev-1.13", "https://opencode.ai/zen/v1/systemone"},
		{"opencode", "jev-1.13-free", "https://opencode.ai/zen/v1/systemone"},
	} {
		// .upstream/v0.99.1/packages/ai/test/classifier-models.test.ts:130
		t.Run("routes "+tc.provider+" Jev ("+tc.id+") to its TypeSafe-compatible endpoint", func(t *testing.T) {
			models := BuiltinModels()
			jev, _ := models.GetModelOfType(ModelTypeClassifier, tc.provider, tc.id).(*ClassifierModel)
			if jev == nil {
				t.Fatalf("missing %s Jev model", tc.provider)
			}
			if jev.API != "typesafe-system-one" || jev.ContextWindow != 32000 || models.GetModel(tc.provider, tc.id) != nil {
				t.Fatalf("jev=%+v", jev)
			}
			type recorded struct {
				url, model, authorization string
				state                     any
			}
			var requests []recorded
			result := models.Classify(t.Context(), jev, classifierTestContext(), ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{APIKey: "secret", APIKeySet: true, Fetch: clsClient(func(r *http.Request) (*http.Response, error) {
				body := clsBody(t, r)
				requests = append(requests, recorded{r.URL.String(), body["model"].(string), r.Header.Get("authorization"), body["state"]})
				return clsJSON(200, `{"model":"`+tc.id+`","answers":{"approved":{"type":"noul","noul":0.8}}}`), nil
			})}})

			if !reflect.DeepEqual(requests, []recorded{{tc.url, tc.id, "Bearer secret", map[string]any{"text": "yes"}}}) {
				t.Fatalf("requests=%+v", requests)
			}
			if result.StopReason != "stop" || clsAnswer(t, result.Answers, "approved") != (ClassifierBoolAnswer{Probability: 0.8}) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
	// .upstream/v1.1.0/packages/ai/test/classifier-models.test.ts:166
	t.Run("rejects images for classifier models without image input before calling the provider", func(t *testing.T) {
		classifier := classifierTestModel("test", "text-only")
		calls := 0
		models := CreateModels()
		models.SetProvider(CreateProvider(CreateProviderOptions{ID: "test", Auth: auth, Models: []AnyModel{classifier},
			Classifiers: ProviderClassifierMap{"test-classifier": {Classify: func(_ context.Context, model *ClassifierModel, _ ClassifierContext, _ ClassifierOptions) (ClassifierResult, error) {
				calls++
				return ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, Answers: ClassifierAnswers{}, StopReason: "stop", Timestamp: time.Now().UnixMilli()}, nil
			}}}}))

		withImages := classifierTestContext()
		withImages.Images = []ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}}
		result := models.Classify(t.Context(), classifier, withImages)
		emptyImages := classifierTestContext()
		emptyImages.Images = []ImageContent{}
		withoutImages := models.Classify(t.Context(), classifier, emptyImages)

		if result.StopReason != "error" || result.ErrorMessage != "Model test/text-only does not accept image input" {
			t.Errorf("result=%+v", result)
		}
		if withoutImages.StopReason != "stop" || calls != 1 {
			t.Errorf("withoutImages=%+v calls=%d", withoutImages, calls)
		}
	})
	// .upstream/v1.1.0/packages/ai/test/classifier-models.test.ts:195
	t.Run("routes OpenAI GPT-6 Luna through the Decisions API with images", func(t *testing.T) {
		models := BuiltinModels()
		luna, _ := models.GetModelOfType(ModelTypeClassifier, "openai", "gpt-6-luna").(*ClassifierModel)
		if luna == nil {
			t.Fatal("missing OpenAI Decisions model")
		}
		if luna.API != "openai-decisions" || !reflect.DeepEqual(luna.Input, []string{"text", "image"}) || luna.ContextWindow != 922000 {
			t.Fatalf("luna=%+v", luna)
		}
		// The chat entry with the same id stays separate.
		if chat := models.GetModel("openai", "gpt-6-luna"); chat == nil || chat.ProviderMeta.API != APIOpenAIResponses {
			t.Fatalf("chat=%+v", chat)
		}

		var urls []string
		withImages := classifierTestContext()
		withImages.Images = []ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}}
		result := models.Classify(t.Context(), luna, withImages, ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{APIKey: "secret", APIKeySet: true, Fetch: clsClient(func(r *http.Request) (*http.Response, error) {
			urls = append(urls, r.URL.String())
			return clsJSON(200, `{"answers":[{"type":"predicate","name":"approved","probability":0.8}]}`), nil
		})}})

		if !reflect.DeepEqual(urls, []string{"https://api.openai.com/v1/decisions"}) {
			t.Fatalf("urls=%v", urls)
		}
		if result.StopReason != "stop" || clsAnswer(t, result.Answers, "approved") != (ClassifierBoolAnswer{Probability: 0.8}) {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v1.1.0/packages/ai/test/classifier-models.test.ts:226
	t.Run("lists OpenAI Decisions models only for API key credentials", func(t *testing.T) {
		apiKeyStore := NewInMemoryCredentialStore()
		if _, err := apiKeyStore.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: "secret"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		oauthStore := NewInMemoryCredentialStore()
		if _, err := oauthStore.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().UnixMilli() + 3_600_000}, nil
		}); err != nil {
			t.Fatal(err)
		}
		withAPIKey := BuiltinModels(CreateModelsOptions{Credentials: apiKeyStore})
		withOAuth := BuiltinModels(CreateModelsOptions{Credentials: oauthStore})

		ids := func(models []AnyModel) []string {
			out := []string{}
			for _, model := range models {
				out = append(out, model.ModelID())
			}
			return out
		}
		available, err := withAPIKey.GetAvailableOfType(t.Context(), ModelTypeClassifier, "openai")
		if err != nil || !reflect.DeepEqual(ids(available), []string{"gpt-6-luna"}) {
			t.Fatalf("api key: %v err=%v", ids(available), err)
		}
		available, err = withOAuth.GetAvailableOfType(t.Context(), ModelTypeClassifier, "openai")
		if err != nil || len(available) != 0 {
			t.Fatalf("oauth: %v err=%v", ids(available), err)
		}
		// Chat models stay available with ChatGPT OAuth.
		chat, err := withOAuth.GetAvailableOfType(t.Context(), ModelTypeChat, "openai")
		if err != nil || !slices.Contains(ids(chat), "gpt-6-luna") {
			t.Fatalf("chat with oauth: err=%v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/classifier-models.test.ts:165
	t.Run("routes OpenRouter classifier models through the System One API", func(t *testing.T) {
		models := BuiltinModels()
		listed := GetBuiltinClassifierModels("openrouter")
		if len(listed) == 0 {
			t.Fatal("no OpenRouter classifier models")
		}
		for _, model := range listed {
			if model.API != "typesafe-system-one" || model.BaseURL != "https://openrouter.ai/api/v1" {
				t.Errorf("model=%+v", model)
			}
			if models.GetModel("openrouter", model.ID) != nil {
				t.Errorf("chat lookup returned %s", model.ID)
			}
			if got := models.GetModelOfType(ModelTypeClassifier, "openrouter", model.ID); !reflect.DeepEqual(got, AnyModel(model)) {
				t.Errorf("of type=%v", got)
			}
		}
	})
}
