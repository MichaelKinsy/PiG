package ai

// Serialized-request tests for PiG's default provider User-Agent header
// (D65, owner decision delivery/OWNER-DECISIONS.md Q4): the exact default
// value, upstream-specific configured-header precedence, and format. Ordinary
// providers allow a configured override; Codex forces the product identity.
// Anthropic's own default/override/OAuth-exception cases (Q3: OAuth requests
// keep claude-cli/2.1.280 regardless of a configured header) are
// TestAnthropicClientUserAgent in anthropic_oauth_test.go. This file covers
// openai-completions, openai-responses (and its azure/codex delegates),
// google-generative-ai (and its vertex delegate), and mistral-conversations.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func TestPiUserAgentFormat(t *testing.T) {
	got := PiUserAgent()
	wantPrefix := "pig/" + pigversion.Version + " ("
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("PiUserAgent() = %q, want prefix %q", got, wantPrefix)
	}
	// pig/<version> (<platform> <release>; <arch>), mirroring upstream's
	// "pi (<platform> <release>; <arch>)" shape with PiG's product name.
	if !regexp.MustCompile(`^pig/\S+ \([^;()]+; [^()]+\)$`).MatchString(got) {
		t.Fatalf("PiUserAgent() = %q, want pig/<version> (<platform> <release>; <arch>)", got)
	}

	// Upstream's AI helper reads node:os. Obtain its machine identity independently of PiG's mappings; D65 changes only the product identity on this surface.
	t.Setenv("NODE_OPTIONS", "")
	machine, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `import os from "node:os"; process.stdout.write(" (" + os.platform() + " " + os.release() + "; " + os.arch() + ")");`).Output()
	if err != nil {
		t.Fatalf("read Node OS identity: %v", err)
	}
	if want := "pig/" + pigversion.Version + string(machine); got != want {
		t.Fatalf("PiUserAgent() = %q, want independent Node OS identity %q", got, want)
	}
}

// TestProductVersionAvailableWithoutMain is the regression guard for library
// consumers, which do not run cmd/pig's main function.
func TestProductVersionAvailableWithoutMain(t *testing.T) {
	if ProductVersion != pigversion.Version {
		t.Fatalf("ProductVersion = %q, want %q", ProductVersion, pigversion.Version)
	}
	if got, want := PiUserAgent(), "pig/"+pigversion.Version+" ("; !strings.HasPrefix(got, want) {
		t.Fatalf("PiUserAgent() = %q, want prefix %q", got, want)
	}
}

// userAgentTestRoundTripperFunc lets a test capture the outgoing request and
// answer with a canned response, without a real socket.
type userAgentTestRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f userAgentTestRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// sseClient returns an *http.Client whose RoundTrip captures the request
// header into captured and answers with sse as an SSE body.
func sseClient(captured *http.Header, sse string) *http.Client {
	return &http.Client{Transport: userAgentTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		*captured = request.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})}
}

// drainProviderStream runs a Stream to completion, failing the test on setup
// or stream error so callers only need to assert on the captured header.
func drainProviderStream(t *testing.T, provider Provider, opts StreamOptions) {
	t.Helper()
	stream, err := provider.Stream(context.Background(), userAgentTestTranscript, opts)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	if result := stream.Result(); result.StopReason == StopReasonError {
		t.Fatalf("stream error: %s", result.ErrorMessage)
	}
}

var userAgentTestTranscript = NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})

const (
	userAgentOpenAICompletionsSSE = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n"
	userAgentOpenAIResponsesSSE   = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	userAgentGoogleSSE            = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n"
	userAgentMistralSSE           = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n"
)

// userAgentProviderCase builds one Provider wired to an injected client, so
// the test can assert on the exact request header without a real socket.
type userAgentProviderCase struct {
	name  string
	build func(client *http.Client) Provider
	sse   string
}

