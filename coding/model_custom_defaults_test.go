package coding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BuildModel and direct Provider.Stream on a models.json custom model under a built-in provider that omits
// api and/or baseUrl must match ModelRuntime.GetModel (provider-composer.ts:212-261, 336-342).
func TestBuildModelCustomModelUnderBuiltInProviderInheritsDefaults(t *testing.T) {
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("GetBuiltinModelDataGeneratedAt() == nil")
	}
	newer := *generatedAt + 1000

	isolate := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for k, v := range map[string]string{
			"HOME": dir, "PIG_HOME": dir, "PI_HOME": dir,
			"PIG_CODING_AGENT_DIR": dir, "PI_CODING_AGENT_DIR": dir,
			"PI_OFFLINE": "", "PIG_OFFLINE": "",
		} {
			t.Setenv(k, v)
		}
		return dir
	}

	t.Run("provider-shapes-match-runtime-and-stream-to-inherited-api", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		cases := []struct {
			name       string
			provider   string
			modelID    string
			modelsJSON string
			authJSON   string
			storeJSON  string
			wantAPI    ai.API
			wantBase   string
			wantPath   string // non-empty: verify direct Provider.Stream reaches local server on this suffix
		}{
			{
				name:       "groq-omits-api-and-baseurl",
				provider:   "groq",
				modelID:    "my-groq-custom",
				modelsJSON: `{"providers":{"groq":{"models":[{"id":"my-groq-custom","name":"My Groq Custom","contextWindow":64000,"maxTokens":8192}]}}}`,
				authJSON:   `{"groq":{"type":"api_key","key":"gsk-fake"}}`,
				wantAPI:    ai.APIOpenAICompletions,
				wantBase:   "https://api.groq.com/openai/v1",
			},
			{
				name:       "groq-provider-local-baseurl-omits-api-streams-completions",
				provider:   "groq",
				modelID:    "my-groq-local",
				modelsJSON: `{"providers":{"groq":{"baseUrl":"` + server.URL + `","models":[{"id":"my-groq-local","name":"My Groq Local"}]}}}`,
				authJSON:   `{"groq":{"type":"api_key","key":"gsk-fake"}}`,
				wantAPI:    ai.APIOpenAICompletions,
				wantBase:   server.URL,
				wantPath:   "/chat/completions",
			},
			{
				name:       "github-copilot-oauth-omits-api-and-baseurl",
				provider:   "github-copilot",
				modelID:    "my-copilot-custom",
				modelsJSON: `{"providers":{"github-copilot":{"models":[{"id":"my-copilot-custom","name":"My Copilot Custom"}]}}}`,
				authJSON:   `{"github-copilot":{"type":"oauth","refresh":"r","access":"tid=0;exp=9999999999;","expires":9999999999000}}`,
				wantAPI:    ai.APIOpenAICompletions,
				wantBase:   "https://api.individual.githubcopilot.com",
			},
			{
				name:       "opencode-go-multi-api-omits-both",
				provider:   "opencode-go",
				modelID:    "my-og-custom",
				modelsJSON: `{"providers":{"opencode-go":{"models":[{"id":"my-og-custom"}]}}}`,
				authJSON:   `{"opencode-go":{"type":"api_key","key":"oc-fake"}}`,
				wantAPI:    ai.APIOpenAICompletions,
				wantBase:   "https://opencode.ai/inference/go/openai/v1",
			},
			{
				name:       "opencode-go-multi-api-explicit-anthropic-inherits-anthropic-baseurl",
				provider:   "opencode-go",
				modelID:    "my-og-anth",
				modelsJSON: `{"providers":{"opencode-go":{"models":[{"id":"my-og-anth","api":"anthropic-messages"}]}}}`,
				authJSON:   `{"opencode-go":{"type":"api_key","key":"oc-fake"}}`,
				wantAPI:    ai.APIAnthropicMessages,
				wantBase:   "https://opencode.ai/inference/go/anthropic",
			},
			{
				name:       "store-entry-same-id-supplies-omitted-api-and-baseurl-models-json-wins",
				provider:   "groq",
				modelID:    "zz-overlap-model",
				storeJSON:  `{"id":"zz-overlap-model","name":"Store Overlap","api":"openai-responses","provider":"groq","baseUrl":"` + server.URL + `","reasoning":true,"input":["text","image"],"cost":{"input":2,"output":8,"cacheRead":0,"cacheWrite":0},"contextWindow":500000,"maxTokens":32000}`,
				modelsJSON: `{"providers":{"groq":{"models":[{"id":"zz-overlap-model","name":"ModelsJSON Overlap","contextWindow":250000,"maxTokens":16000}]}}}`,
				authJSON:   `{"groq":{"type":"api_key","key":"gsk-fake"}}`,
				wantAPI:    ai.APIOpenAIResponses,
				wantBase:   server.URL,
				wantPath:   "/responses",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				mu.Lock()
				paths = nil
				mu.Unlock()
				dir := isolate(t)
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(tc.modelsJSON), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(tc.authJSON), 0o600); err != nil {
					t.Fatal(err)
				}
				if tc.storeJSON != "" {
					entry := ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(tc.storeJSON)}), CheckedAt: new(1.0), LastModified: &newer}
					if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(context.Background(), tc.provider, entry); err != nil {
						t.Fatal(err)
					}
				}
				svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer svcs.Close()

				rt := svcs.ModelRuntime().GetModel(tc.provider, tc.modelID)
				if rt == nil {
					t.Fatalf("ModelRuntime.GetModel = nil (err=%q)", svcs.ModelRuntime().GetError())
				}
				built, err := BuildModel(tc.provider+"/"+tc.modelID, svcs)
				if err != nil {
					t.Fatalf("BuildModel: %v", err)
				}
				if built.ProviderMeta.API != tc.wantAPI || built.ProviderMeta.BaseURL != tc.wantBase {
					t.Errorf("BuildModel api=%q baseUrl=%q, want api=%q baseUrl=%q (GetModel has api=%q baseUrl=%q)",
						built.ProviderMeta.API, built.ProviderMeta.BaseURL, tc.wantAPI, tc.wantBase, rt.ProviderMeta.API, rt.ProviderMeta.BaseURL)
				}
				if tc.wantPath != "" {
					transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}})
					stream, err := built.Provider.Stream(context.Background(), transcript, ai.StreamOptions{MaxRetries: new(0)})
					if err != nil {
						t.Fatalf("direct Provider.Stream: %v", err)
					}
					for range stream.Events(context.Background()) {
					}
					mu.Lock()
					got := append([]string(nil), paths...)
					mu.Unlock()
					if len(got) == 0 || !strings.HasSuffix(got[0], tc.wantPath) {
						t.Errorf("direct Provider.Stream paths = %v, want suffix %q", got, tc.wantPath)
					}
				}
			})
		}
	})

	t.Run("custom-non-builtin-provider-omitting-api-stays-rejected", func(t *testing.T) {
		dir := isolate(t)
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"custom-no-api":{"baseUrl":"https://custom.invalid/v1","apiKey":"k","models":[{"id":"m1"}]}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		defer svcs.Close()
		if !strings.Contains(svcs.ModelRuntime().GetError(), `no "api" specified`) {
			t.Fatalf("expected load error for custom provider omitting api, got %q", svcs.ModelRuntime().GetError())
		}
		if m, err := BuildModel("custom-no-api/m1", svcs); m != nil || err == nil || err.Error() != "Model not found: custom-no-api/m1" {
			t.Fatalf("BuildModel(custom-no-api/m1) = %v, %v; want Model not found", m, err)
		}
	})
}

