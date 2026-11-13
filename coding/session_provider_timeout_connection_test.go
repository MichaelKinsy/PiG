package coding

import (
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The caller path of ai's TestProviderTimeoutClosesTheRequestConnection: what becomes of a provider request's connection when the session
// gives the request up, by retry.provider.timeoutMs or by the user's Escape. As Pi's SDK timer and the caller's signal abort the fetch, the
// connection is closed then, not when the server answers or the retry cycle ends. The server reads only, so it sees the close when it
// happens; the tests wait for that event and compare counts, never times.

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
			h := closedConnectionHarness(t, api, &httptest.Server{URL: server.URL}, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1,"provider":{"timeoutMs":250,"maxRetries":0}}}`)
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
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
