package coding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// failureSeen is what a decider saw: the attempt, the error text and the failure response of the failed message.
type failureSeen struct {
	attempt, maxAttempts int
	errorMessage         string
	failure              *ai.FailureResponse
}

func recordingDecider(t *testing.T, decision ai.RetryDecision) func() []failureSeen {
	t.Helper()
	t.Cleanup(func() { icodingagent.SetDefaultRetryDecider(nil, false) })
	var mu sync.Mutex
	var seen []failureSeen
	icodingagent.SetDefaultRetryDecider(func(attempt, maxAttempts int, response ai.AssistantMessage) ai.RetryDecision {
		entry := failureSeen{attempt: attempt, maxAttempts: maxAttempts, errorMessage: response.ErrorMessage}
		if response.FailureResponse != nil {
			copied := *response.FailureResponse
			entry.failure = &copied
		}
		mu.Lock()
		seen = append(seen, entry)
		mu.Unlock()
		return decision
	}, true)
	return func() []failureSeen {
		mu.Lock()
		defer mu.Unlock()
		return append([]failureSeen(nil), seen...)
	}
}

// failedRequestServer answers every request with a 429 and a Retry-After header, or, for cut, with the first event of a stream and then a dropped connection.
func failedRequestServer(t *testing.T, cut bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !cut {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		chunk := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}]}\n\n"
		_, _ = fmt.Fprintf(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: %d\r\n\r\n%s", len(chunk)+1000, chunk)
		_ = buffered.Flush()
		_ = connection.Close()
	}))
	t.Cleanup(server.Close)
	return server
}

func sendFailingRequest(t *testing.T, cut bool) (events []agent.AgentEvent) {
	t.Helper()
	server := failedRequestServer(t, cut)
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL + "/v1", APIKey: "k", Model: "faux-1", ProviderID: "faux"})
	h := newRecoveryHarness(t, harnessOptions{provider: provider, settings: `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`})
	_, _ = h.session.Send(context.Background(), "hello")
	return h.settle(t)
}

func failedAssistantMessages(events []agent.AgentEvent) []*agent.AssistantMessage {
	var failed []*agent.AssistantMessage
	for _, event := range events {
		if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil && end.Message.Assistant.StopReason == ai.StopReasonError {
			failed = append(failed, end.Message.Assistant)
		}
	}
	return failed
}

// D654: with a decider installed, the status, the headers and the stream state of a real provider's failed response reach Decide through the session's
// request path; the session marks the request, the provider records the response, the stream builder attaches it.
func TestDeciderSeesTheFailedResponseOfARealProvider(t *testing.T) {
	seen := recordingDecider(t, ai.RetryDecision{})
	events := sendFailingRequest(t, false)
	got := seen()
	if len(got) != 1 || got[0].attempt != 1 || got[0].maxAttempts != 3 || !strings.Contains(got[0].errorMessage, "rate limited") {
		t.Fatalf("decider saw %+v, want one failure: attempt 1 of 3 with the provider's error text", got)
	}
	if failure := got[0].failure; failure == nil || failure.Status != 429 || failure.Headers["retry-after"] != "3" || failure.StreamStarted {
		t.Fatalf("failure response = %+v, want status 429, retry-after 3, stream not started", failure)
	}
	if failed := failedAssistantMessages(events); len(failed) != 1 {
		t.Fatalf("failed messages = %d, want 1 (the decider refused the retry)", len(failed))
	}
}

// D654: a stream that delivered an event and was then cut is StreamStarted, with no status.
func TestDeciderSeesAStreamThatWasCutAfterItStarted(t *testing.T) {
	seen := recordingDecider(t, ai.RetryDecision{})
	sendFailingRequest(t, true)
	got := seen()
	if len(got) != 1 || got[0].failure == nil || !got[0].failure.StreamStarted || got[0].failure.Status != 0 {
		t.Fatalf("decider saw %+v, want a failure with the stream started and no status", got)
	}
}

// D654: without a decider the failed message carries no response metadata: the session does not ask the request to keep it.
func TestStockSessionKeepsNoFailureResponseOnAFailedMessage(t *testing.T) {
	icodingagent.SetDefaultRetryDecider(nil, false)
	server := failedRequestServer(t, false)
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL + "/v1", APIKey: "k", Model: "faux-1", ProviderID: "faux"})
	h := newRecoveryHarness(t, harnessOptions{provider: provider, settings: `{"retry":{"enabled":false}}`})
	_, _ = h.session.Send(context.Background(), "hello")
	failed := failedAssistantMessages(h.settle(t))
	if len(failed) != 1 {
		t.Fatalf("failed messages = %d, want 1", len(failed))
	}
	if failed[0].FailureResponse != nil {
		t.Fatalf("FailureResponse = %+v, want nil without a decider", failed[0].FailureResponse)
	}
}

// D654: a compaction's summarization request is retried by the installed decider too: an error PiG's own rule never retries is retried when the decider says
// so, with the attempt and the retry budget of the settings.
func TestSummarizationRetriesPerTheInstalledDecider(t *testing.T) {
	seen := recordingDecider(t, ai.RetryDecision{Retry: true})
	s, p := retryCompactionSession(t, true, 3, 0, []string{"insufficient_quota", ""})
	_ = s.Events()
	result, err := s.Compact(t.Context(), "")
	if err != nil || !strings.Contains(result.Summary, "recovered summary") {
		t.Fatalf("result=%+v error=%v, want the retried summary", result, err)
	}
	if p.callCount() != 2 {
		t.Fatalf("summarization calls = %d, want 2 (insufficient_quota is not retried by PiG's own rule)", p.callCount())
	}
	got := seen()
	if len(got) != 1 || got[0].attempt != 1 || got[0].maxAttempts != 3 || !strings.Contains(got[0].errorMessage, "insufficient_quota") {
		t.Fatalf("decider saw %+v, want one summarization failure: attempt 1 of 3", got)
	}
}