// userAgentProviderCases covers every upstream getPiUserAgent() call site
// under packages/ai/src/api/ except anthropic-messages.ts (covered in
// anthropic_oauth_test.go): openai-completions.ts, openai-responses.ts (whose
// azure-openai-responses.ts and openai-codex-responses.ts wrappers delegate
// to the same Go request builder), google-generative-ai.ts (whose
// google-vertex.ts wrapper delegates to the same Go request builder), and
// mistral-conversations.ts.
func userAgentProviderCases(t *testing.T) []userAgentProviderCase {
	t.Helper()
	return []userAgentProviderCase{
		{
			name: "openai-completions",
			build: func(client *http.Client) Provider {
				p, ok := NewOpenAIProvider(OpenAIConfig{BaseURL: "https://example.test/v1", Model: "gpt-4o"}).(*openAIProvider)
				if !ok {
					t.Fatal("NewOpenAIProvider did not return *openAIProvider")
				}
				p.client = client
				return p
			},
			sse: userAgentOpenAICompletionsSSE,
		},
		{
			name: "openai-responses",
			build: func(client *http.Client) Provider {
				p, ok := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: "https://example.test/v1", Model: "gpt-5"}).(*openAIResponsesProvider)
				if !ok {
					t.Fatal("NewOpenAIResponsesProvider did not return *openAIResponsesProvider")
				}
				p.client = client
				return p
			},
			sse: userAgentOpenAIResponsesSSE,
		},
		{
			name: "azure-openai-responses",
			build: func(client *http.Client) Provider {
				p, ok := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{
					APIKey: "azure-key", Model: "gpt-5", BaseURL: "https://res.openai.azure.com/openai/v1",
				}).(*openAIResponsesProvider)
				if !ok {
					t.Fatal("NewAzureOpenAIResponsesProvider did not return *openAIResponsesProvider")
				}
				p.client = client
				return p
			},
			sse: userAgentOpenAIResponsesSSE,
		},
		{
			name: "openai-codex-responses",
			build: func(client *http.Client) Provider {
				p, ok := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestTokenForAccount("user-agent"), Model: "gpt-5"}).(*openAIResponsesProvider)
				if !ok {
					t.Fatal("NewOpenAICodexResponsesProvider did not return *openAIResponsesProvider")
				}
				p.client = client
				return p
			},
			sse: userAgentOpenAIResponsesSSE,
		},
		{
			name: "google-generative-ai",
			build: func(client *http.Client) Provider {
				p, ok := NewGoogleProvider(GoogleConfig{APIKey: "key", Model: "gemini-2.5-flash", BaseURL: "https://example.test"}).(*googleProvider)
				if !ok {
					t.Fatal("NewGoogleProvider did not return *googleProvider")
				}
				p.client = client
				return p
			},
			sse: userAgentGoogleSSE,
		},
		{
			name: "google-vertex",
			build: func(client *http.Client) Provider {
				return newGoogleVertexTestProvider(GoogleVertexConfig{
					APIKey: "key", Model: "gemini-2.5-flash", Project: "proj", Location: "us-central1",
				}, client)
			},
			sse: userAgentGoogleSSE,
		},
		{
			name: "mistral-conversations",
			build: func(client *http.Client) Provider {
				return newMistralTestProvider(MistralConfig{APIKey: "key", Model: "mistral-large-latest", BaseURL: "https://example.test/v1"}, client)
			},
			sse: userAgentMistralSSE,
		},
	}
}

// TestProviderDefaultUserAgent proves every non-Anthropic HTTP provider
// stamps PiUserAgent() as its default User-Agent, mirroring upstream's
// `{ "User-Agent": getPiUserAgent(), ...model.headers }` seed.
func TestProviderDefaultUserAgent(t *testing.T) {
	want := PiUserAgent()
	for _, tc := range userAgentProviderCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			var header http.Header
			provider := tc.build(sseClient(&header, tc.sse))
			opts := StreamOptions{}
			if provider.ID() == string(APIOpenAICodexResponses) {
				opts.Transport = TransportSSE
			}
			drainProviderStream(t, provider, opts)
			if got := header.Get("User-Agent"); got != want {
				t.Errorf("User-Agent = %q, want %q", got, want)
			}
		})
	}
}