// TestBuildModelOpenCodeGoAnthropicCustomModelRoutesToProviderHostNotAnthropic pins the wrong-host
// failure mode: a models.json custom model under opencode-go that sets api="anthropic-messages" and
// omits baseUrl must send a direct Provider.Stream request to https://opencode.ai/inference/go/anthropic...,
// never to api.anthropic.com with the opencode-go key.
func TestBuildModelOpenCodeGoAnthropicCustomModelRoutesToProviderHostNotAnthropic(t *testing.T) {
	dir := t.TempDir()
	for k, v := range map[string]string{
		"HOME": dir, "PIG_HOME": dir, "PI_HOME": dir,
		"PIG_CODING_AGENT_DIR": dir, "PI_CODING_AGENT_DIR": dir,
		"PI_OFFLINE": "1", "PIG_OFFLINE": "1",
	} {
		t.Setenv(k, v)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"opencode-go":{"models":[{"id":"my-og-anth-wire","api":"anthropic-messages"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"opencode-go":{"type":"api_key","key":"oc-provider-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer svcs.Close()

	built, err := BuildModel("opencode-go/my-og-anth-wire", svcs)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if built.ProviderMeta.API != ai.APIAnthropicMessages || built.ProviderMeta.BaseURL != "https://opencode.ai/inference/go/anthropic" {
		t.Errorf("BuildModel api=%q baseUrl=%q, want anthropic-messages / https://opencode.ai/inference/go/anthropic",
			built.ProviderMeta.API, built.ProviderMeta.BaseURL)
	}

	var capturedURL *url.URL
	capturedErr := errors.New("request captured hermetically")
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}})
	stream, err := built.Provider.Stream(context.Background(), transcript, ai.StreamOptions{
		MaxRetries: new(0),
		Fetch: &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
			u := *req.URL
			capturedURL = &u
			return nil, capturedErr
		})},
	})
	if err != nil {
		t.Fatalf("direct Provider.Stream: %v", err)
	}
	for range stream.Events(context.Background()) {
	}

	if capturedURL == nil {
		t.Fatal("direct Provider.Stream did not issue an HTTP request to the capture transport")
	}
	if capturedURL.Host == "api.anthropic.com" {
		t.Fatalf("direct Provider.Stream sent opencode-go request to wrong vendor host %q (%s)", capturedURL.Host, capturedURL.String())
	}
	const wantPrefix = "https://opencode.ai/inference/go/anthropic"
	if capturedURL.Scheme != "https" || capturedURL.Host != "opencode.ai" || !strings.HasPrefix(capturedURL.String(), wantPrefix) {
		t.Fatalf("direct Provider.Stream request URL = %q, want prefix %q", capturedURL.String(), wantPrefix)
	}
}
