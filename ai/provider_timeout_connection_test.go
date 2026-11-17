package ai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
//
// The timer is a wall-clock timer that starts when the request is handed to the transport, so it covers the dial, the proxy's CONNECT and the
// send as well as the wait for the answer, in Pi as here. A test that wants the timeout to fall on a request the server holds must therefore not
// leave the order of the two to the scheduler: on a loaded machine a 250 ms timer fires before the request has reached the server, and the
// timeout then closes a connection that carried no request, which is another path (TestProviderTimeoutBeforeTheTunnelIsUp). The product reads
// the time directly, so the tests do not move the clock; they keep the cancellation the timer issues from reaching the dispatcher until the
// server has read every request (arrivalGate).

// arrivalGate sits between the provider's timer and the dispatcher. The timer fires after TimeoutMs and cancels the round trip as it does in
// production; the gate lets that cancellation through to the dispatcher, which closes the connection, only once the test has opened it, which
// it does when the server has the request. Until then a request whose timer has fired waits at the gate, with its connection as it is, so no
// connection can be closed before its request has arrived, whatever the load: the order is a gate and not a time.
type arrivalGate struct {
	open  chan struct{}
	once  sync.Once
	ended <-chan struct{}
}

// newArrivalGate returns a closed gate. It opens by itself when the test ends, so a test that fails before it opens the gate leaves no request
// waiting.
func newArrivalGate(t testing.TB) *arrivalGate {
	return &arrivalGate{open: make(chan struct{}), ended: t.Context().Done()}
}

// Open lets the cancellations through, those waiting and those still to come.
func (g *arrivalGate) Open() { g.once.Do(func() { close(g.open) }) }

// around returns a transport that passes requests to base and holds their cancellation at the gate.
func (g *arrivalGate) around(base http.RoundTripper) http.RoundTripper {
	return &gatedCancellation{base: base, gate: g}
}

// holdSharedDispatcher puts the gate in front of the shared streaming transport, which every provider built from now on takes its connections
// from (baseStreamingHTTPClient), and restores the transport when the test ends. It must run after the proxy environment is set and before the
// provider is built.
func (g *arrivalGate) holdSharedDispatcher(t testing.TB) {
	t.Helper()
	// A change of proxy environment replaces the shared transport at the next call, so the slot is made current before it is wrapped.
	baseStreamingHTTPClient(false)
	streamingTransports.Lock()
	defer streamingTransports.Unlock()
	slot := &streamingTransports.current[0]
	shared := slot.transport
	held := g.around(shared)
	slot.transport = held
	t.Cleanup(func() {
		streamingTransports.Lock()
		defer streamingTransports.Unlock()
		if slot.transport == held {
			slot.transport = shared
		}
	})
}

// gatedCancellation hands each request to base under a context that is cancelled with the request's own cause, but only after the gate is open.
// It is for a server that never answers: an answer is a failure.
type gatedCancellation struct {
	base http.RoundTripper
	gate *arrivalGate
}

func (g *gatedCancellation) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(request.Context()))
	defer cancel(nil)
	returned := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Go(func() {
		select {
		case <-request.Context().Done():
		case <-returned:
			return
		}
		select {
		case <-g.gate.open:
		case <-g.gate.ended:
		case <-returned:
			return
		}
		cancel(context.Cause(request.Context()))
	})
	response, err := g.base.RoundTrip(request.WithContext(ctx))
	close(returned)
	watcher.Wait()
	if err != nil {
		return nil, err
	}
	_ = response.Body.Close()
	return nil, errors.New("the server that holds every request answered one")
}

func (g *gatedCancellation) CloseIdleConnections() {
	if closer, ok := g.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

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

// timeoutConnectionRoute is one of the ways a provider request leaves the process.
type timeoutConnectionRoute struct {
	options StreamOptions
	// proxy is the CONNECT proxy of a proxy route; nil on the others.
	proxy *connectProxy
}

// timeoutConnectionRouteFor prepares PiG's own dispatcher ("direct"), the same through an HTTP_PROXY CONNECT tunnel ("proxy tunnel") or through
// a proxy that dials the target and never answers the CONNECT ("unanswered proxy tunnel"), or a caller's fetch client, StreamOptions.Fetch
// ("caller's fetch"). A gate, when given, holds the cancellation of every request on the route (arrivalGate). It must run before the provider is
// built: the dispatcher reads the proxy environment when a client is made.
func timeoutConnectionRouteFor(t *testing.T, transport string, gate *arrivalGate) timeoutConnectionRoute {
	t.Helper()
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(name, "")
	}
	route := timeoutConnectionRoute{options: StreamOptions{MaxRetries: new(0)}}
	switch transport {
	case "direct":
	case "proxy tunnel", "unanswered proxy tunnel":
		route.proxy = startConnectProxy(t, transport == "unanswered proxy tunnel")
		t.Setenv("http_proxy", "http://"+route.proxy.address)
	case "caller's fetch":
		own := &http.Transport{}
		t.Cleanup(own.CloseIdleConnections)
		var fetch http.RoundTripper = own
		if gate != nil {
			fetch = gate.around(own)
		}
		route.options.Fetch = &http.Client{Transport: fetch}
	default:
		t.Fatalf("unknown transport %q", transport)
	}
	if gate != nil && transport != "caller's fetch" {
		gate.holdSharedDispatcher(t)
	}
	return route
}

