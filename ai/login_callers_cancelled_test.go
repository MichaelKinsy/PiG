package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type loopbackRoundTripper func(*http.Request) (*http.Response, error)

func (f loopbackRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// loopbackDefaultClient makes every request of http.DefaultClient reach handler on a local listener, with the request's original host kept as the Host header.
func loopbackDefaultClient(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: loopbackRoundTripper(func(r *http.Request) (*http.Response, error) {
		out := r.Clone(r.Context())
		out.Host = r.URL.Host
		out.URL.Scheme = "http"
		out.URL.Host = server.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(out)
	})}
	t.Cleanup(func() { http.DefaultClient = old })
}

// The registered GitHub Copilot provider (the caller that hands the login its prompt) returns exactly "Login cancelled" when its prompt settles after the signal aborted
// (github-copilot.ts:434), and no request leaves: the listener fails the test on any.
func TestCopilotOAuthProviderLoginSettledPromptAfterAbortIsLoginCancelled(t *testing.T) {
	loopbackDefaultClient(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("a request left a cancelled login: %s %s", r.Method, r.URL)
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := copilotOAuthRegistryProvider{}.LoginContext(ctx, OAuthLoginCallbacks{OnPrompt: func(OAuthPrompt) (string, error) {
		cancel()
		return "", nil
	}})
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("error = %v, want exactly %q", err, "Login cancelled")
	}
}

// The registered OpenAI Codex provider returns exactly "Login cancelled" when the login is cancelled while its first device-code request is in flight against a
// local listener (openai-codex.ts fetchWithLoginCancellation :111-119 wraps the fetch of startDeviceFlow, :188).
func TestCodexOAuthProviderLoginRequestFailingAfterAbortIsLoginCancelled(t *testing.T) {
	entered := make(chan struct{})
	loopbackDefaultClient(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server notices a closed connection only once the body is read
		close(entered)
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := CodexOAuthProvider{}.LoginContext(ctx, OAuthLoginCallbacks{
			OnSelect:     func(OAuthSelectPrompt) (string, error) { return OpenAICodexDeviceCodeLoginMethod, nil },
			OnDeviceCode: func(OAuthDeviceCodeInfo) { t.Error("no device code was issued") },
		})
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("error = %v, want exactly %q", err, "Login cancelled")
	}
}
