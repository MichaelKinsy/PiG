//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func bedrockSocketCodeAttempt(t *testing.T, provider ai.Provider, env ai.ProviderEnv) (ai.AssistantMessage, error) {
	t.Helper()
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{CacheRetention: ai.CacheRetentionNone, Env: env})
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	return *stream.Result(), nil
}

// Pi 4699b5b269 (#12136, #12128), the case its message confirms: the AWS SDK's Node HTTP handler passes the socket error through
// unchanged, so a Bedrock turn fails with the bare code, which retry.ts did not match. The shared retry loop now retries it: the call
// and the configured retries, then the error is the result.
func TestBedrockBareSocketCodeIsRetriedByTheAssistantRetryLoop(t *testing.T) {
	address := closedTCPAddress(t, "tcp4", "127.0.0.1:0")
	calls, scheduled, final := bedrockSocketCodeRetries(t, "http://"+address.String())
	want := "connect ECONNREFUSED " + address.String()
	if calls != 3 || !slices.Equal(scheduled, []string{want, want}) {
		t.Fatalf("calls = %d, retries scheduled for %q, want 3 calls and two retries of %q", calls, scheduled, want)
	}
	if final.StopReason != ai.StopReasonError || final.ErrorMessage != want {
		t.Fatalf("final = %s %q, want the surfaced error %q", final.StopReason, final.ErrorMessage, want)
	}
}

// bedrockSocketCodeRetries runs the shared retry loop over a provider whose endpoint is baseURL: the calls made, the error message of
// each retry scheduled, and the final message.
func bedrockSocketCodeRetries(t *testing.T, baseURL string) (calls int, scheduled []string, final ai.AssistantMessage) {
	t.Helper()
	isolateBedrockConfig(t)
	env := ai.ProviderEnv{"AWS_BEDROCK_FORCE_HTTP1": "1", "AWS_BEDROCK_SKIP_AUTH": "1"}
	provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", baseURL)
	final, err := ai.RetryAssistantCall(t.Context(), func() (ai.AssistantMessage, error) {
		calls++
		return bedrockSocketCodeAttempt(t, provider, env)
	}, &ai.RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 1}, ai.RetryCallbacks{OnRetryScheduled: func(_, _, _ int, errorMessage string) error {
		scheduled = append(scheduled, errorMessage)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return calls, scheduled, final
}

// A peer that resets every connection after it read the request, before any response: the call and the configured retries, then the
// reset is the result.
func TestBedrockConnectionResetBeforeTheResponseIsRetried(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	calls, scheduled, final := bedrockSocketCodeRetries(t, server.URL)
	t.Logf("calls=%d requests=%d scheduled=%q final=%s %q", calls, requests.Load(), scheduled, final.StopReason, final.ErrorMessage)
	if calls != 3 || len(scheduled) != 2 || final.StopReason != ai.StopReasonError || !ai.IsRetryableAssistantError(final) {
		t.Fatalf("calls = %d, retries scheduled for %q, final %s %q: want 3 calls, two retries and the reset surfaced", calls, scheduled, final.StopReason, final.ErrorMessage)
	}
}

// What is not retried: a 400 whose message names a socket code inside a longer identifier (retry.test.ts "Invalid value EXECONNRESET").
func TestBedrockBadRequestThatNamesASocketCodeInsideAnIdentifierIsNotRetried(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Amzn-Errortype", "ValidationException")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"message":"Invalid value EXECONNRESET"}`))
	}))
	defer server.Close()
	calls, scheduled, final := bedrockSocketCodeRetries(t, server.URL)
	if calls != 1 || requests.Load() != 1 || len(scheduled) != 0 || final.StopReason != ai.StopReasonError || final.ErrorMessage != "Validation error: Invalid value EXECONNRESET" {
		t.Fatalf("calls = %d, requests = %d, retries %q, final %s %q: want one request and the 400 surfaced", calls, requests.Load(), scheduled, final.StopReason, final.ErrorMessage)
	}
}