// connectProxy is a CONNECT proxy, the kind HTTP_PROXY names: it relays bytes both ways and closes both sides when either one closes. An
// unanswered one dials the target as the CONNECT asks and then answers nothing, until the client closes its side.
type connectProxy struct {
	address    string
	unanswered bool
	// tunnels counts the tunnels the proxy answered.
	tunnels atomic.Int32
	// pending gets one value for each CONNECT an unanswered proxy holds, once the target is dialed; gaveUp one for each of them whose client
	// closed its side.
	pending, gaveUp chan struct{}
}

func startConnectProxy(t *testing.T, unanswered bool) *connectProxy {
	t.Helper()
	proxy := &connectProxy{unanswered: unanswered, pending: make(chan struct{}, 64), gaveUp: make(chan struct{}, 64)}
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
		if proxy.unanswered {
			proxy.pending <- struct{}{}
			// The read ends when the client closes its side, or resets it.
			_, _ = io.Copy(io.Discard, reader)
			proxy.gaveUp <- struct{}{}
			return
		}
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		proxy.tunnels.Add(1)
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
	proxy.address = address.String()
	return proxy
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

// A provider that takes the request and never answers: every one of twenty requests ends with the SDK's "Request timed out.", the server
// sees each request's connection close, and afterwards no connection is open and every goroutine the requests started has ended. The timer is
// the product's, 250 ms; the gate (arrivalGate) keeps its cancellation from the dispatcher until the server has read all twenty requests, so the
// connection that is closed is always the connection of a request the server holds.
func TestProviderTimeoutClosesTheRequestConnection(t *testing.T) {
	const timeouts = 20
	for _, transport := range []string{"direct", "proxy tunnel", "caller's fetch"} {
		for _, shape := range timeoutConnectionShapes() {
			t.Run(transport+"/"+shape.name, func(t *testing.T) {
				server := testenv.NewHeldRequestServer(t, func(int) testenv.HeldAnswer { return testenv.HeldAnswer{} })
				gate := newArrivalGate(t)
				route := timeoutConnectionRouteFor(t, transport, gate)
				options := route.options
				options.TimeoutMs = new(250)
				provider := shape.provider(server.URL)
				t.Cleanup(func() { _ = provider.Close() })

				baseline := testenv.BaselineGoroutines()
				results := make(chan string, timeouts)
				for range timeouts {
					go func() { results <- timeoutConnectionResult(t.Context(), provider, options) }()
				}
				for range timeouts {
					testenv.Await(t, server.Received, "a request to reach the server")
				}
				gate.Open()
				for range timeouts {
					if got := testenv.Await(t, results, "a request to end"); got != "error: Request timed out." {
						t.Fatalf("result = %q, want error: Request timed out.", got)
					}
				}
				for range timeouts {
					if held := testenv.Await(t, server.Closed, "the server to see a timed-out request's connection close"); !held {
						t.Fatal("a connection closed that held no request")
					}
				}
				if got := server.Requests(); got != timeouts {
					t.Fatalf("server requests = %d, want %d", got, timeouts)
				}
				if route.proxy != nil && route.proxy.tunnels.Load() != timeouts {
					t.Fatalf("proxy tunnels = %d, want %d", route.proxy.tunnels.Load(), timeouts)
				}
				if open := server.Open(); open != 0 {
					t.Fatalf("server connections still open after %d timeouts = %d, want 0", timeouts, open)
				}
				baseline.RequireEnded(t, 10*time.Second)
			})
		}
	}
}

// A provider timeout that falls while the request is still being sent: the proxy has dialed the target and does not answer the CONNECT. This is
// what a stall of the machine does to the test above, made certain by a proxy that waits for the client instead of for a time. The SDK's timer
// covers the connection as well as the wait for headers, so the request ends with the same "Request timed out." and not with a connection
// error, and the connection that carried no request is closed too: the client's side to the proxy, and through it the target's. The gate opens
// when the proxy holds the CONNECT, so the timeout takes effect on a tunnel that is half made and not before the proxy has it.
//
// Direct and a caller's fetch have no such moment on a loopback server: the dial completes before the transport sends, and a timeout after
// that is the test above.
func TestProviderTimeoutBeforeTheTunnelIsUp(t *testing.T) {
	for _, shape := range timeoutConnectionShapes() {
		t.Run(shape.name, func(t *testing.T) {
			server := testenv.NewHeldRequestServer(t, func(int) testenv.HeldAnswer { return testenv.HeldAnswer{} })
			gate := newArrivalGate(t)
			route := timeoutConnectionRouteFor(t, "unanswered proxy tunnel", gate)
			options := route.options
			options.TimeoutMs = new(250)
			provider := shape.provider(server.URL)
			t.Cleanup(func() { _ = provider.Close() })

			baseline := testenv.BaselineGoroutines()
			results := make(chan string, 1)
			go func() { results <- timeoutConnectionResult(t.Context(), provider, options) }()
			testenv.Await(t, route.proxy.pending, "the proxy to hold the CONNECT")
			gate.Open()
			if got := testenv.Await(t, results, "the request to end"); got != "error: Request timed out." {
				t.Fatalf("result = %q, want error: Request timed out.", got)
			}
			testenv.Await(t, route.proxy.gaveUp, "the proxy to see the client close its side of the tunnel")
			if held := testenv.Await(t, server.Closed, "the server to see the target connection close"); held {
				t.Fatal("the target connection closed holding a request, but none was sent")
			}
			if got := server.Requests(); got != 0 {
				t.Fatalf("server requests = %d, want 0", got)
			}
			if got := route.proxy.tunnels.Load(); got != 0 {
				t.Fatalf("proxy tunnels = %d, want 0: the proxy never answered", got)
			}
			if open := server.Open(); open != 0 {
				t.Fatalf("server connections still open = %d, want 0", open)
			}
			baseline.RequireEnded(t, 10*time.Second)
		})
	}
}

// After the response headers the SDK's timer is gone, and a body that stalls is ended by the dispatcher's bodyTimeout, which fails the stream
// with "terminated". PiG's dispatcher arms two timers of that length: the connection's byte idle timer (Pi's Body Timeout Error) and the
// event-stream idle timer (D131). On a body that sends nothing either one may fire first; the connection is closed by both.
//
// A caller's fetch has neither timer, in Pi as here: Pi's bodyTimeout belongs to the undici dispatcher that configureHttpDispatcher installs
// globally, not to the provider, so a fetch implementation that does not go through that dispatcher is not bounded by it, and PiG's two
// timers belong to its own dispatcher in the same way. Such a body ends only when the caller aborts.
//
// The byte idle timer's deadline is set when the connection is handed to the request, so with a 250 ms timeout the whole wait for the response
// headers would be a race against the scheduler. The dispatcher reads the configured timeout whenever it arms a timer (ConfigureHTTPDispatcher
// applies to connections already open, as Pi's reinstalled dispatcher does), so the test configures an hour while the request is on its way, and
// the server, which reads the request and then waits to be released, is released only after the test has configured 250 ms: every timer that
// can fire before the request has arrived is an hour long, and both timers that end the stalled body are armed after the headers.
func TestProviderIdleTimeoutClosesAStalledResponseBody(t *testing.T) {
	const idleTimeoutMs, idleHoldMs = 250, 3_600_000
	previous := ConfiguredHTTPIdleTimeoutMs()
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
				arrived := make(chan struct{}, 1)
				release := make(chan struct{})
				server := testenv.NewHeldRequestServer(t, func(int) testenv.HeldAnswer {
					// The plan runs once the request has been read whole, before the headers are written.
					arrived <- struct{}{}
					select {
					case <-release:
					case <-t.Context().Done():
					}
					return testenv.HeldAnswer{Headers: true}
				})
				if err := ConfigureHTTPDispatcher(idleHoldMs); err != nil {
					t.Fatal(err)
				}
				route := timeoutConnectionRouteFor(t, transport, nil)
				provider := shape.provider(server.URL)
				t.Cleanup(func() { _ = provider.Close() })

				baseline := testenv.BaselineGoroutines()
				results := make(chan string, 1)
				go func() { results <- timeoutConnectionResult(t.Context(), provider, route.options) }()
				testenv.Await(t, arrived, "the request to reach the server")
				if err := ConfigureHTTPDispatcher(idleTimeoutMs); err != nil {
					t.Fatal(err)
				}
				close(release)
				if got := testenv.Await(t, results, "the stalled stream to end"); !slices.Contains(want, got) {
					t.Fatalf("result = %q, want one of %q", got, want)
				}
				if held := testenv.Await(t, server.Closed, "the server to see the stalled response's connection close"); !held {
					t.Fatal("a connection closed that held no request")
				}
				if got := server.Requests(); got != 1 {
					t.Fatalf("server requests = %d, want 1", got)
				}
				if route.proxy != nil && route.proxy.tunnels.Load() != 1 {
					t.Fatalf("proxy tunnels = %d, want 1", route.proxy.tunnels.Load())
				}
				if open := server.Open(); open != 0 {
					t.Fatalf("server connections still open = %d, want 0", open)
				}
				baseline.RequireEnded(t, 10*time.Second)
			})
		}
	}
}
