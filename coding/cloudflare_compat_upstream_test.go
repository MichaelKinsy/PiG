package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type cloudflareCompatTransport func(*http.Request) (*http.Response, error)

func (f cloudflareCompatTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCloudflareExtensionHostAuthUsesCatalogModel(t *testing.T) {
	services := newRuntimeTestServices(t)
	if err := services.Auth().Set("cloudflare-ai-gateway", ai.Credential{Type: ai.CredentialAPIKey, Key: "test-token", Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "test-account", "CLOUDFLARE_GATEWAY_ID": "test-gateway"}}); err != nil {
		t.Fatal(err)
	}
	probe := &modelOperationBridgeProbe{actions: map[string]any{}}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	defer detach()
	auth := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)(t.Context(), "cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6")
	if auth["ok"] != true {
		t.Fatalf("host auth=%+v, want same successful resolution as ModelRegistry.GetAPIKeyAndHeaders", auth)
	}
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ResolvedRequestAuth
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Env["CLOUDFLARE_ACCOUNT_ID"] != "test-account" || decoded.Env["CLOUDFLARE_GATEWAY_ID"] != "test-gateway" {
		t.Fatalf("host lost scoped env: %s", data)
	}
	if value := decoded.Headers["cf-aig-authorization"]; value == nil || *value != "Bearer test-token" {
		t.Fatalf("host lost gateway header: %s", data)
	}
	for _, name := range []string{"Authorization", "x-api-key"} {
		if value, exists := decoded.Headers[name]; !exists || value != nil {
			t.Fatalf("host lost %s suppression: %s", name, data)
		}
	}
}

