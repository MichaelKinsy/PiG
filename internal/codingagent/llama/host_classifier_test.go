package llama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The native provider registers every model of getAllModels, classifiers included, and owns their classify operation
// (upstream: packages/coding-agent/src/extensions/llama/provider.ts:204-262, index.ts:44 registerProvider).
func TestHostRegistersClassifierModelsAndTheirImplementation(t *testing.T) {
	clearLlamaEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			writeJSON(w, map[string]any{"data": []any{map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 4096}}}})
		case "/props":
			writeJSON(w, map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	host, registry, credentials := newTestHost(t, t.TempDir())
	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Key: "local", Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	if result := host.Refresh(context.Background(), true); result.Err != nil || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}

	config := registry.last()
	if len(config.Models) != 2 {
		t.Fatalf("registered models = %+v, want the chat model then its classifier", config.Models)
	}
	chat, classifier := config.Models[0], config.Models[1]
	if chat.ID != "qwen" || chat.Type != "" && chat.Type != ai.ModelTypeChat {
		t.Fatalf("chat entry = %+v", chat)
	}
	if classifier.ID != "qwen" || classifier.Type != ai.ModelTypeClassifier || classifier.API != "llama-cpp-classify" || classifier.ContextWindow != 4096 || len(classifier.Input) != 1 || classifier.Input[0] != "text" {
		t.Fatalf("classifier entry = %+v", classifier)
	}
	if implementation := config.Classifiers[ai.ClassifierAPILlamaCppClassify]; implementation == nil || implementation.Classify == nil {
		t.Fatalf("classifier implementations = %+v, want llama-cpp-classify", config.Classifiers)
	}
}
