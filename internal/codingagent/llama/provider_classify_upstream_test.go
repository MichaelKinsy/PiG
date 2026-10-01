package llama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// classifyServer is the llama-server stand-in of llama-extension.test.ts:331-373. It answers /tokenize, /apply-template and /completion
// for the model "qwen" and records the request paths.
type classifyServer struct {
	mu    sync.Mutex
	paths []string
}

func (s *classifyServer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.paths)
}

func (s *classifyServer) start(t *testing.T) string {
	t.Helper()
	return listen(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.RequestURI())
		s.mu.Unlock()
		var payload struct {
			Model   string `json:"model"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode %s body %q: %v", r.URL.Path, body, err)
		}
		if payload.Model != "qwen" {
			t.Errorf("%s model = %q, want qwen", r.URL.Path, payload.Model)
		}
		switch r.URL.Path {
		case "/tokenize":
			tokens := []int{}
			for _, char := range payload.Content {
				tokens = append(tokens, int(char))
			}
			writeJSON(w, map[string]any{"tokens": tokens})
		case "/apply-template":
			writeJSON(w, map[string]any{"prompt": "<|im_start|>assistant\n"})
		case "/completion":
			writeJSON(w, map[string]any{"completion_probabilities": []any{map[string]any{"top_logprobs": []any{
				map[string]any{"id": 66, "token": "B", "logprob": -0.1},
				map[string]any{"id": 65, "token": "A", "logprob": -2.4},
			}}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// Ports .upstream/v0.99.2/packages/coding-agent/test/llama-extension.test.ts:331-383 ("classifies with selectable models through llama-server").
func TestProviderClassifiesWithSelectableModelsThroughLlamaServer(t *testing.T) {
	server := &classifyServer{}
	url := server.start(t)

	controller := CreateLlamaProvider()
	controller.SetCatalog([]LlamaModelInfo{{ID: "qwen", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded}}}, url, false)
	classifiers := classifierModels(controller.Provider.GetAllModels())
	if len(classifiers) == 0 {
		t.Fatal("missing classifier model")
	}
	classifier := classifiers[0]
	if controller.Provider.Classify == nil {
		t.Fatal("the llama.cpp provider has no classify (provider.ts:262)")
	}

	// Provider auth resolves the OpenAI-compatible /v1 URL, which replaces the model's base URL (llama-extension.test.ts:364).
	withInferenceURL := classifier
	withInferenceURL.BaseURL = url + "/v1"
	result := controller.Provider.Classify(context.Background(), withInferenceURL,
		ai.ClassifierContext{
			State: ai.JsonObject{"message": "The build is red again."},
			Questions: ai.ClassifierQuestions{{ID: "kind", Question: ai.ClassifierChoiceQuestion{
				Instructions: "What is this about?",
				Criteria:     []ai.ClassifierChoiceCriterion{{Key: "billing"}, {Key: "ci"}},
			}}},
		},
		ai.ClassifierOptions{APIKey: "local", APIKeySet: true})

	if result.ErrorMessage != "" {
		t.Fatalf("error message = %q, want none", result.ErrorMessage)
	}
	if len(result.Answers) != 1 || result.Answers[0].ID != "kind" {
		t.Fatalf("answers = %+v", result.Answers)
	}
	choice, ok := result.Answers[0].Answer.(ai.ClassifierChoiceAnswer)
	if !ok || choice.Choice != "ci" {
		t.Fatalf("answer = %#v, want a choice answer %q", result.Answers[0].Answer, "ci")
	}
	if !slices.Contains(server.recorded(), "/completion") {
		t.Fatalf("requests = %v, want /completion", server.recorded())
	}
}
