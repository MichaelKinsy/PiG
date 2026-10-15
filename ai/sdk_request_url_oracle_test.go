package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type sdkURLCase struct {
	API     string `json:"api"`
	BaseURL string `json:"baseURL"`
}

type sdkURLResult struct {
	URL   *string `json:"url"`
	Creds bool    `json:"creds"`
	Error *string `json:"error"`
}

func sdkURLCases(apis ...string) []sdkURLCase {
	random := rand.New(rand.NewSource(20261101))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var cases []sdkURLCase
	for _, api := range apis {
		for range 1500 {
			scheme := pick("https://", "https://", "http://", "HTTPS://", "https:/", "https:", "ftp://", "ws://", "file:///", "")
			authority := pick("", "", "", "u@", "u:p@", "u:p%40q@", ":p@", ":@", "@") +
				pick("api.test", "H.TEST", "h.test:443", "h.test:8080", "h.test:00443", "%65xample.test", "127.0.0.1", "[::1]:8443", "h.test.", "xn--nxasmq6b.test", "a b.test", "")
			if scheme == "" && strings.Contains(authority, ":") {
				// "host:port/path" is a URL with a scheme named host and an opaque path; fetch rejects the scheme, which the oracle does not model.
				continue
			}
			base := scheme + authority + pick("", "/", "/v1", "/v1/", "//v1///", "/a/../v1", "/a/./v1", "/a b/v1", "/v1/%2e%2e/x", "/%7Euser/v1", "/v1%2Fx", "/a%zz", "/é", "\\v1", "/v1\\x") +
				pick("", "", "", "", "?", "?a=1", "?a=1&b=2", "?a=b%20c", "?a=b+c", "?a=1&a=2", "?a=~x", "?alt=sse&x=%20", "?alt=x&alt=sse", "?x=1&alt=sse", "?7=x&b=y&3=z", "?x[]=1", "?é=ü", "?a", "?=v", "?&&a=1&&", "?a=%zz", "?q=1#f", "?s=!*'();:@,$/?") +
				pick("", "", "", "", "#", "#frag", "#a/b")
			cases = append(cases, sdkURLCase{API: api, BaseURL: base})
		}
		if api != "openrouter-images" {
			// With no base URL the openai SDK requests api.openai.com; an image model always carries its catalog base URL, and Pig does not send an OpenRouter key to another host.
			cases = append(cases, sdkURLCase{API: api, BaseURL: ""})
		}
		cases = append(cases, sdkURLCase{API: api, BaseURL: "not a url"}, sdkURLCase{API: api, BaseURL: "https://api.openai.com/v1"})
	}
	return cases
}

func runSDKURLOracle(t *testing.T, cases []sdkURLCase) []sdkURLResult {
	t.Helper()
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", filepath.Join(root, "ai", "testdata", "sdk_url_oracle.mjs"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi SDK oracle (npm ci --ignore-scripts in ai/testdata/sdk first): %v\n%.2000s", err, &stderr)
	}
	var results []sdkURLResult
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

// streamErrorText is the message a provider reports for a request that fails before or during the request: the error Stream returns, or the error message of the stream's result.
func streamErrorText(stream *AssistantMessageEventStream, err error) string {
	if err != nil {
		return err.Error()
	}
	for range stream.Events(context.Background()) {
	}
	return stream.Result().ErrorMessage
}

// recordingFetch answers 400 and stores the URL it was asked for.
func recordingFetch(seen *string) *http.Client {
	return &http.Client{Transport: FetchFunction(func(request *http.Request) (*http.Response, error) {
		*seen = request.URL.String()
		return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: request}, nil
	})}
}

// sdkAPIRequest sends one request through Pig's provider for api with baseURL and returns the error message the request reports.
func sdkAPIRequest(t *testing.T, api, baseURL string, fetch *http.Client) string {
	t.Helper()
	user := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}})
	switch api {
	case "openai-completions":
		stream, err := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", ProviderID: "oracle", BaseURL: baseURL}).Stream(t.Context(), user, StreamOptions{Fetch: fetch})
		return streamErrorText(stream, err)
	case "openai-responses":
		stream, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", ProviderID: "oracle", BaseURL: baseURL}).Stream(t.Context(), user, StreamOptions{Fetch: fetch})
		return streamErrorText(stream, err)
	case "anthropic-messages":
		stream, err := NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "claude-haiku-4-5", BaseURL: baseURL}).Stream(t.Context(), user, StreamOptions{Fetch: fetch})
		return streamErrorText(stream, err)
	case "pi-messages":
		stream, err := NewPiMessagesProvider(PiMessagesConfig{APIKey: "k", Model: "m", ProviderID: "oracle", BaseURL: baseURL}).Stream(t.Context(), user, StreamOptions{Fetch: fetch})
		return streamErrorText(stream, err)
	case "google-generative-ai":
		// The adapter refuses a custom fetch and requests through the global one, so the provider's own client records the request.
		provider := NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		if fetch != nil {
			provider.(*googleProvider).client = fetch
		}
		stream, err := provider.Stream(t.Context(), user, StreamOptions{})
		return streamErrorText(stream, err)
	case "openrouter-images":
		out := GenerateImagesOpenRouter(t.Context(), ImageModel{ID: "m", Provider: "openrouter", BaseURL: baseURL, Input: []string{"text"}, Output: []string{"image", "text"}},
			ImagesContext{Input: []ImagesInputContent{TextContent{Text: "hi"}}}, ImagesOptions{APIKey: "k", Fetch: fetch})
		return out.ErrorMessage
	}
	t.Fatalf("unknown api %s", api)
	return ""
}

