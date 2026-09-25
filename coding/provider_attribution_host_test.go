package coding

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// provider-attribution.ts matchesHost uses WHATWG URL.hostname, which lowercases DNS hosts.
// D26 changes branding values only, not host equivalence or the telemetry gate.
func TestAttributionMixedCaseHostMatchesUpstream(t *testing.T) {
	for _, tc := range attributionHostCases() {
		t.Run(tc.host, func(t *testing.T) {
			capture := &attributionCaptureProvider{}
			provider := newProviderAttributionProvider(capture, "custom", "https://"+tc.host+"/v1", func() bool { return true }, nil)
			stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{}), ai.StreamOptions{SessionID: "session"})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
				t.Fatalf("result=%v", result)
			}
			if !reflect.DeepEqual(capture.options.Headers, ai.ProviderHeadersFromStrings(tc.headers)) {
				t.Fatalf("captured headers=%v, want %v", capture.options.Headers, tc.headers)
			}
		})
	}
}

type attributionHostCase struct {
	host    string
	headers map[string]string
}

func attributionHostCases() []attributionHostCase {
	return []attributionHostCase{
		{"INTEGRATE.API.NVIDIA.COM", map[string]string{"X-BILLING-INVOKE-ORIGIN": "PiG"}},
		{"API.CLOUDFLARE.COM", map[string]string{"User-Agent": "pig-coding-agent"}},
		{"GATEWAY.AI.CLOUDFLARE.COM", map[string]string{"User-Agent": "pig-coding-agent"}},
		{"OPENCODE.AI", map[string]string{"x-opencode-session": "session", "x-opencode-client": "pig"}},
	}
}

// The production wrapper selects by the original model endpoint while its inner provider sends to a loopback capture server. No provider hostname is resolved or contacted.
func TestAttributionMixedCaseHostProductionRequests(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, tc := range attributionHostCases() {
			for _, enabled := range []bool{false, true} {
				t.Run(string(api)+"/"+tc.host+"/telemetry="+strconv.FormatBool(enabled), func(t *testing.T) {
					requests := make(chan struct {
						headers http.Header
						body    []byte
					}, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						requests <- struct {
							headers http.Header
							body    []byte
						}{r.Header.Clone(), body}
						w.Header().Set("Content-Type", "text/event-stream")
						reply := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
						if api == ai.APIOpenAIResponses {
							reply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
						}
						_, _ = io.WriteString(w, reply)
					}))
					defer server.Close()
					// Provider identity is custom, so only URL host matching can add the defaults.
					baseURL := "https://" + tc.host + "/v1"
					var inner ai.Provider
					if api == ai.APIOpenAICompletions {
						inner = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, Model: "test", APIKey: "fixture", ProviderID: "custom"})
					} else {
						inner = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, Model: "test", APIKey: "fixture", ProviderID: "custom"})
					}
					provider := newProviderAttributionProvider(inner, "custom", baseURL, func() bool { return enabled }, map[string]string{"X-Configured": "kept"})
					defer func() {
						if err := provider.Close(); err != nil {
							t.Error(err)
						}
					}()
					options := ai.StreamOptions{SessionID: "session", CacheRetention: ai.CacheRetentionNone, Headers: ai.ProviderHeaders{"X-Request": new("kept")}}
					stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), options)
					if err != nil {
						t.Fatal(err)
					}
					if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
						t.Fatalf("result=%#v", result)
					}
					select {
					case request := <-requests:
						want := http.Header{}
						for name, value := range map[string]string{"Authorization": "Bearer fixture", "Content-Type": "application/json", "User-Agent": ai.PiUserAgent(), "Content-Length": strconv.Itoa(len(request.body)), "Accept-Encoding": "gzip", "X-Configured": "kept", "X-Request": "kept"} {
							want.Set(name, value)
						}
						if enabled || tc.host == "OPENCODE.AI" {
							for name, value := range tc.headers {
								want.Set(name, value)
							}
						}
						if !reflect.DeepEqual(request.headers, want) {
							t.Errorf("headers = %v, want %v", request.headers, want)
						}
						var body map[string]any
						if err := json.Unmarshal(request.body, &body); err != nil {
							t.Fatal(err)
						}
						if body["model"] != "test" || body["stream"] != true {
							t.Fatalf("request = %s", request.body)
						}
						key := "messages"
						var content any = "hello"
						if api == ai.APIOpenAIResponses {
							key = "input"
							content = []any{map[string]any{"type": "input_text", "text": "hello"}}
						}
						if !reflect.DeepEqual(body[key], []any{map[string]any{"role": "user", "content": content}}) {
							t.Errorf("request = %s", request.body)
						}
					default:
						t.Fatal("provider completed without a request")
					}
					if !reflect.DeepEqual(options.Headers, ai.ProviderHeaders{"X-Request": new("kept")}) {
						t.Fatal("wrapper mutated caller headers")
					}
				})
			}
		}
	}
}

func TestAttributionHostEquivalenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://InTeGrAtE.Api.Nvidia.Com:443/v1", true},
		{"https://integrate.api.nvidia.com/v1", true},
		{"https://INTEGRATE.API.NVIDIA.COM.example.test/v1", false},
		{"https://INTEGRATE.API.NVIDIA.COM@other.test/v1", false},
		{"https://other.test/INTEGRATE.API.NVIDIA.COM", false},
		{"https://INTEGRATE.API.NVIDIA.COM./v1", false},
		{"", false}, {"://INTEGRATE.API.NVIDIA.COM", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			if got := matchesProviderHost(tc.url, "integrate.api.nvidia.com"); got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
		})
	}
	// OpenRouter deliberately keeps Pi's separate case-sensitive substring rule.
	if got := mergeProviderAttributionHeaders("custom", "https://OPENROUTER.AI/v1", true, ""); got != nil {
		t.Fatalf("OpenRouter headers=%v", got)
	}
	for _, tc := range attributionHostCases() {
		upper := mergeProviderAttributionHeaders("custom", "https://"+tc.host+"/v1", true, "", ai.ProviderHeaders{"User-Agent": nil, "X-BILLING-INVOKE-ORIGIN": new("caller")})
		lower := mergeProviderAttributionHeaders("custom", "https://"+strings.ToLower(tc.host)+"/v1", true, "", ai.ProviderHeaders{"User-Agent": nil, "X-BILLING-INVOKE-ORIGIN": new("caller")})
		if !reflect.DeepEqual(upper, lower) {
			t.Fatalf("case changed caller overrides: upper=%v lower=%v", upper, lower)
		}
	}
}
