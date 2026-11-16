package coding

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The caller path of ai's TestProviderTimeoutClosesTheRequestConnection: what becomes of a provider request's connection when the session
// gives the request up, by retry.provider.timeoutMs or by the user's Escape. As Pi's SDK timer and the caller's signal abort the fetch, the
// connection is closed then, not when the server answers or the retry cycle ends. The server reads only, so it sees the close when it
// happens; the tests wait for that event and compare counts, never times.
//
// The SDK's timer is a wall-clock timer that starts when the request is handed to the transport, so it covers the dial and the send as well as
// the wait for the answer, in Pi as here. A test that wants the timeout to fall on a request the server holds must not leave the order of the
// two to the scheduler: on a loaded machine a 250 ms timer fires before the request has reached the server, and the timeout then closes a
// connection that carried no request, which is another path (ai's TestProviderTimeoutBeforeTheTunnelIsUp). The session takes any ai.Provider
// and passes the request options through, so the test hands the session the real provider behind a wrapper that supplies the caller's fetch
// (StreamOptions.Fetch): a client over PiG's own dispatcher (ai.NewNodeFetch: the same connection pool, idle timers and connection objects)
// with a gate in front of it (timeoutGate). The timer still fires after 250 ms and cancels the round trip as it does in production; the gate
// lets the cancellation through to the dispatcher, which closes the connection, only once the server has read the first request.

// timeoutGate sits between the provider's request timer and PiG's dispatcher.
type timeoutGate struct {
	arrived  chan struct{}
	once     sync.Once
	ended    <-chan struct{}
	attempts atomic.Int32
	dispatch *ai.NodeFetch
}

// newTimeoutGate returns a closed gate. It opens by itself when the test ends, so a test that fails before it opens the gate leaves no request
// waiting.
func newTimeoutGate(t testing.TB) *timeoutGate {
	return &timeoutGate{arrived: make(chan struct{}), ended: t.Context().Done(), dispatch: ai.NewNodeFetch()}
}

// Open lets the cancellation of the first request through to the dispatcher, the one already waiting and any still to come.
func (g *timeoutGate) Open() { g.once.Do(func() { close(g.arrived) }) }

// provider returns build's provider with the gated fetch in every request's options.
func (g *timeoutGate) provider(build func(baseURL string) ai.Provider) func(baseURL string) ai.Provider {
	return func(baseURL string) ai.Provider {
		return gatedProvider{Provider: build(baseURL), fetch: &http.Client{Transport: g}}
	}
}

// gatedProvider is a provider whose requests go through the caller's fetch.
type gatedProvider struct {
	ai.Provider
	fetch *http.Client
}

func (p gatedProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	options.Fetch = p.fetch
	return p.Provider.Stream(ctx, transcript, options)
}

// RoundTrip hands the request to the dispatcher under a context that is cancelled with the request's own cause, the timer's, but only after the
// gate is open. The first request is the one the server holds and the timeout ends. The second is answered, and the test is about its
// connection staying open, so the timer's cancellation never reaches the dispatcher: a stall of the machine could otherwise end the answered
// retry with a timeout of its own, which is not what the test is about.
func (g *timeoutGate) RoundTrip(request *http.Request) (*http.Response, error) {
	attempt := g.attempts.Add(1)
	caller := request.Context()
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(caller))
	returned := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Go(func() {
		select {
		case <-caller.Done():
		case <-returned:
			return
		}
		hold := g.ended
		if attempt == 1 {
			hold = g.arrived
		}
		select {
		case <-hold:
		case <-g.ended:
		case <-returned:
			return
		}
		cancel(context.Cause(caller))
	})
	response, err := g.dispatch.Do(request.WithContext(ctx))
	close(returned)
	watcher.Wait()
	if err != nil {
		cancel(nil)
		return nil, err
	}
	response.Body = &releasedBody{ReadCloser: response.Body, release: func() { cancel(nil) }}
	return response, nil
}

// releasedBody calls release when the body is closed.
type releasedBody struct {
	io.ReadCloser
	release func()
}

func (b *releasedBody) Close() error {
	err := b.ReadCloser.Close()
	b.release()
	return err
}

// The first request is held to its deadline and its connection is closed; the retry is answered, and its connection is the one kept.
func TestSessionProviderTimeoutClosesTheRequestConnection(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			server := testenv.NewHeldRequestServer(t, func(request int) testenv.HeldAnswer {
				if request == 1 {
					return testenv.HeldAnswer{}
				}
				return testenv.HeldAnswer{Body: api.okBody}
			})
			gate := newTimeoutGate(t)
			api.provider = gate.provider(api.provider)
			h := closedConnectionHarness(t, api, &httptest.Server{URL: server.URL}, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1,"provider":{"timeoutMs":250,"maxRetries":0}}}`)
			log := recordSessionEvents(h.session)
			prompted := make(chan error, 1)
			go func() { prompted <- h.session.Prompt(t.Context(), "hello", nil) }()
			testenv.Await(t, server.Received, "the first request to reach the server")
			gate.Open()
			if err := testenv.Await(t, prompted, "the prompt to return"); err != nil {
				t.Fatal(err)
			}
			if held := testenv.Await(t, server.Closed, "the server to see the timed-out request's connection close"); !held {
				t.Fatal("a connection closed that held no request")
			}
			if got := server.Requests(); got != 2 {
				t.Fatalf("server requests = %d, want 2 (the request that reached its deadline and one retry)", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
				t.Fatalf("retry events = %v", got)
			}
			if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != "Request timed out." {
				t.Fatalf("auto_retry_start = %#v, want Request timed out.", starts)
			}
			if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
				t.Fatalf("last assistant text = %v", text)
			}
			if open := server.Open(); open != 1 {
				t.Fatalf("server connections open = %d, want 1 (the answered retry's)", open)
			}
		})
	}
}

// Escape while the provider holds the request: before any response, and after the response headers with a body that sends nothing. The
// connection is closed, the prompt returns without an error and nothing is retried. An abort that arrives with the headers has net/http read
// the body for up to 50 ms before it closes the connection (transport.go maxPostCloseReadTime), so the close is awaited, not timed.
func TestSessionAbortClosesTheRequestConnection(t *testing.T) {
	phases := []struct {
		name   string
		answer testenv.HeldAnswer
	}{
		{"before the response headers", testenv.HeldAnswer{}},
		{"during a stalled body", testenv.HeldAnswer{Headers: true}},
	}
	for _, api := range closedConnectionAPIs() {
		for _, phase := range phases {
			t.Run(api.name+"/"+phase.name, func(t *testing.T) {
				server := testenv.NewHeldRequestServer(t, func(int) testenv.HeldAnswer { return phase.answer })
				h := closedConnectionHarness(t, api, &httptest.Server{URL: server.URL}, retryEventsSettings)
				prompted := make(chan error, 1)
				go func() { prompted <- h.session.Prompt(t.Context(), "hello", nil) }()
				testenv.Await(t, server.Received, "the server to hold the request")
				if err := h.session.Abort(t.Context()); err != nil {
					t.Fatal(err)
				}
				if held := testenv.Await(t, server.Closed, "the server to see the aborted request's connection close"); !held {
					t.Fatal("a connection closed that held no request")
				}
				if err := testenv.Await(t, prompted, "the prompt to return"); err != nil {
					t.Fatalf("prompt = %v, want nil", err)
				}
				if got := server.Requests(); got != 1 {
					t.Fatalf("server requests = %d, want 1: an aborted request is not retried", got)
				}
				if open := server.Open(); open != 0 {
					t.Fatalf("server connections open = %d, want 0", open)
				}
			})
		}
	}
}
