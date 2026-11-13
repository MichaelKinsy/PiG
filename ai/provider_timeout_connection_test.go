package ai

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// What becomes of the connection of a provider request that is given up.
//
// Pi (v1.5.2) hands StreamOptions.timeoutMs, which the coding agent takes from retry.provider.timeoutMs or else from the HTTP idle timeout
// (sdk.ts buildRequestOptions), to the OpenAI and Anthropic SDKs as `timeout`, with maxRetries 0 (openai-completions.ts, openai-responses.ts,
// anthropic-messages.ts). The SDK owns the timer, one per fetch attempt (fetchWithTimeout in client.js of openai@7.19.0 and of
// @anthropic-ai/sdk@0.129.0): when it fires, the SDK aborts that fetch's AbortController, which destroys the request's connection, and
// rejects with APIConnectionTimeoutError "Request timed out."; when the response headers arrive first, the timer is cleared, and from then on
// only the dispatcher's bodyTimeout (http-dispatcher.ts configureHttpDispatcher) and the caller's signal end the body. PiG arms the same timer
// in providerRequestTransport and cancels the round trip's context with it.
//
// The server of these tests reads only, so it sees the close when it happens; the tests wait for that event and compare counts, never times.

type timeoutConnectionShape struct {
	name     string
	provider func(baseURL string) Provider
}

// timeoutConnectionShapes are the three API kinds, each with the credential shapes it is used with: an API key, an OAuth token, and an
// OpenAI-compatible gateway under a provider id the registry does not know.
func timeoutConnectionShapes() []timeoutConnectionShape {
	return []timeoutConnectionShape{
		{"anthropic-messages API key", func(u string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: u, APIKey: "test-key", Model: "claude-sonnet-4-20250514"})
		}},
		{"anthropic-messages OAuth token", func(u string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: u, APIKey: "sk-ant-oat-test-token", Model: "claude-sonnet-4-20250514"})
		}},
		{"openai-completions API key", func(u string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: u, APIKey: "test-key", Model: "gpt-test", ProviderID: "openai"})
		}},
		{"openai-completions OAuth token", func(u string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: u, APIKey: "tid=test;exp=9999999999", Model: "gpt-4o", ProviderID: "github-copilot"})
		}},
		{"openai-completions custom base URL", func(u string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: u, APIKey: "test-key", Model: "gateway-model", ProviderID: "my-gateway"})
		}},
		{"openai-responses API key", func(u string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: u, APIKey: "test-key", Model: "gpt-test", ProviderID: "openai"})
		}},
		{"openai-responses OAuth token", func(u string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: u, APIKey: "tid=test;exp=9999999999", Model: "gpt-5", ProviderID: "github-copilot"})
		}},
		{"openai-responses custom base URL", func(u string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: u, APIKey: "test-key", Model: "gateway-model", ProviderID: "my-gateway"})
		}},
	}
}

// timeoutConnectionRoute is one of the three ways a provider request leaves the process.
type timeoutConnectionRoute struct {
	options StreamOptions
	// tunnels counts the CONNECT tunnels the proxy opened; nil without a proxy.
	tunnels *atomic.Int32
}

// timeoutConnectionRouteFor prepares PiG's own dispatcher ("direct"), the same through an HTTP_PROXY CONNECT tunnel ("proxy tunnel"), or a
// caller's fetch client, StreamOptions.Fetch ("caller's fetch"). It must run before the provider is built: the dispatcher reads the proxy
// environment when a client is made.
func timeoutConnectionRouteFor(t *testing.T, transport string) timeoutConnectionRoute {
	t.Helper()
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(name, "")
	}
	route := timeoutConnectionRoute{options: StreamOptions{MaxRetries: new(0)}}
	switch transport {
	case "direct":
	case "proxy tunnel":
		address, tunnels := connectProxy(t)
		route.tunnels = tunnels
		t.Setenv("http_proxy", "http://"+address)
	case "caller's fetch":
		own := &http.Transport{}
		t.Cleanup(own.CloseIdleConnections)
		route.options.Fetch = &http.Client{Transport: own}
	default:
		t.Fatalf("unknown transport %q", transport)
	}
	return route
}

// connectProxy starts a CONNECT proxy, the kind HTTP_PROXY names: it relays bytes both ways and closes both sides when either one closes.
func connectProxy(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	tunnels := &atomic.Int32{}
	address := testenv.ServeLoopback(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		request, err := http.ReadRequest(reader)
		if err != nil || request.Method != http.MethodConnect {
			return
		}
		upstream, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", request.Host)
		if err != nil {
			return
		}
		defer func() { _ = upstream.Close() }()
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		tunnels.Add(1)
		var relay sync.WaitGroup
		relay.Go(func() {
			_, _ = io.Copy(upstream, reader)
			_ = upstream.Close()
			_ = conn.Close()
		})
		relay.Go(func() {
			_, _ = io.Copy(conn, upstream)
			_ = conn.Close()
			_ = upstream.Close()
		})
		relay.Wait()
	})
	return address.String(), tunnels
}

// timeoutConnectionResult sends one request and returns how it ended: the stop reason and the error message.
func timeoutConnectionResult(ctx context.Context, provider Provider, options StreamOptions) string {
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), options)
	if err != nil {
		return "Stream: " + err.Error()
	}
	result := stream.Result()
	return string(result.StopReason) + ": " + result.ErrorMessage
}

