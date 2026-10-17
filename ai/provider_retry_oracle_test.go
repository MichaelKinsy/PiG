//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type retryAttempt struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

type retryCase struct {
	Chunks     []string       `json:"chunks"`
	Status     int            `json:"status"`
	Attempts   []retryAttempt `json:"attempts"`
	MaxRetries int            `json:"maxRetries"`
}

// providerRetryCases returns seeded failing-attempt sequences (retryable and not, with retry hints) before a successful stream. Delays stay short: every case with a retryable status carries retry-after-ms or retry-after hints except a few that use the SDK's own backoff.
func providerRetryCases(success []byte) []retryCase {
	random := rand.New(rand.NewSource(20261110))
	statuses := []int{400, 401, 408, 409, 429, 500, 502, 503, 529}
	var cases []retryCase
	for i := range 36 {
		c := retryCase{Chunks: []string{base64.StdEncoding.EncodeToString(success)}, Status: http.StatusOK, MaxRetries: random.Intn(4)}
		for range random.Intn(4) {
			status := statuses[random.Intn(len(statuses))]
			text := http.StatusText(status)
			if text == "" {
				text = fmt.Sprintf("status code %d", status)
			}
			attempt := retryAttempt{Status: status, StatusText: text, Headers: map[string]string{}, Body: `{"error":{"message":"try later"}}`}
			switch random.Intn(6) {
			case 0:
				attempt.Headers["retry-after-ms"] = "1"
			case 1:
				attempt.Headers["retry-after"] = "0"
			case 2:
				attempt.Headers["retry-after-ms"] = "1"
				attempt.Headers["x-should-retry"] = "false"
			case 3:
				attempt.Headers["retry-after-ms"] = "1"
				attempt.Headers["x-should-retry"] = "true"
			case 4:
				attempt.Headers["retry-after-ms"] = "1"
			default:
				if i%9 != 0 {
					attempt.Headers["retry-after-ms"] = "2"
				}
			}
			c.Attempts = append(c.Attempts, attempt)
		}
		cases = append(cases, c)
	}
	return cases
}

func providerRetryOracle(t *testing.T, pi [4]string, success []byte, newProvider func(baseURL string) Provider) {
	t.Helper()
	cases := providerRetryCases(success)
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/provider_stream_oracle.mjs", pigversion.UpstreamPackageVersion, pi[0], pi[1], pi[2], pi[3])
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	var expected []struct {
		Requests int      `json:"requests"`
		Events   []string `json:"events"`
		Message  struct {
			StopReason   string  `json:"stopReason"`
			ErrorMessage *string `json:"errorMessage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	var counts [64]atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var index int
		_, _ = fmt.Sscanf(r.URL.Path, "/case/%d/", &index)
		n := int(counts[index].Add(1)) - 1
		if n < len(cases[index].Attempts) {
			attempt := cases[index].Attempts[n]
			w.Header().Set("Content-Type", "application/json")
			for k, v := range attempt.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(attempt.Status)
			_, _ = w.Write([]byte(attempt.Body))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		raw, _ := base64.StdEncoding.DecodeString(cases[index].Chunks[0])
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	user := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}})
	for i, c := range cases {
		provider := newProvider(fmt.Sprintf("%s/case/%d", server.URL, i))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		stream, err := provider.Stream(ctx, user, StreamOptions{MaxRetries: new(c.MaxRetries)})
		var events []string
		var stop, message string
		if err != nil {
			events, stop, message = []string{"error"}, "error", err.Error()
		} else {
			for event := range stream.Events(ctx) {
				events = append(events, string(event.EventType()))
			}
			result := stream.Result()
			stop, message = string(result.StopReason), result.ErrorMessage
		}
		cancel()
		want := expected[i]
		wantMessage := ""
		if want.Message.ErrorMessage != nil {
			wantMessage = *want.Message.ErrorMessage
		}
		got := fmt.Sprintf("requests=%d events=%v stop=%s error=%q", counts[i].Load(), events, stop, message)
		wantText := fmt.Sprintf("requests=%d events=%v stop=%s error=%q", want.Requests, want.Events, want.Message.StopReason, wantMessage)
		if got != wantText {
			t.Errorf("case %d (maxRetries %d, attempts %s):\n  got  %s\n  want %s", i, c.MaxRetries, strconv.Quote(attemptSummary(c.Attempts)), got, wantText)
		}
	}
}

func attemptSummary(attempts []retryAttempt) string {
	var out string
	for _, a := range attempts {
		out += fmt.Sprintf("%d%v ", a.Status, a.Headers)
	}
	return out
}

func TestProviderRetriesMatchPi(t *testing.T) {
	for _, test := range []struct {
		name    string
		pi      [4]string
		success string
		make    func(baseURL string) Provider
	}{
		{"openai-completions", [4]string{"openai-completions", "openAICompletionsApi", "openai-completions", "openai"}, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", func(baseURL string) Provider {
			return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"openai-responses", [4]string{"openai-responses", "openAIResponsesApi", "openai-responses", "openai"}, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n", func(baseURL string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"anthropic", [4]string{"anthropic-messages", "anthropicMessagesApi", "anthropic-messages", "anthropic"}, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", func(baseURL string) Provider {
			return NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"google", [4]string{"google-generative-ai", "googleGenerativeAIApi", "google-generative-ai", "google"}, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n", func(baseURL string) Provider {
			return NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"mistral", [4]string{"mistral-conversations", "mistralConversationsApi", "mistral-conversations", "mistral"}, "data: {\"id\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", func(baseURL string) Provider {
			return NewMistralProvider(MistralConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
	} {
		t.Run(test.name, func(t *testing.T) { providerRetryOracle(t, test.pi, []byte(test.success), test.make) })
	}
}