func TestCloudflareScopedEnvironmentAcrossAPIs(t *testing.T) {
	for _, tc := range []struct {
		provider string
		api      ai.API
	}{
		{"cloudflare-ai-gateway", ai.APIOpenAICompletions},
		{"cloudflare-ai-gateway", ai.APIOpenAIResponses},
		{"cloudflare-ai-gateway", ai.APIAnthropicMessages},
		{"cloudflare-workers-ai", ai.APIOpenAICompletions},
	} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/failure=%t", tc.provider, tc.api, fail), func(t *testing.T) {
				services := newRuntimeTestServices(t)
				env := map[string]string{"CLOUDFLARE_ACCOUNT_ID": "scoped-account", "CLOUDFLARE_GATEWAY_ID": "scoped-gateway"}
				if err := services.Auth().Set(tc.provider, ai.Credential{Type: ai.CredentialAPIKey, Key: "scoped-token", Env: env}); err != nil {
					t.Fatal(err)
				}
				var model *ai.Model
				for _, candidate := range services.ModelRuntime().GetModels() {
					if candidate.ProviderMeta.ProviderID == tc.provider && candidate.ProviderMeta.API == tc.api {
						model = candidate
						break
					}
				}
				if model == nil {
					t.Fatalf("pinned catalog has no %s/%s model", tc.provider, tc.api)
				}
				base := strings.NewReplacer("{CLOUDFLARE_ACCOUNT_ID}", "scoped-account", "{CLOUDFLARE_GATEWAY_ID}", "scoped-gateway").Replace(model.ProviderMeta.BaseURL)
				suffix := map[ai.API]string{ai.APIOpenAICompletions: "/chat/completions", ai.APIOpenAIResponses: "/responses", ai.APIAnthropicMessages: "/v1/messages?beta=true"}[tc.api]
				called := false
				fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
					called = true
					if request.URL.String() != base+suffix {
						t.Errorf("URL=%s, want %s", request.URL, base+suffix)
					}
					if tc.provider == "cloudflare-ai-gateway" {
						if request.Header.Get("cf-aig-authorization") != "Bearer scoped-token" || request.Header.Get("Authorization") != "" || request.Header.Get("x-api-key") != "" {
							t.Errorf("gateway headers=%v", request.Header)
						}
					} else if request.Header.Get("Authorization") != "Bearer scoped-token" {
						t.Errorf("workers headers=%v", request.Header)
					}
					if fail {
						return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"scoped-failure"}}`))}, nil
					}
					body := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"
					if tc.api == ai.APIOpenAIResponses {
						body = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
					} else if tc.api == ai.APIAnthropicMessages {
						body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				result := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{}}, ai.StreamOptions{Fetch: fetch})
				if !called {
					t.Fatalf("request did not reach fetch: %+v", result)
				}
				if fail {
					if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "scoped-failure") {
						t.Fatalf("failure=%+v", result)
					}
				} else if result.StopReason != ai.StopReasonStop {
					t.Fatalf("result=%+v", result)
				}
			})
		}
	}
}

func TestModelRuntimeCloudflareCompatUpstream(t *testing.T) {
	for _, extensionStyle := range []bool{false, true} {
		name := "materializes the Cloudflare endpoint through ModelRuntime streaming"
		if extensionStyle {
			name = "materializes the Cloudflare endpoint after extension-style auth resolution"
		}
		t.Run(name, func(t *testing.T) {
			// .upstream/v0.87.1/packages/coding-agent/test/model-runtime-cloudflare-compat.test.ts:60,76
			services := newRuntimeTestServices(t)
			if err := services.Auth().Set("cloudflare-ai-gateway", ai.Credential{Type: ai.CredentialAPIKey, Key: "test-token", Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "test-account", "CLOUDFLARE_GATEWAY_ID": "test-gateway"}}); err != nil {
				t.Fatal(err)
			}
			model := services.ModelRuntime().GetModel("cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6")
			if model == nil {
				t.Fatal("missing model")
			}
			calls := 0
			options := ai.StreamOptions{Fetch: &http.Client{Transport: cloudflareCompatTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				want := "https://gateway.ai.cloudflare.com/v1/test-account/test-gateway/compat/chat/completions"
				if r.URL.String() != want {
					t.Errorf("URL=%s want=%s", r.URL, want)
				}
				if r.Header.Get("cf-aig-authorization") != "Bearer test-token" {
					t.Errorf("headers=%v", r.Header)
				}
				if extensionStyle && (r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "") {
					t.Errorf("suppressed headers leaked: %v", r.Header)
				}
				data, err := json.Marshal([]any{extensionStyle, r.URL.String(), r.Header.Get("cf-aig-authorization"), len(r.Header.Values("Authorization")) > 0, r.Header.Get("Authorization"), len(r.Header.Values("x-api-key")) > 0, r.Header.Get("x-api-key")})
				if err != nil {
					return nil, err
				}
				fmt.Println("CLOUDFLARE_REQUEST " + string(data))
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))}, nil
			})}}
			if extensionStyle {
				auth := services.Registry().GetAPIKeyAndHeaders(t.Context(), model)
				if !auth.OK {
					t.Fatalf("auth=%+v", auth)
				}
				if value := auth.Headers["cf-aig-authorization"]; value == nil || *value != "Bearer test-token" {
					t.Fatalf("auth=%+v", auth)
				}
				for _, name := range []string{"Authorization", "x-api-key"} {
					if value, present := auth.Headers[name]; !present || value != nil {
						t.Fatalf("missing suppression %s: %+v", name, auth)
					}
				}
				if auth.APIKey != nil {
					options.APIKey = *auth.APIKey
				}
				options.Headers = auth.Headers
				options.Env = auth.Env
				stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{}}), options)
				if err != nil {
					t.Fatal(err)
				}
				if got := stream.Result(); got.StopReason != ai.StopReasonStop {
					t.Fatalf("result=%#v", got)
				}
			} else {
				if got := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{}}, options); got.StopReason != ai.StopReasonStop {
					t.Fatalf("result=%#v", got)
				}
			}
			if calls != 1 {
				t.Fatalf("HTTP calls=%d", calls)
			}
		})
	}
}
