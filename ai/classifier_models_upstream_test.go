package ai

import (
	"context"
	"net/http"
	"reflect"
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