// TestSDKProvidersRequestURLMatchesSDK compares the URL the openai and @anthropic-ai/sdk clients (pinned by Pi) build for a base URL, and what fetch does with it, with the URL Pig's openai-completions, openai-responses, openrouter-images and anthropic-messages providers request.
func TestSDKProvidersRequestURLMatchesSDK(t *testing.T) {
	apis := []string{"openai-completions", "openai-responses", "openrouter-images", "anthropic-messages", "google-generative-ai", "pi-messages"}
	cases := sdkURLCases(apis...)
	results := runSDKURLOracle(t, cases)
	differing, urls, refused, invalid := 0, 0, 0, 0
	for i, c := range cases {
		want := results[i]
		var seen string
		message := sdkAPIRequest(t, c.API, c.BaseURL, recordingFetch(&seen))
		var problem string
		switch {
		case want.Error != nil:
			invalid++
			if seen != "" || message != *want.Error {
				problem = "want error " + *want.Error
			}
		case want.URL == nil:
			problem = "oracle gave neither"
		default:
			urls++
			if seen != *want.URL {
				problem = "want URL " + *want.URL
			}
		}
		if problem == "" && want.Creds {
			refused++
			// Without a custom fetch, global fetch refuses the URL; the SDK reports its connection error.
			wantMessage := "Connection error."
			if c.API == "pi-messages" {
				wantMessage = "Request cannot be constructed from a URL that includes credentials: " + *want.URL
			}
			if got := sdkAPIRequest(t, c.API, c.BaseURL, nil); got != wantMessage {
				problem = "want global fetch to refuse credentials with " + wantMessage + ", got " + got
			}
		}
		if problem != "" {
			differing++
			if differing <= 12 {
				t.Errorf("%s %q: %s\n  got URL %q, error %q", c.API, c.BaseURL, problem, seen, message)
			}
		}
	}
	t.Logf("%d cases, %d URLs, %d refused for credentials, %d invalid, %d differing", len(cases), urls, refused, invalid, differing)
	if differing > 0 {
		t.Fatalf("%d cases differ from the SDK", differing)
	}
	if urls < 1000 || refused < 100 || invalid < 100 {
		t.Fatalf("probe too narrow: %d URLs, %d refused, %d invalid", urls, refused, invalid)
	}
}

// TestSDKProvidersRefuseCredentialsLikeFetch: global fetch refuses a URL with a username or password before it connects, so the server sees no request, and a custom fetch receives the URL as written.
func TestSDKProvidersRefuseCredentialsLikeFetch(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	base := strings.Replace(server.URL, "http://", "http://u:p@", 1) + "/v1"
	for _, api := range []string{"openai-completions", "openai-responses", "openrouter-images", "anthropic-messages"} {
		hits = 0
		if got := sdkAPIRequest(t, api, base, nil); got != "Connection error." || hits != 0 {
			t.Errorf("%s: error %q, %d requests reached the server; want Connection error. and none", api, got, hits)
		}
		var seen string
		sdkAPIRequest(t, api, base, recordingFetch(&seen))
		if !strings.HasPrefix(seen, base) {
			t.Errorf("%s: a custom fetch saw %q, want the URL with its credentials", api, seen)
		}
	}
	// @google/genai requests through the global fetch, whose TypeError names the whole URL.
	hits = 0
	want := "Request cannot be constructed from a URL that includes credentials: " + base + "/models/m:streamGenerateContent?alt=sse"
	if got := sdkAPIRequest(t, "google-generative-ai", base, nil); got != want || hits != 0 {
		t.Errorf("google-generative-ai: error %q, %d requests reached the server; want %q and none", got, hits, want)
	}
}

// TestPostClassifierRequestRefusesCredentialsLikeFetch: classifier-shared.ts:73 sends the target with `options.fetch ?? globalThis.fetch`, so global fetch refuses a URL with credentials before it connects, naming the URL, and a custom fetch receives it as written.
func TestPostClassifierRequestRefusesCredentialsLikeFetch(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	target := strings.Replace(server.URL, "http://", "http://u:p@", 1) + "/classify?x=1"
	model := ClassifierModel{ID: "m", Provider: "oracle"}
	_, err := PostClassifierRequest(t.Context(), "Label", target, model, map[string]any{}, ClassifierOptions{APIKey: "k"}, nil)
	if want := "Request cannot be constructed from a URL that includes credentials: " + target; err == nil || err.Error() != want || hits != 0 {
		t.Errorf("error %v, %d requests reached the server; want %q and none", err, hits, want)
	}
	var seen string
	_, _ = PostClassifierRequest(t.Context(), "Label", target, model, map[string]any{}, ClassifierOptions{APIKey: "k", Fetch: recordingFetch(&seen)}, nil)
	if seen != target {
		t.Errorf("a custom fetch saw %q, want %q", seen, target)
	}
}
