package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

// The built-in llama.cpp provider's classifier models reach the model registry through the same native-provider registration as its chat
// models, and classifying with one runs llama-server's /tokenize, /apply-template and /completion
// (upstream: packages/coding-agent/src/extensions/llama/provider.ts:204-262 getAllModels and classify; llama-extension.test.ts:331).
func TestLlamaHostPublishesClassifierModelsAndClassifiesThroughTheRegistry(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encode := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		switch r.URL.Path {
		case "/models":
			encode(map[string]any{"data": []any{map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 8192}}}})
		case "/props":
			encode(map[string]any{})
		case "/tokenize":
			var payload struct{ Content string }
			_ = json.NewDecoder(r.Body).Decode(&payload)
			tokens := []int{}
			for _, char := range payload.Content {
				tokens = append(tokens, int(char))
			}
			encode(map[string]any{"tokens": tokens})
		case "/apply-template":
			encode(map[string]any{"prompt": "<|im_start|>assistant\n"})
		case "/completion":
			encode(map[string]any{"completion_probabilities": []any{map[string]any{"top_logprobs": []any{
				map[string]any{"id": 66, "token": "B", "logprob": -0.1},
				map[string]any{"id": 65, "token": "A", "logprob": -2.4},
			}}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	host := llama.NewHost(registry, auth, ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	if err := auth.Set(llama.LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	if result := host.Refresh(context.Background(), true); result.Err != nil {
		t.Fatal(result.Err)
	}

	provider := registry.GetTypedProvider(llama.LlamaProviderID, nil)
	if provider == nil {
		t.Fatal("llama.cpp provider is not registered")
	}
	all, err := allProviderModels(provider)
	if err != nil {
		t.Fatal(err)
	}
	var classifier *ai.ClassifierModel
	for _, model := range all {
		if candidate, ok := model.(*ai.ClassifierModel); ok {
			classifier = candidate
		}
	}
	if classifier == nil || classifier.ID != "qwen" || classifier.API != ai.ClassifierAPILlamaCppClassify || classifier.ContextWindow != 8192 {
		t.Fatalf("classifier model = %+v in %d models", classifier, len(all))
	}
	if provider.Classify == nil {
		t.Fatal("the registered llama.cpp provider has no classify")
	}
	request := *classifier
	request.BaseURL = server.URL + "/v1"
	result, err := provider.Classify(context.Background(), &request, ai.ClassifierContext{
		State: ai.JsonObject{"message": "The build is red again."},
		Questions: ai.ClassifierQuestions{{ID: "kind", Question: ai.ClassifierChoiceQuestion{
			Instructions: "What is this about?",
			Criteria:     []ai.ClassifierChoiceCriterion{{Key: "billing"}, {Key: "ci"}},
		}}},
	}, ai.ClassifierOptions{APIKey: "local", APIKeySet: true})
	if err != nil || result.ErrorMessage != "" || len(result.Answers) != 1 {
		t.Fatalf("classify = %+v, %v", result, err)
	}
	if choice, ok := result.Answers[0].Answer.(ai.ClassifierChoiceAnswer); !ok || choice.Choice != "ci" {
		t.Fatalf("answer = %#v, want choice ci", result.Answers[0].Answer)
	}
}
