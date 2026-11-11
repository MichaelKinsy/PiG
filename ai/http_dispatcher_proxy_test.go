package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHTTPProxyCancellationClosesConnectSocket(t *testing.T) {
	clearDispatcherProxyEnv(t)
	entered := make(chan struct{})
	closed := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close(); close(closed) }()
		close(entered)
		var b [1]byte
		_, _ = conn.Read(b[:])
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	client := baseStreamingHTTPClient(false)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	<-closed
}

func TestHTTPProxySettingsPreserveExplicitEmptyEnvironment(t *testing.T) {
	clearDispatcherProxyEnv(t)
	t.Setenv("HTTP_PROXY", "")
	if err := ApplyHTTPProxySettings("http://settings:7890"); err != nil {
		t.Fatal(err)
	}
	if value, exists := os.LookupEnv("HTTP_PROXY"); !exists || value != "" {
		t.Fatalf("HTTP_PROXY=(%q,%v)", value, exists)
	}
	if got := os.Getenv("HTTPS_PROXY"); got != "http://settings:7890" {
		t.Fatalf("HTTPS_PROXY=%q", got)
	}
}
