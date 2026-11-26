package coding

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi fb62c069b, 25190b72c, a00a5048d: the session's turn retry path
// (coding/session.go -> session_recovery.go -> auto_recovery.go -> ai.IsRetryableAssistantError)
// retries Cloudflare 525, gateway temporary-unavailable/partial-response, and admission-ceiling errors.
func TestSessionRetriesPiMainRetryPatterns(t *testing.T) {
	for _, tc := range []struct {
		name         string
		errorMessage string
	}{
		{"Pi fb62c069b Cloudflare 525", "525 <!DOCTYPE html><title>SSL handshake failed</title>"},
		{"Pi 25190b72c gateway provider temporarily unavailable", "The provider is temporarily unavailable. Retry shortly."},
		{"Pi a00a5048d admission ceiling", "waited 73.4s in queue, reaching this model's admission ceiling of 60s; retry after the hinted delay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := func([]ai.Message) *ai.AssistantMessage {
				return &ai.AssistantMessage{
					Provider:     "faux",
					Model:        "faux-1",
					StopReason:   ai.StopReasonError,
					ErrorMessage: tc.errorMessage,
					Timestamp:    time.Now().UnixMilli(),
				}
			}
			recovered := fauxReply("recovered", ai.StopReasonStop, 0)
			h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":0}}`, extension.Extension{}, nil, failed, recovered)
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")

			if got := h.provider.callCount(); got != 2 {
				t.Fatalf("provider calls = %d, want 2", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
				t.Fatalf("retry events = %v, want [start:1 end:true]", got)
			}
			if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != tc.errorMessage {
				t.Fatalf("auto_retry_start = %#v, want error %q", starts, tc.errorMessage)
			}
			if text := h.session.LastAssistantText(); text == nil || *text != "recovered" {
				t.Fatalf("last assistant text = %v, want recovered", text)
			}
		})
	}
}

// Overlap precedence (Pi a00a5048d vs agent-session.ts _isRetryableError): an Anthropic streaming
// api_error whose message is a context overflow or HTTP 413 Request too large matches
// ai.IsRetryableAssistantError ("type":"api_error"), while codingagent.IsRetryableError returns
// false because context overflow takes precedence. Tests the classifier predicates directly.
func TestApiErrorOverlapContextOverflowAnd413NotRetryableByPredicate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		errorMessage string
	}{
		{
			name:         "api_error with prompt too long overflow",
			errorMessage: `{"type":"error","error":{"type":"api_error","message":"prompt is too long: 213462 tokens > 200000 maximum"}}`,
		},
		{
			name:         "413 with api_error Request too large",
			errorMessage: `413 {"type":"error","error":{"type":"api_error","message":"Request too large"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &agent.AssistantMessage{
				Provider:     "anthropic",
				ModelID:      "claude-sonnet-4-20250514",
				StopReason:   ai.StopReasonError,
				ErrorMessage: tc.errorMessage,
				Timestamp:    time.Now().UnixMilli(),
			}
			if !ai.IsRetryableAssistantError(msg.LLMMessage()) {
				t.Fatalf("ai.IsRetryableAssistantError(%q) = false, want true", tc.errorMessage)
			}
			if !codingagent.IsContextOverflow(msg, 200000) {
				t.Fatalf("codingagent.IsContextOverflow(%q) = false, want true", tc.errorMessage)
			}
			if codingagent.IsRetryableError(msg, 200000) {
				t.Fatalf("codingagent.IsRetryableError(%q) = true, want false (overflow wins)", tc.errorMessage)
			}
		})
	}
}

// Real Anthropic provider path (Pi a00a5048d): an Anthropic streaming api_error event whose
// message text matches no other pattern is retried through ai.NewAnthropicProvider and the session loop.
func TestSessionRetriesAnthropicStreamingAPIErrorThroughRealProvider(t *testing.T) {
	const apiErrorEvent = `{"type":"error","error":{"details":null,"type":"api_error","message":"Unable to complete this request right now. Please try again."},"request_id":"req_011CgfE1bTgPTDsor4TsApFv"}`
	api := closedConnectionAPI{
		name: "anthropic API key streaming api_error",
		provider: func(u string) ai.Provider {
			return ai.NewAnthropicProvider(ai.AnthropicConfig{BaseURL: u, APIKey: "test-key", Model: "claude-sonnet-4-20250514"})
		},
		dropBody:  "event: error\ndata: " + apiErrorEvent + "\n\n",
		wantError: apiErrorEvent,
		okBody:    closedAnthropicOK,
	}
	server, requests := closedConnectionServer(t, api, 1)
	h := closedConnectionHarness(t, api, server, retryEventsSettings)
	log := recordSessionEvents(h.session)
	retryPrompt(t, h, "hello")

	if got := requests.Load(); got != 2 {
		t.Fatalf("server requests = %d, want 2 (one api_error failure, one retry)", got)
	}
	if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
		t.Fatalf("retry events = %v, want [start:1 end:true]", got)
	}
	if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != apiErrorEvent {
		t.Fatalf("auto_retry_start = %#v, want error %q", starts, apiErrorEvent)
	}
	if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
		t.Fatalf("last assistant text = %v, want recovered after retry", text)
	}
}
