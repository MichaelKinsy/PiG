package ai

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// error-body.test.ts "surfaces the reason from a real openai SDK connection failure" against the pinned pi-ai, then the same closed local port
// through the three stream APIs (openai-completions, openai-responses, anthropic-messages): each keeps `Connection error.: connect ECONNREFUSED <addr>`.
func TestConnectionErrorKeepsCauseThroughSDKsMatchesPi(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	port := address[len("127.0.0.1:"):]
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.CommandContext(t.Context(), "node", "testdata/connection_cause.mjs", pigversion.UpstreamPackageVersion, port).Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	var pi struct {
		SDK  string `json:"sdk"`
		APIs map[string]struct {
			Stop  string `json:"stop"`
			Error string `json:"error"`
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	pi.APIs = map[string]struct {
		Stop  string `json:"stop"`
		Error string `json:"error"`
	}{}
	for key, value := range fields {
		if key == "sdk" {
			if err := json.Unmarshal(value, &pi.SDK); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var entry struct {
			Stop  string `json:"stop"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(value, &entry); err != nil {
			t.Fatal(err)
		}
		pi.APIs[key] = entry
	}

	// The OpenAI SDK's own failure, as Pi's test builds it: a closed port through the fetch boundary, then the SDK's rejection mapping.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, fetchErr := (&nodeFetchTransport{base: http.DefaultTransport}).RoundTrip(request)
	sdkErr := openAISDKTransportError(t.Context(), fetchErr)
	if sdkErr == nil {
		t.Fatalf("no SDK error for %v", fetchErr)
	}
	if got := FormatProviderError(NormalizeProviderError(sdkErr)); got != pi.SDK {
		t.Errorf("SDK error = %q, Pi %q", got, pi.SDK)
	}

	for _, c := range []struct {
		api     API
		streams *ProviderStreams
		base    string
	}{{APIOpenAICompletions, OpenAICompletionsAPI(), "/v1"}, {APIOpenAIResponses, OpenAIResponsesAPI(), "/v1"}, {APIAnthropicMessages, AnthropicMessagesAPI(), ""}} {
		model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: c.api, ProviderID: "acme", BaseURL: "http://" + address + c.base}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
		stream, err := c.streams.Stream(context.Background(), model, newTranscriptContext([]Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}), StreamOptions{APIKey: "k"})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(t.Context()) {
		}
		result := stream.Result()
		want := pi.APIs[string(c.api)]
		if string(result.StopReason) != want.Stop || result.ErrorMessage != want.Error {
			t.Errorf("%s: stop=%q error=%q, Pi stop=%q error=%q", c.api, result.StopReason, result.ErrorMessage, want.Stop, want.Error)
		}
	}
}

// Node's fetch refuses the Fetch standard's blocked ports without connecting (`fetch failed: bad port`); every other port connects.
func TestFetchRefusesBadPortsLikeNode(t *testing.T) {
	for _, port := range []int{1, 7, 25, 6000, 6669, 10080} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&nodeFetchTransport{base: http.DefaultTransport, connectionIdle: true}).RoundTrip(request)
		if err == nil || FormatProviderError(NormalizeProviderError(err)) != "fetch failed: bad port" {
			t.Errorf("port %d: %v", port, err)
		}
	}
	for _, port := range []int{2, 8, 6670, 10081} {
		u, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		if isFetchBadPort(u) {
			t.Errorf("port %d is not blocked", port)
		}
	}
}
