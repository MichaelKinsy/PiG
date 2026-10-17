package ai

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Node's global fetch rejects a connection that fails before any response with TypeError("fetch failed"); http.Client's "Post <url>:" decoration is not part of it. Oracle: coding/mcpext TestMcpCommandMatchesPi (`pi mcp login` against a refused port).
func TestNodeFetchRejectsALostConnectionWithFetchFailed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+address+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := NewNodeFetch().Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || err.Error() != "fetch failed" {
		t.Fatalf("error = %v, want fetch failed", err)
	}
	if _, decorated := err.(*url.Error); decorated { //nolint:errorlint // The test checks the rejection is not the client's decoration.
		t.Fatalf("error %T keeps the http.Client decoration", err)
	}
}

// Pi's MCP code calls the global fetch, which uses whatever dispatcher configureHttpDispatcher installed last (interactive-mode.ts applyRuntimeSettings and the
// httpIdleTimeoutMs setting both reinstall it), so a NodeFetch made before a reconfiguration must apply the new header timeout.
func TestNodeFetchFollowsADispatcherConfiguredAfterIt(t *testing.T) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(DefaultHTTPIdleTimeoutMs); err != nil {
		t.Fatal(err)
	}
	fetch := NewNodeFetch()
	if err := ConfigureHTTPDispatcher(100); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ConfigureHTTPDispatcher(previous); err != nil {
			t.Fatal(err)
		}
	}()
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY"} {
		t.Setenv(name, "")
	}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)
	guard, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(guard, http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := fetch.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if guard.Err() != nil || err == nil || err.Error() != "fetch failed" {
		t.Fatalf("error = %v (guard %v), want the 100 ms header timeout's fetch failed", err, guard.Err())
	}
}

// The reverse direction: a timeout raised after the NodeFetch was made lets a slower response through, as the reinstalled global dispatcher does.
func TestNodeFetchWaitsForALongerTimeoutConfiguredAfterIt(t *testing.T) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(100); err != nil {
		t.Fatal(err)
	}
	fetch := NewNodeFetch()
	if err := ConfigureHTTPDispatcher(30_000); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ConfigureHTTPDispatcher(previous); err != nil {
			t.Fatal(err)
		}
	}()
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY"} {
		t.Setenv(name, "")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond) // longer than the 100 ms the fetch was made under, far shorter than the 30 s now configured
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := fetch.Do(request)
	if err != nil {
		t.Fatalf("error = %v, want the response under the 30 s timeout", err)
	}
	_ = response.Body.Close()
}
