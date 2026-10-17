package ai

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Pi reuses connections through one process-wide undici dispatcher (packages/coding-agent/src/core/http-dispatcher.ts
// configureHttpDispatcher). Every provider build used to create its own http.Transport, so each model request opened a
// new TCP connection and left an idle socket with its goroutines behind (comb-audit row 6, audit-go-pig H1).
// Providers of the same TLS mode, idle timeout and proxy environment share one transport.
func TestProvidersReuseOneConnectionAcrossBuilds(t *testing.T) {
	var conns atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Path == "/v1/messages" {
			_, _ = fmt.Fprint(w, anthropicMinimalFixture())
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	// A new idle timeout replaces the shared transport, as configureHttpDispatcher replaces the global dispatcher, so the count starts from a fresh one.
	if err := ConfigureHTTPDispatcher(299_001); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(DefaultHTTPIdleTimeoutMs) })

	request := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	for i := range 10 {
		openai := NewOpenAIProvider(OpenAIConfig{Model: "m", APIKey: "k", BaseURL: server.URL})
		stream, err := openai.Stream(t.Context(), request, StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if message := stream.Result(); message.StopReason != StopReasonStop {
			t.Fatalf("request %d: %+v", i, message)
		}
	}
	for i := range 10 {
		anthropic := NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "k", BaseURL: server.URL})
		stream, err := anthropic.Stream(t.Context(), request, StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if message := stream.Result(); message.StopReason != StopReasonStop {
			t.Fatalf("anthropic request %d: %+v", i, message)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("20 requests through 20 provider builds opened %d TCP connections, want 1", n)
	}
}

// A changed idle timeout (ConfigureHTTPDispatcher) or TLS mode must not reuse a transport built for another setting.
func TestStreamingTransportIsKeyedByItsSettings(t *testing.T) {
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(DefaultHTTPIdleTimeoutMs) })
	base := func(insecure bool) http.RoundTripper { return baseStreamingHTTPClient(insecure).Transport }
	if base(false) != base(false) {
		t.Fatal("same settings must share a transport")
	}
	if base(false) == base(true) {
		t.Fatal("insecure TLS must not share the verifying transport")
	}
	first := base(false)
	if err := ConfigureHTTPDispatcher(123_456); err != nil {
		t.Fatal(err)
	}
	if base(false) == first {
		t.Fatal("a new idle timeout must not reuse the old transport")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	if base(false) == first {
		t.Fatal("a changed proxy environment must not reuse the old transport")
	}
}

// The no-retry streaming client wraps the same shared transport, so it reuses the connection too.
func TestStreamingClientWithoutRetryReusesTheConnection(t *testing.T) {
	var conns atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	for range 6 {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := streamingHTTPClientNoRetry().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(response.Body); err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("6 requests through freshly built no-retry clients opened %d connections, want 1", n)
	}
}

// Undici's headersTimeout and bodyTimeout run from the request that is being answered. A pooled connection's next request gets the whole
// budget again, however long the connection sat idle before it. Go's transport keeps a read pending on an idle pooled connection, so a read
// deadline set when that read began must not shorten the budget of the request that reuses the connection.
func TestReusedStreamingConnectionGivesTheNextRequestTheFullIdleTimeout(t *testing.T) {
	const timeout, idle, slowHeaders = 3000 * time.Millisecond, 2000 * time.Millisecond, 2000 * time.Millisecond
	previous := ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(previous) })
	if err := ConfigureHTTPDispatcher(int(timeout.Milliseconds())); err != nil {
		t.Fatal(err)
	}
	var requests, accepted atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 2 {
			time.Sleep(slowHeaders)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			accepted.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	// A model request is a POST whose body is written before the answer is awaited, so net/http does not replay it on another connection.
	post := func() error {
		response, err := newStreamingHTTPClient(false).Post(server.URL, "application/json", strings.NewReader(`{"model":"m"}`))
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		_, err = io.ReadAll(response.Body)
		return err
	}
	if err := post(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(idle)
	if err := post(); err != nil {
		t.Fatalf("a request on a connection idle for %v failed while its headers took %v of the %v idle timeout: %v", idle, slowHeaders, timeout, err)
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("the second request opened a new connection (%d in all); the test needs it to reuse the first", got)
	}
}
