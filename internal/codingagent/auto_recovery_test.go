package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestIsContextOverflow(t *testing.T) {
	cases := []struct {
		name   string
		msg    *agent.AssistantMessage
		ctxWin int
		want   bool
	}{
		{"nil message", nil, 128000, false},
		{"normal stop", &agent.AssistantMessage{StopReason: "stop"}, 128000, false},
		{"anthropic overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "prompt is too long: 213462 tokens > 200000 maximum",
		}, 200000, true},
		{"openai overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Your input exceeds the context window of this model",
		}, 128000, true},
		{"openai max context length overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Requested token count exceeds the model's maximum context length of 131072 tokens",
		}, 128000, true},
		{"openai-compat parenthesized max context length overflow (#5677)", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Input length (265330) exceeds model's maximum context length (262144).",
		}, 262144, true},
		{"copilot overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "prompt token count of 150000 exceeds the limit of 128000",
		}, 128000, true},
		{"rate limit NOT overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "rate limit exceeded, too many tokens per minute",
		}, 128000, false},
		{"throttle NOT overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Throttling error: Too many tokens, please wait",
		}, 128000, false},
		{"silent overflow via usage", &agent.AssistantMessage{
			StopReason: "stop",
			Usage:      &ai.Usage{Input: 150000, CacheRead: 0},
		}, 128000, true},
		{"usage within window", &agent.AssistantMessage{
			StopReason: "stop",
			Usage:      &ai.Usage{Input: 100000, CacheRead: 0},
		}, 128000, false},
		{"cerebras no body", &agent.AssistantMessage{
			Provider: "cerebras", StopReason: "error", ErrorMessage: "413 status code (no body)",
		}, 128000, true},
		{"ollama overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "prompt too long; exceeded max context length by 5000 tokens",
		}, 128000, true},
		{"together overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "The input (500 tokens) is longer than the model's context length (128000 tokens).",
		}, 128000, true},
		{"openrouter/poolside max allowed input length overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Input length 200000 exceeds the maximum allowed input length of 128000 tokens.",
		}, 128000, true},
		{"DS4 configured context size overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Prompt has 5,958,968 tokens, but the configured context size is 256,000 tokens",
		}, 256000, true},
		{"DashScope input range overflow", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Range of input length should be [1, 131072]",
		}, 131072, true},
		{"length-stop overflow via filled context", &agent.AssistantMessage{
			StopReason: "length",
			Usage:      &ai.Usage{Input: 126800, CacheRead: 200, Output: 0},
		}, 128000, true},
		{"length-stop with room left is not overflow", &agent.AssistantMessage{
			StopReason: "length",
			Usage:      &ai.Usage{Input: 100000, CacheRead: 0, Output: 0},
		}, 128000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsContextOverflow(tc.msg, tc.ctxWin)
			if got != tc.want {
				t.Errorf("IsContextOverflow = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsRecoverableLength(t *testing.T) {
	cases := []struct {
		name             string
		stopReason       ai.StopReason
		outputTokens     int
		desiredMaxOutput int
		want             bool
	}{
		{name: "below desired output", stopReason: "length", outputTokens: 16, desiredMaxOutput: 128000, want: true},
		{name: "zero output", stopReason: "length", outputTokens: 0, desiredMaxOutput: 128000, want: true},
		{name: "reached desired output", stopReason: "length", outputTokens: 1024, desiredMaxOutput: 1024, want: false},
		{name: "normal stop", stopReason: "stop", outputTokens: 16, desiredMaxOutput: 128000, want: false},
		{name: "missing desired limit", stopReason: "length", outputTokens: 16, desiredMaxOutput: 0, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &agent.AssistantMessage{StopReason: tc.stopReason, Usage: &ai.Usage{Output: tc.outputTokens}}
			if got := IsRecoverableLength(msg, tc.desiredMaxOutput); got != tc.want {
				t.Fatalf("IsRecoverableLength() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	cases := []struct {
		name   string
		msg    *agent.AssistantMessage
		ctxWin int
		want   bool
	}{
		{"nil message", nil, 128000, false},
		{"normal stop", &agent.AssistantMessage{StopReason: "stop"}, 128000, false},
		{"rate limit", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Rate limit exceeded",
		}, 128000, true},
		{"429", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "HTTP 429: Too Many Requests",
		}, 128000, true},
		{"503", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "HTTP 503: Service Unavailable",
		}, 128000, true},
		{"overloaded", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "overloaded_error: The server is overloaded",
		}, 128000, true},
		{"connection refused", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "connection refused",
		}, 128000, true},
		{"connection reset by peer is not in pi-ai's retry patterns", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "github-copilot: request: read tcp 100.64.0.1:50817->140.82.112.22:443: read: connection reset by peer",
		}, 128000, false},
		{"websocket closed", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "websocket closed unexpectedly",
		}, 128000, true},
		{"stream ended before message_stop", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Anthropic stream ended before message_stop",
		}, 128000, true},
		{"cloudflare 524", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "HTTP 524: origin timeout",
		}, 128000, true},
		{"socket connection was closed", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "WebSocket error: socket connection was closed",
		}, 128000, true},
		{"stream ended before a terminal response event", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "stream ended before a terminal response event",
		}, 128000, true},
		{"grpc ResourceExhausted", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "rpc error: code = ResourceExhausted",
		}, 128000, true},
		{"timeout", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "request timed out",
		}, 128000, true},
		{"explicit retry guidance", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "please retry your request",
		}, 128000, true},
		{"overflow is NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "prompt is too long: 213462 tokens > 200000 maximum",
		}, 200000, false},
		{"generic error NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "Invalid API key",
		}, 128000, false},
		{"billing error NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "billing: payment required",
		}, 128000, false},
		{"quota exceeded NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "quota exceeded for this month",
		}, 128000, false},
		{"insufficient_quota NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "insufficient_quota: You have exceeded your usage",
		}, 128000, false},
		{"GoUsageLimitError NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "GoUsageLimitError: limit reached",
		}, 128000, false},
		{"FreeUsageLimitError NOT retryable", &agent.AssistantMessage{
			StopReason: "error", ErrorMessage: "FreeUsageLimitError: free tier",
		}, 128000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRetryableError(tc.msg, tc.ctxWin)
			if got != tc.want {
				t.Errorf("IsRetryableError = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLatestCompactionTimestampMs verifies the resume guard helper: it returns
// the timestamp of the most recent compaction on the active branch, which
// checkAutoRecovery uses to skip stale pre-compaction usage after a resume.
func TestLatestCompactionTimestampMs(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-lct", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// No compaction yet → not found.
	if _, ok := sess.LatestCompactionTimestampMs(); ok {
		t.Fatal("expected no compaction on a fresh session")
	}
	if _, err := sess.AppendMessage(mkUserMsg("hi")); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := sess.AppendMessage(mkAssistantMsg("hello")); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if _, ok := sess.LatestCompactionTimestampMs(); ok {
		t.Fatal("expected no compaction before one is appended")
	}

	// Append a compaction; helper returns its timestamp.
	before := time.Now().UnixMilli()
	if _, err := sess.AppendCompaction("summary", "", 1000, nil, false, nil); err != nil {
		t.Fatalf("append compaction: %v", err)
	}
	after := time.Now().UnixMilli()
	ts, ok := sess.LatestCompactionTimestampMs()
	if !ok {
		t.Fatal("expected a compaction timestamp")
	}
	if ts < before-2000 || ts > after+2000 {
		t.Fatalf("compaction ts %d outside [%d,%d]", ts, before, after)
	}

	// Messages appended after the compaction do not change the latest
	// compaction timestamp (it is still the most recent compaction on branch).
	if _, err := sess.AppendMessage(mkUserMsg("more")); err != nil {
		t.Fatalf("append user2: %v", err)
	}
	ts2, ok2 := sess.LatestCompactionTimestampMs()
	if !ok2 || ts2 != ts {
		t.Fatalf("latest compaction ts changed after later messages: got (%d,%v) want (%d,true)", ts2, ok2, ts)
	}
}
