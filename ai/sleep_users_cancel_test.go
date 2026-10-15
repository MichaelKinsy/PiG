package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// kimi-coding.ts:217-221 and github-copilot.ts:164 wait with utils/sleep, which rejects with signal.reason: a refresh or a rate-limit retry cancelled while it waits fails with the cancellation cause, not with a replacement message.
func TestRetryWaitsFailWithTheCancellationCause(t *testing.T) {
	stop := errors.New("operator stopped the wait")
	cancelSoon := func() (context.Context, context.CancelCauseFunc) {
		ctx, cancel := context.WithCancelCause(t.Context())
		time.AfterFunc(50*time.Millisecond, func() { cancel(stop) })
		return ctx, cancel
	}

	t.Run("kimi refresh backoff", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
		defer server.Close()
		ctx, cancel := cancelSoon()
		defer cancel(nil)
		provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL}
		if _, err := provider.RefreshTokenContext(ctx, OAuthCredentials{Refresh: "old"}); !errors.Is(err, stop) {
			t.Fatalf("RefreshTokenContext error = %v, want the cancellation cause", err)
		}
	})

	t.Run("copilot rate-limit retry", func(t *testing.T) {
		withMockCopilotClient(t, func(*http.Request) (*http.Response, error) {
			response := copilotJSONResp(http.StatusTooManyRequests, "")
			response.Header.Set("Retry-After", "30")
			return response, nil
		})
		ctx, cancel := cancelSoon()
		defer cancel(nil)
		_, err := copilotFetchWithRetry(ctx, copilotRetryPolicy{MaxRetries: 2}, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, "http://copilot.invalid/", nil)
		})
		if !errors.Is(err, stop) {
			t.Fatalf("copilotFetchWithRetry error = %v, want the cancellation cause", err)
		}
	})
}
