package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type capturedProviderRequest struct {
	header http.Header
	host   string
	body   []byte
}

func rejectingProviderServer(t *testing.T) (*httptest.Server, <-chan capturedProviderRequest) {
	t.Helper()
	requests := make(chan capturedProviderRequest, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests <- capturedProviderRequest{header: request.Header.Clone(), host: request.Host, body: body}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("x-amzn-errortype", "ValidationException")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"message":"expected test rejection"}`))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func providerWireTranscript() TranscriptContext {
	return NormalizeContext(Context{
		SystemPrompt: "system",
		Messages:     []Message{UserMessage{Content: UserText("hello")}},
	})
}

func requestHeaderOptions() StreamOptions {
	return StreamOptions{Headers: ProviderHeaders{
		"x-configured": new("request"),
		"X-Delete":     nil,
		"X-Request":    new("yes"),
	}}
}

func assertPreparedProviderHeaders(t *testing.T, request capturedProviderRequest) {
	t.Helper()
	if got := request.header.Get("X-Configured"); got != "request" {
		t.Fatalf("X-Configured = %q, want request", got)
	}
	if got := request.header.Get("X-Delete"); got != "" {
		t.Fatalf("X-Delete = %q, want deleted", got)
	}
	if got := request.header.Get("X-Request"); got != "yes" {
		t.Fatalf("X-Request = %q, want yes", got)
	}
}

func TestAnthropicAppliesPreparedHeadersAndRequestEnvironment(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	provider := NewAnthropicProvider(AnthropicConfig{
		APIKey: "configured-key", Model: "claude-test", BaseURL: server.URL,
		ExtraHeaders: map[string]string{"X-Configured": "configured", "X-Delete": "configured"},
		Env:          ProviderEnv{"PI_CACHE_RETENTION": "long"},
	})
	options := requestHeaderOptions()
	options.Env = ProviderEnv{"PI_CACHE_RETENTION": "none"}
	options.Headers["x-api-key"] = nil
	if _, err := provider.Stream(context.Background(), providerWireTranscript(), options); err == nil {
		t.Fatal("Stream error = nil, want test rejection")
	}
	request := <-requests
	assertPreparedProviderHeaders(t, request)
	if request.header.Get("x-api-key") != "" {
		t.Fatalf("x-api-key = %q, want deleted", request.header.Get("x-api-key"))
	}
	if strings.Contains(string(request.body), `"cache_control"`) {
		t.Fatalf("request environment did not disable cache retention: %s", request.body)
	}
}

func TestGoogleAppliesPreparedHeaders(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	provider := NewGoogleProvider(GoogleConfig{
		APIKey: "configured-key", Model: "gemini-test", BaseURL: server.URL,
		ExtraHeaders: map[string]string{"X-Configured": "configured", "X-Delete": "configured"},
	})
	options := requestHeaderOptions()
	options.Headers["x-goog-api-key"] = nil
	if _, err := provider.Stream(context.Background(), providerWireTranscript(), options); err == nil {
		t.Fatal("Stream error = nil, want test rejection")
	}
	request := <-requests
	assertPreparedProviderHeaders(t, request)
	if request.header.Get("x-goog-api-key") != "" {
		t.Fatalf("x-goog-api-key = %q, want deleted", request.header.Get("x-goog-api-key"))
	}
}

func TestMistralAppliesPreparedHeadersAndRequestSessionID(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	provider := NewMistralProvider(MistralConfig{
		APIKey: "configured-key", Model: "mistral-test", BaseURL: server.URL,
		ExtraHeaders: map[string]string{"X-Configured": "configured", "X-Delete": "configured"},
		SessionID:    "configured-session",
	})
	options := requestHeaderOptions()
	options.SessionID = "request-session"
	options.Headers["Authorization"] = nil
	if _, err := provider.Stream(context.Background(), providerWireTranscript(), options); err == nil {
		t.Fatal("Stream error = nil, want test rejection")
	}
	request := <-requests
	assertPreparedProviderHeaders(t, request)
	if request.header.Get("Authorization") != "" {
		t.Fatalf("Authorization = %q, want deleted", request.header.Get("Authorization"))
	}
	if got := request.header.Get("x-affinity"); got != "request-session" {
		t.Fatalf("x-affinity = %q, want request-session", got)
	}
}

func bedrockReservedHeaderOptions(value *string) StreamOptions {
	options := requestHeaderOptions()
	options.Env = ProviderEnv{
		"AWS_BEDROCK_SKIP_AUTH": "1",
		"AWS_REGION":            "eu-test-1",
		"PI_CACHE_RETENTION":    "none",
	}
	options.Headers["aUtHoRiZaTiOn"] = value
	options.Headers["hOsT"] = value
	options.Headers["X-AmZ-Date"] = value
	return options
}

func assertBedrockReservedHeadersOwnedBySDK(t *testing.T, request capturedProviderRequest, serverURL string) {
	t.Helper()
	if got := request.header.Get("Authorization"); got == "" || got == "caller-reserved" {
		t.Fatalf("Authorization = %q, want SDK signing value", got)
	}
	if got := request.header.Get("X-Amz-Date"); got == "" || got == "caller-reserved" {
		t.Fatalf("X-Amz-Date = %q, want SDK signing value", got)
	}
	wantHost := strings.TrimPrefix(serverURL, "http://")
	if request.host != wantHost {
		t.Fatalf("Host = %q, want endpoint host %q", request.host, wantHost)
	}
}

func TestBedrockReservedHeaderMiddlewarePreservesSDKValues(t *testing.T) {
	for _, family := range []struct {
		name   string
		header string
	}{
		{name: "authorization", header: "aUtHoRiZaTiOn"},
		{name: "host", header: "hOsT"},
		{name: "x-amz", header: "X-AmZ-Date"},
	} {
		for _, value := range []*string{new("caller-reserved"), nil} {
			name := "null"
			if value != nil {
				name = "string"
			}
			t.Run(family.name+"/"+name, func(t *testing.T) {
				request := smithyhttp.NewStackRequest().(*smithyhttp.Request)
				request.Header.Set(family.header, "sdk-owned")
				middleware := bedrockHeadersMiddleware{headers: ProviderHeaders{family.header: value}}
				_, _, err := middleware.HandleBuild(context.Background(), smithymiddleware.BuildInput{Request: request}, smithymiddleware.BuildHandlerFunc(
					func(_ context.Context, input smithymiddleware.BuildInput) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
						got := input.Request.(*smithyhttp.Request).Header.Get(family.header)
						if got != "sdk-owned" {
							t.Fatalf("%s = %q, want SDK-owned value", family.header, got)
						}
						return smithymiddleware.BuildOutput{}, smithymiddleware.Metadata{}, nil
					},
				))
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestBedrockReservedHeadersCannotBeOverriddenOrDeleted(t *testing.T) {
	for _, test := range []struct {
		name  string
		value *string
	}{
		{name: "string", value: new("caller-reserved")},
		{name: "null", value: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests := rejectingProviderServer(t)
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			provider := NewBedrockProvider("anthropic.claude-test", server.URL)
			if _, err := provider.Stream(context.Background(), providerWireTranscript(), bedrockReservedHeaderOptions(test.value)); err == nil {
				t.Fatal("Stream error = nil, want test rejection")
			}
			request := <-requests
			assertPreparedProviderHeaders(t, request)
			assertBedrockReservedHeadersOwnedBySDK(t, request, server.URL)
		})
	}
}

func TestBedrockAppliesPreparedHeadersAndRequestEnvironment(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	provider := NewBedrockProvider("anthropic.claude-test", server.URL)
	options := requestHeaderOptions()
	options.Env = ProviderEnv{
		"AWS_BEDROCK_SKIP_AUTH": "1",
		"AWS_REGION":            "eu-test-1",
		"PI_CACHE_RETENTION":    "none",
	}
	if _, err := provider.Stream(context.Background(), providerWireTranscript(), options); err == nil {
		t.Fatal("Stream error = nil, want test rejection")
	}
	request := <-requests
	if got := request.header.Get("X-Configured"); got != "request" {
		t.Fatalf("X-Configured = %q, want request", got)
	}
	if got := request.header.Get("X-Request"); got != "yes" {
		t.Fatalf("X-Request = %q, want yes", got)
	}
	var body map[string]any
	if err := json.Unmarshal(request.body, &body); err != nil {
		t.Fatalf("decode Bedrock request: %v", err)
	}
	if _, exists := body["system"]; !exists {
		t.Fatalf("Bedrock request has no system prompt: %#v", body)
	}
	if strings.Contains(string(request.body), `"cachePoint"`) {
		t.Fatalf("request environment did not disable Bedrock cache retention: %s", request.body)
	}
}
