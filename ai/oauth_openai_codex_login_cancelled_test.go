package ai

import (
	"context"
	"net/http"
	"testing"
)

// openai-codex.ts fetchWithLoginCancellation (:111) wraps every login request: a fetch that fails after the signal aborted rejects with exactly
// "Login cancelled" instead of the transport's abort error. The device-code user-code request (:188) and the device token poll (:237) use it.
func cancelledMidRequest(t *testing.T, entered chan<- struct{}) func(*http.Request) (*http.Response, error) {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
}

func TestCodexDeviceUserCodeRequestCancelledMidFlightIsLoginCancelled(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(ctx context.Context) error
	}{
		{"startCodexDeviceAuth", func(ctx context.Context) error { _, err := startCodexDeviceAuth(ctx); return err }},
		{"LoginOpenAICodexDeviceCode", func(ctx context.Context) error {
			_, err := LoginOpenAICodexDeviceCode(ctx, func(OAuthDeviceCodeInfo) { t.Error("no device code was issued") })
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			withMockCodexClient(t, cancelledMidRequest(t, entered)) //nolint:bodyclose // the stub never returns a response
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- tc.run(ctx) }()
			<-entered
			cancel()
			if err := <-done; err == nil || err.Error() != "Login cancelled" {
				t.Fatalf("error = %v, want exactly %q", err, "Login cancelled")
			}
		})
	}
}

func TestCodexDeviceTokenPollCancelledMidFlightIsLoginCancelled(t *testing.T) {
	entered := make(chan struct{})
	withMockCodexClient(t, cancelledMidRequest(t, entered)) //nolint:bodyclose // the stub never returns a response
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := codexPollStep(ctx, codexDeviceAuthInfo{deviceAuthID: "d", userCode: "u", intervalSeconds: 0})
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("error = %v, want exactly %q", err, "Login cancelled")
	}
}
