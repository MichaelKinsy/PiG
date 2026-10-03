package coding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Extension model headers reach a request only through ModelRuntime.getAuth(model):
// packages/coding-agent/src/core/model-runtime.ts:470-495 and :574-610, provider-composer.ts:431-443 (rawModelHeaders).
// The model projection carries no headers (provider-composer.ts:271).

func headerText(value *string) string {
	if value == nil {
		return "<absent>"
	}
	return *value
}

func headerTestModel(headers map[string]string) *ai.Model {
	model := authOptionsTestModel("header-model")
	model.ProviderMeta.Headers = headers
	return model
}

func registerCapturingHeaderProvider(t *testing.T, services *Services, headers map[string]string) (*ai.Model, func() (ai.StreamOptions, *ai.Model)) {
	t.Helper()
	var mu sync.Mutex
	var captured ai.StreamOptions
	var capturedModel *ai.Model
	if err := services.ModelRuntime().RegisterProvider("header-provider", ProviderConfigInput{
		BaseURL: "https://example.test/v1", APIKey: "generated-key", API: ai.APIOpenAICompletions,
		StreamSimple: func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			mu.Lock()
			captured, capturedModel = options, model
			mu.Unlock()
			return nil, errors.New("captured")
		},
		Models: []ai.AnyModel{headerTestModel(headers)},
	}); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("header-provider", "header-model")
	if model == nil {
		t.Fatal("registered model is absent")
	}
	return model, func() (ai.StreamOptions, *ai.Model) {
		mu.Lock()
		defer mu.Unlock()
		return captured, capturedModel
	}
}

func TestNativeExtensionModelHeadersReachRequestAndNotProjectionUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	model, captured := registerCapturingHeaderProvider(t, services, map[string]string{"x-model": "model"})
	if model.ProviderMeta.Headers != nil {
		t.Fatalf("GetModel exposes headers Pi strips: %v", model.ProviderMeta.Headers)
	}
	services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{})
	options, seen := captured()
	if got := options.Headers["x-model"]; got == nil || *got != "model" {
		t.Fatalf("x-model = %s, want model", headerText(got))
	}
	if seen == nil || seen.ProviderMeta.Headers != nil {
		t.Fatalf("model passed to streamSimple = %+v, want no headers", seen)
	}
}

func TestNativeExtensionModelHeaderValuesResolveFromRequestEnvUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	model, captured := registerCapturingHeaderProvider(t, services, map[string]string{"x-model": "$PIG_NATIVE_MODEL_HEADER"})
	services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{Env: ai.ProviderEnv{"PIG_NATIVE_MODEL_HEADER": "resolved"}})
	options, _ := captured()
	if got := options.Headers["x-model"]; got == nil || *got != "resolved" {
		t.Fatalf("x-model = %s, want resolved", headerText(got))
	}
}

func TestNativeProviderModelsJSONOverrideHeadersReachRequestUpstream(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"header-provider":{"modelOverrides":{"header-model":{"headers":{"x-override":"ov"}}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	model, captured := registerCapturingHeaderProvider(t, services, map[string]string{"x-model": "model"})
	services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{})
	options, _ := captured()
	for name, want := range map[string]string{"x-override": "ov", "x-model": "model"} {
		if got := options.Headers[name]; got == nil || *got != want {
			t.Fatalf("%s = %s, want %s", name, headerText(got), want)
		}
	}
}

// Pi model-runtime.ts:470-495,574-610 assembles model headers before the transform. Both OpenAI leaves see only the transformed request headers.
func TestNativeExtensionHeaderDeletedByTransformIsNotResentUpstream(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, kind := range []ai.CredentialType{ai.CredentialAPIKey, ai.CredentialOAuth} {
			for _, success := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/success=%v", api, kind, success), func(t *testing.T) {
					var mu sync.Mutex
					var got http.Header
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						got = r.Header.Clone()
						mu.Unlock()
						if !success {
							http.Error(w, "stop", http.StatusInternalServerError)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						if api == ai.APIOpenAIResponses {
							_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
						} else {
							_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
						}
					}))
					defer server.Close()
					credential := ai.Credential{Type: kind, Key: "stored-key"}
					wantKey := "stored-key"
					config := ProviderConfigInput{BaseURL: server.URL, API: api, Models: []ai.AnyModel{headerTestModel(map[string]string{"x-model": "model"})}}
					if kind == ai.CredentialOAuth {
						credential = ai.Credential{Type: kind, Access: "expired-access", Refresh: "refresh"}
						wantKey = "refreshed-access"
						config.OAuth = &ExtensionOAuthConfig{
							Name: "Injected OAuth",
							Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
								return ai.Credential{}, errors.New("unused login")
							},
							RefreshToken: func(_ context.Context, current ai.Credential) (ai.Credential, error) {
								current.Access = "refreshed-access"
								current.Expires = time.Now().Add(time.Hour).UnixMilli()
								return current, nil
							},
							GetAPIKey: func(current ai.Credential) string { return current.Access },
						}
					}
					credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"header-provider": credential})
					runtime := createRuntimeOnCredentials(t, credentials, t.TempDir())
					if err := runtime.RegisterProvider("header-provider", config); err != nil {
						t.Fatal(err)
					}
					model := runtime.GetModel("header-provider", "header-model")
					transforms := 0
					result := runtime.CompleteSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{
						TransformHeaders: func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
							transforms++
							if got := headers["x-model"]; got == nil || *got != "model" {
								t.Errorf("transform did not receive x-model: %s", headerText(got))
							}
							delete(headers, "x-model")
							return headers, nil
						},
					})
					if transforms != 1 {
						t.Fatalf("transforms = %d, want one per request", transforms)
					}
					wantStop := ai.StopReasonError
					if success {
						wantStop = ai.StopReasonStop
					}
					if result.StopReason != wantStop {
						t.Fatalf("stop = %s, want %s; error = %s", result.StopReason, wantStop, result.ErrorMessage)
					}
					mu.Lock()
					defer mu.Unlock()
					if got == nil {
						t.Fatal("server saw no request")
					}
					if value := got.Get("X-Model"); value != "" {
						t.Fatalf("X-Model = %q, want absent", value)
					}
					if got.Get("Authorization") != "Bearer "+wantKey {
						t.Fatal("request did not use the injected credential")
					}
					if kind == ai.CredentialOAuth {
						stored, err := credentials.Read(t.Context(), "header-provider")
						if err != nil || stored == nil || stored.Access != wantKey {
							t.Fatalf("refresh was not persisted to the injected store: %v", err)
						}
					}
				})
			}
		}
	}
}