// TestProviderUserAgentOverridePrecedence proves every provider preserves
// upstream's model/request header precedence. Since Pi 1.1.0 Codex sets its
// product identity first as well (buildBaseCodexHeaders, #10429).
func TestProviderUserAgentOverridePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		build         func(client *http.Client, extraHeaders map[string]string) Provider
		sse           string
		optionsHeader ProviderHeaders
	}{
		{
			name: "openai-completions model header",
			build: func(client *http.Client, extraHeaders map[string]string) Provider {
				p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://example.test/v1", Model: "gpt-4o", ExtraHeaders: extraHeaders}, client: client}
				return p
			},
			sse: userAgentOpenAICompletionsSSE,
		},
		{
			name: "openai-responses request header outranks model header",
			build: func(client *http.Client, extraHeaders map[string]string) Provider {
				p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{BaseURL: "https://example.test/v1", Model: "gpt-5", ExtraHeaders: extraHeaders}, client: client}
				return p
			},
			sse:           userAgentOpenAIResponsesSSE,
			optionsHeader: ProviderHeaders{"User-Agent": new("request-client")},
		},
		{
			name: "openai-codex-responses request header outranks model header",
			build: func(client *http.Client, extraHeaders map[string]string) Provider {
				p, ok := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestTokenForAccount("user-agent"), Model: "gpt-5"}).(*openAIResponsesProvider)
				if !ok {
					t.Fatal("NewOpenAICodexResponsesProvider did not return *openAIResponsesProvider")
				}
				p.cfg.ExtraHeaders = extraHeaders
				p.client = client
				return p
			},
			sse:           userAgentOpenAIResponsesSSE,
			optionsHeader: ProviderHeaders{"User-Agent": new("request-client")},
		},
		{
			name: "google-generative-ai model header",
			build: func(client *http.Client, extraHeaders map[string]string) Provider {
				p := &googleProvider{cfg: GoogleConfig{BaseURL: "https://example.test", APIKey: "key", Model: "gemini-2.5-flash", ExtraHeaders: extraHeaders}, client: client}
				return p
			},
			sse: userAgentGoogleSSE,
		},
		{
			name: "mistral-conversations model header",
			build: func(client *http.Client, extraHeaders map[string]string) Provider {
				return newMistralTestProvider(MistralConfig{BaseURL: "https://example.test/v1", APIKey: "key", Model: "mistral-large-latest", ExtraHeaders: extraHeaders}, client)
			},
			sse: userAgentMistralSSE,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var header http.Header
			provider := tc.build(sseClient(&header, tc.sse), map[string]string{"User-Agent": "model-client"})
			opts := StreamOptions{}
			if provider.ID() == string(APIOpenAICodexResponses) {
				opts.Transport = TransportSSE
			}
			want := "model-client"
			if tc.optionsHeader != nil {
				opts.Headers = tc.optionsHeader
				want = "request-client"
			}
			drainProviderStream(t, provider, opts)
			if got := header.Get("User-Agent"); got != want {
				t.Errorf("User-Agent = %q, want %q", got, want)
			}
		})
	}
}

// openai-codex-stream.test.ts "lets model and caller headers override originator and User-Agent" (#10429): the identity headers are defaults, Authorization is not.
func TestCodexModelAndCallerHeadersOverrideOriginatorAndUserAgent(t *testing.T) {
	token := codexTestTokenForAccount("override")
	var header http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(userAgentOpenAIResponsesSSE))
	}))
	t.Cleanup(server.Close)
	// The model's headers reach the provider the way Pi's streamOpenAICodexResponses(model, ...) reads model.headers.
	model := cloneGeneratedModel(t, "openai-codex/gpt-5.5").ToModel()
	model.ProviderMeta.BaseURL = server.URL
	model.ProviderMeta.Headers = map[string]string{"Originator": "my-app"}
	stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Say hello")}}}), StreamOptions{
		APIKey: token, Transport: TransportSSE, Headers: ProviderHeaders{"user-agent": new("my-app/1.0"), "Authorization": new("Bearer ignored")},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	if got := header.Get("originator"); got != "my-app" {
		t.Errorf("originator = %q, want the model header", got)
	}
	if got := header.Get("User-Agent"); got != "my-app/1.0" {
		t.Errorf("User-Agent = %q, want the caller header", got)
	}
	if got := header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization = %q, want the token", got)
	}
	// The WebSocket handshake builds the same base headers.
	ws := codexWebSocketHeaders(map[string]string{"originator": "my-app"}, ProviderHeaders{"user-agent": new("my-app/1.0")}, "token", "acct", "request")
	if ws.Get("originator") != "my-app" || ws.Get("User-Agent") != "my-app/1.0" {
		t.Errorf("websocket originator = %q, User-Agent = %q", ws.Get("originator"), ws.Get("User-Agent"))
	}
	if defaults := codexWebSocketHeaders(nil, nil, "token", "acct", "request"); defaults.Get("User-Agent") != PiUserAgent() || defaults.Get("originator") != pigidentity.CodexOriginator {
		t.Errorf("default websocket identity = %q / %q", defaults.Get("originator"), defaults.Get("User-Agent"))
	}
}
