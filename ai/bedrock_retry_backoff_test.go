//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
)

// Pi's Bedrock client retries with @smithy/core's StandardRetryStrategy (bundle chunk bedrock-converse-stream.js): delay =
// floor(Math.random() * min(base * 2^retryCount, 20000)) ms, base 500 for a throttling error (status 429 or a THROTTLING_ERROR_CODES
// name) and 100 otherwise, then max(delay, min(retryAfterHint - now, delay + 5000)). The Go SDK's default waited random * 2^attempt s.
func TestBedrockRetryBackoffIsTheJavaScriptSDKs(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	backoff := bedrockSDKBackoff{random: func() float64 { return 0.5 }, now: func() time.Time { return now }}
	response := func(status int, header http.Header, code string) error {
		var err error = &smithy.GenericAPIError{Code: code}
		return &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status, Header: header}}, Err: err}
	}
	for _, tc := range []struct {
		name    string
		attempt int
		err     error
		want    time.Duration
	}{
		{"server error, first retry", 1, response(503, nil, "ServiceUnavailableException"), 50 * time.Millisecond},
		{"server error, second retry", 2, response(503, nil, "ServiceUnavailableException"), 100 * time.Millisecond},
		{"429 throttles", 1, response(429, nil, "Unknown"), 250 * time.Millisecond},
		{"throttling name", 2, response(400, nil, "ThrottlingException"), 500 * time.Millisecond},
		{"cap", 20, response(503, nil, ""), 10 * time.Second},
		{"retry-after seconds within slack", 1, response(503, http.Header{"Retry-After": {"2"}}, ""), 2 * time.Second},
		{"retry-after capped at delay plus 5 s", 1, response(503, http.Header{"Retry-After": {"120"}}, ""), 5050 * time.Millisecond},
		{"retry-after below the delay", 1, response(429, http.Header{"Retry-After": {"0.1"}}, ""), 250 * time.Millisecond},
		{"retry-after HTTP date", 1, response(503, http.Header{"Retry-After": {"Sun, 11 Oct 2026 00:00:03 GMT"}}, ""), 3 * time.Second},
		{"x-amz-retry-after ms", 1, response(503, http.Header{"X-Amz-Retry-After": {"1500"}}, ""), 1500 * time.Millisecond},
		{"unparsable retry-after", 1, response(503, http.Header{"Retry-After": {"soon"}}, ""), 50 * time.Millisecond},
		{"no response", 1, errors.New("connect ECONNREFUSED"), 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backoff.BackoffDelay(tc.attempt, tc.err)
			if err != nil || got != tc.want {
				t.Fatalf("BackoffDelay(%d) = %v, %v; want %v", tc.attempt, got, err, tc.want)
			}
		})
	}
}

// The provider path: three attempts against a 503 wait at most 100 ms and 200 ms with the JavaScript SDK's delay. The Go SDK's default
// waits up to 2 s and 4 s; at a jitter of 0.999 that is about 6 s.
func TestBedrockProviderWaitsTheJavaScriptSDKsRetryDelay(t *testing.T) {
	before := bedrockRetryRandom
	t.Cleanup(func() { bedrockRetryRandom = before })
	bedrockRetryRandom = func() float64 { return 0.999 }
	var mu sync.Mutex
	var arrivals []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"busy"}`))
	}))
	defer server.Close()
	provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_BEDROCK_FORCE_HTTP1": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonError {
		t.Fatalf("result = %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 3 {
		t.Fatalf("attempts = %d, want 3", len(arrivals))
	}
	if gap := arrivals[2].Sub(arrivals[0]); gap > 1500*time.Millisecond {
		t.Fatalf("three attempts took %v; the JavaScript SDK waits at most 100 ms + 200 ms", gap)
	}
}

// The JavaScript SDK reads AWS_RETRY_MODE (client-bedrock-runtime runtimeConfig.js retryMode, NODE_RETRY_MODE_CONFIG_OPTIONS), and its
// AdaptiveRetryStrategy wraps a StandardRetryStrategy with the same backoff. The client keeps the resolved mode and the JavaScript delay.
func TestBedrockClientKeepsTheRetryModeWithTheJavaScriptSDKsBackoff(t *testing.T) {
	before := bedrockRetryRandom
	t.Cleanup(func() { bedrockRetryRandom = before })
	bedrockRetryRandom = func() float64 { return 0.5 }
	serverError := &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}, Err: &smithy.GenericAPIError{Code: "ServiceUnavailableException"}}
	for _, tc := range []struct {
		mode     string
		adaptive bool
	}{
		{"standard", false},
		{"adaptive", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("AWS_RETRY_MODE", tc.mode)
			cfg, runtimeHTTPClient, err := loadBedrockConfig(t.Context(), "http://127.0.0.1:9", "fixture", StreamOptions{Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
			if err != nil {
				t.Fatal(err)
			}
			retryer := bedrockruntime.NewFromConfig(cfg, bedrockClientOptions(cfg, runtimeHTTPClient)).Options().Retryer
			if _, adaptive := retryer.(*retry.AdaptiveMode); adaptive != tc.adaptive {
				t.Fatalf("retryer = %T, want adaptive %v", retryer, tc.adaptive)
			}
			if delay, err := retryer.RetryDelay(1, serverError); err != nil || delay != 50*time.Millisecond {
				t.Fatalf("RetryDelay(1) = %v, %v; want 50ms", delay, err)
			}
		})
	}
}
