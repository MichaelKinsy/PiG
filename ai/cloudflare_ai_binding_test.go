package ai

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/ai/test/cloudflare-ai-binding.test.ts.

const bindingPrefix = "https://workers-binding.ai/ai-gateway/gateways/my-gateway"

type bindingRequest struct {
	url, method string
	header      http.Header
	body        string
}

func fakeAIBinding(response *http.Response) (AIBinding, *[]bindingRequest) {
	var requests []bindingRequest
	return AIBinding{Fetch: func(request *http.Request) (*http.Response, error) {
		body := ""
		if request.Body != nil {
			raw, _ := io.ReadAll(request.Body)
			body = string(raw)
		}
		requests = append(requests, bindingRequest{request.URL.String(), request.Method, request.Header.Clone(), body})
		if response != nil {
			return response, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}}, &requests
}

// cloudflare-ai-binding.test.ts:25 "passes requests to the binding untouched".
func TestCreateAIBindingFetchPassesRequestsToTheBindingUntouched(t *testing.T) {
	bindingResponse := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Cf-Aig-Log-Id": {"log-1"}}, Body: io.NopCloser(strings.NewReader("data: {}\n\n"))}
	binding, requests := fakeAIBinding(bindingResponse)
	fetch, err := CreateAIBindingFetch(binding)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"claude","messages":[{"role":"user","content":"hi"}]}`
	request, err := http.NewRequest(http.MethodPost, bindingPrefix+"/anthropic/v1/messages?beta=true", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("cf-aig-authorization", "Bearer "+CloudflareGatewayBindingAuthSentinel)
	request.Header.Set("anthropic-version", "2023-06-01")

	response, err := fetch(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	if len(*requests) != 1 {
		t.Fatalf("binding saw %d requests", len(*requests))
	}
	got := (*requests)[0]
	if got.url != bindingPrefix+"/anthropic/v1/messages?beta=true" || got.method != "POST" || got.body != body {
		t.Fatalf("request = %+v", got)
	}
	want := http.Header{"Content-Type": {"application/json"}, "Cf-Aig-Authorization": {"Bearer " + CloudflareGatewayBindingAuthSentinel}, "Anthropic-Version": {"2023-06-01"}}
	if !reflect.DeepEqual(got.header, want) {
		t.Fatalf("headers = %v, want %v", got.header, want)
	}
	if response != bindingResponse {
		t.Fatal("the binding's response was not returned as is")
	}
	if text, _ := io.ReadAll(response.Body); string(text) != "data: {}\n\n" {
		t.Fatalf("response body = %q", text)
	}
}

// cloudflare-ai-binding.test.ts:63 "rejects a binding with no fetch() at construction, not on first request".
func TestCreateAIBindingFetchRejectsABindingWithoutFetch(t *testing.T) {
	fetch, err := CreateAIBindingFetch(AIBinding{})
	if err == nil || !strings.Contains(err.Error(), "does not expose fetch()") || fetch != nil {
		t.Fatalf("CreateAIBindingFetch(no fetch) = %v, %v", fetch != nil, err)
	}
}

// A binding error is the request's error.
func TestCreateAIBindingFetchReturnsTheBindingError(t *testing.T) {
	want := errors.New("binding down")
	fetch, err := CreateAIBindingFetch(AIBinding{Fetch: func(*http.Request) (*http.Response, error) { return nil, want }})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, bindingPrefix+"/openai", nil)
	response, err := fetch(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

// cloudflare-ai-binding.test.ts:72 "keeps SDK placeholder auth off the wire when paired with null auth headers": the sentinel
// satisfies the request-auth check and the nil headers delete the SDK's own `Authorization: Bearer unused` placeholder.
func TestAIBindingFetchKeepsPlaceholderAuthOffTheWire(t *testing.T) {
	binding, requests := fakeAIBinding(&http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"bad_request","message":"stubbed"}}`))})
	fetch, err := CreateAIBindingFetch(binding)
	if err != nil {
		t.Fatal(err)
	}
	model := &Model{ID: "test-model", DisplayName: "Test Model", Input: []string{"text"},
		ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "openai", BaseURL: bindingPrefix + "/openai"},
		Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
	zero := 0
	stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), StreamOptions{
		Headers:    ProviderHeaders{"cf-aig-authorization": new("Bearer " + CloudflareGatewayBindingAuthSentinel), "Authorization": nil, "x-api-key": nil},
		Fetch:      &http.Client{Transport: fetch},
		MaxRetries: &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()

	if result.StopReason != StopReasonError {
		t.Fatalf("stop reason = %s", result.StopReason)
	}
	if len(*requests) != 1 || (*requests)[0].url != bindingPrefix+"/openai/chat/completions" {
		t.Fatalf("requests = %+v", *requests)
	}
	for name := range (*requests)[0].header {
		if strings.EqualFold(name, "authorization") || strings.EqualFold(name, "x-api-key") {
			t.Errorf("header %s reached the binding", name)
		}
	}
	if (*requests)[0].header.Get("cf-aig-authorization") != "Bearer "+CloudflareGatewayBindingAuthSentinel {
		t.Errorf("headers = %v", (*requests)[0].header)
	}
}

func TestCloudflareWorkersAIRESTBaseURLIsTheUnversionedRoot(t *testing.T) {
	if CloudflareWorkersAIRESTBaseURL+"/v1" != CloudflareWorkersAIBaseURL {
		t.Fatalf("REST root %q is not the /v1 root %q without its suffix", CloudflareWorkersAIRESTBaseURL, CloudflareWorkersAIBaseURL)
	}
}