// awaitGoroutineBaseline yields until the process has no more goroutines than baseline, its count before the requests were sent: a transport's
// connection goroutines end a moment after the server saw the connection close. It sets no time bound, as testenv.Await sets none: goroutines
// that stay hold the test until its deadline, whose dump names them.
func awaitGoroutineBaseline(t *testing.T, baseline int) {
	t.Helper()
	for runtime.NumGoroutine() > baseline {
		if t.Context().Err() != nil {
			t.Fatalf("goroutines = %d, want the %d from before the requests", runtime.NumGoroutine(), baseline)
		}
		runtime.Gosched()
	}
}

// A provider that takes the request and never answers: every one of twenty requests ends with the SDK's "Request timed out.", the server
// sees each request's connection close, and afterwards no connection is open and the process is back at its goroutine count.
func TestProviderTimeoutClosesTheRequestConnection(t *testing.T) {
	const timeouts = 20
	for _, transport := range []string{"direct", "proxy tunnel", "caller's fetch"} {
		for _, shape := range timeoutConnectionShapes() {
			t.Run(transport+"/"+shape.name, func(t *testing.T) {
				server := testenv.NewHeldRequestServer(t, func(int) testenv.HeldAnswer { return testenv.HeldAnswer{} })
				route := timeoutConnectionRouteFor(t, transport)
				options := route.options
				options.TimeoutMs = new(250)
				provider := shape.provider(server.URL)
				t.Cleanup(func() { _ = provider.Close() })

				baseline := runtime.NumGoroutine()
				results := make(chan string, timeouts)
				for range timeouts {
					go func() { results <- timeoutConnectionResult(t.Context(), provider, options) }()
				}
				for range timeouts {
					if got := testenv.Await(t, results, "a request to end"); got != "error: Request timed out." {
						t.Fatalf("result = %q, want error: Request timed out.", got)
					}
				}
				for range timeouts {
					if held := testenv.Await(t, server.Closed, "the server to see a timed-out request's connection close"); !held {
						t.Fatal("a connection closed before its request had arrived: the provider timeout fired first")
					}
				}
				if got := server.Requests(); got != timeouts {
					t.Fatalf("server requests = %d, want %d", got, timeouts)
				}
				if route.tunnels != nil && route.tunnels.Load() != timeouts {
					t.Fatalf("proxy tunnels = %d, want %d", route.tunnels.Load(), timeouts)
				}
				if open := server.Open(); open != 0 {
					t.Fatalf("server connections still open after %d timeouts = %d, want 0", timeouts, open)
				}
				awaitGoroutineBaseline(t, baseline)
			})
		}
	}
}

// After the response headers the SDK's timer is gone, and a body that stalls is ended by the dispatcher's bodyTimeout, which fails the stream
// with "terminated". PiG's dispatcher arms two timers of that length: the connection's byte idle timer (Pi's Body Timeout Error) and the
// event-stream idle timer (D131). On a body that sends nothing either one may fire first; the connection is closed by both.
//
// A caller's fetch has neither timer, in Pi as here: Pi's bodyTimeout belongs to the undici dispatcher that configureHttpDispatcher installs
// globally, not to the provider, so a fetch implementation that does not go through that dispatcher is not bounded by it, and PiG's two
// timers belong to its own dispatcher in the same way. Such a body ends only when the caller aborts.
func TestProviderIdleTimeoutClosesAStalledResponseBody(t *testing.T) {
	const idleTimeoutMs = 250
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(idleTimeoutMs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ConfigureHTTPDispatcher(previous); err != nil {
			t.Error(err)
		}
	})
	want := []string{
		"error: terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)",
		fmt.Sprintf("error: terminated: SSE Event Timeout Error: no event for %dms", idleTimeoutMs),
	}
	for _, transport := range []string{"direct", "proxy tunnel"} {
		for _, shape := range timeoutConnectionShapes() {
			t.Run(transport+"/"+shape.name, func(t *testing.T) {
				server := testenv.NewHeldRequestServer(t, func(int) testenv.HeldAnswer { return testenv.HeldAnswer{Headers: true} })
				route := timeoutConnectionRouteFor(t, transport)
				provider := shape.provider(server.URL)
				t.Cleanup(func() { _ = provider.Close() })

				baseline := runtime.NumGoroutine()
				if got := timeoutConnectionResult(t.Context(), provider, route.options); !slices.Contains(want, got) {
					t.Fatalf("result = %q, want one of %q", got, want)
				}
				if held := testenv.Await(t, server.Closed, "the server to see the stalled response's connection close"); !held {
					t.Fatal("the connection closed before its request had arrived")
				}
				if got := server.Requests(); got != 1 {
					t.Fatalf("server requests = %d, want 1", got)
				}
				if route.tunnels != nil && route.tunnels.Load() != 1 {
					t.Fatalf("proxy tunnels = %d, want 1", route.tunnels.Load())
				}
				if open := server.Open(); open != 0 {
					t.Fatalf("server connections still open = %d, want 0", open)
				}
				awaitGoroutineBaseline(t, baseline)
			})
		}
	}
}
