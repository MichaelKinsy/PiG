package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// probeHeader names the probe a request belongs to. The scripted server answers a request only when it carries the id of the probe currently scripted.
const probeHeader = "X-Pig-Probe"

// probeServer is the one scripted HTTP server a classifier oracle test sends every probe to, and so the one keep-alive connection they all share. A server and a connection per probe cost a listener and a TCP connection
// each, and Windows hands the next listener the same dynamic port again (127.0.0.1:49152 in run after run), so a new connection collides with a four-tuple still in TIME_WAIT often enough for a 700-probe test to fail
// about one run in twenty: connectex "Only one usage of each socket address (protocol/network address/port) is normally permitted" (WSAEADDRINUSE), which PiG reports as "fetch failed" before the handler sees the request.
// Production never opens a listener per request; only the tests did.
//
// A probe is selected by the probeHeader the request carries (probeOptionHeaders), never by timing or order: a request for any other probe, or with no probe, fails the test loudly and gets a 500.
type probeServer struct {
	*httptest.Server
	t       testing.TB
	mu      sync.Mutex
	current string
	handler http.Handler
	routed  int
	stray   int
}

// newProbeServer starts the server and closes it when the test ends.
func newProbeServer(t testing.TB) *probeServer {
	t.Helper()
	s := &probeServer{t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func probeID(i int) string { return fmt.Sprintf("probe-%d", i) }

// probeHeaders are the headers that route a request to probe i.
func probeHeaders(i int) map[string]string { return map[string]string{probeHeader: probeID(i)} }

// probeOptionHeaders is probeHeaders as the request headers of ClassifierOptions.
func probeOptionHeaders(i int) ProviderHeaders { return ProviderHeadersFromStrings(probeHeaders(i)) }

// forProbe is options with the headers that route the request to probe i.
func forProbe(options ClassifierOptions, i int) ClassifierOptions {
	options.Headers = probeOptionHeaders(i)
	return options
}

// script makes handler the answer to every request of probe i, until the next script call.
func (s *probeServer) script(i int, handler http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current, s.handler = probeID(i), handler
}

func (s *probeServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	handler, current := s.handler, s.current
	got := r.Header.Get(probeHeader)
	if handler == nil || got != current {
		s.stray++
		s.mu.Unlock()
		s.t.Errorf("request %s %s carries probe %q while probe %q is scripted", r.Method, r.URL.RequestURI(), got, current)
		http.Error(w, "unscripted probe", http.StatusInternalServerError)
		return
	}
	s.routed++
	s.mu.Unlock()
	handler.ServeHTTP(w, r)
}

// requireRouted fails unless the server answered exactly want requests, all of them routed to the probe they were for: a request that took another path to the server, or none at all, cannot pass silently.
func (s *probeServer) requireRouted(want int) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routed != want || s.stray != 0 {
		s.t.Errorf("the probe server answered %d requests and turned away %d; the probes expected %d", s.routed, s.stray, want)
	}
}

// failureRecorder is a testing.TB whose failures are kept, so a test can check that probeServer reports them.
type failureRecorder struct {
	testing.TB
	failures []string
}

func (r *failureRecorder) Helper() {}

func (r *failureRecorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *failureRecorder) Cleanup(func()) {}

// TestProbeServerRoutesByProbeHeader pins what makes the shared server safe to use: a request is answered only for the probe currently scripted, every other request fails the test and gets a 500, and a count that does not
// match what the probes expected fails too.
func TestProbeServerRoutesByProbeHeader(t *testing.T) {
	recorder := &failureRecorder{TB: t}
	server := newProbeServer(recorder)
	t.Cleanup(server.Close)
	server.script(1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("one")) }))
	get := func(headers map[string]string) (int, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/x", nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode, response.Status
	}
	if status, _ := get(probeHeaders(1)); status != http.StatusOK {
		t.Fatalf("the scripted probe got status %d", status)
	}
	if len(recorder.failures) != 0 {
		t.Fatalf("the scripted probe's request failed the test: %q", recorder.failures)
	}
	for name, headers := range map[string]map[string]string{"another probe": probeHeaders(2), "no probe": nil} {
		if status, _ := get(headers); status != http.StatusInternalServerError {
			t.Errorf("%s: status %d, want 500", name, status)
		}
	}
	if len(recorder.failures) != 2 || !slices.ContainsFunc(recorder.failures, func(f string) bool {
		return strings.Contains(f, `carries probe "probe-2" while probe "probe-1" is scripted`)
	}) {
		t.Fatalf("a request for an unscripted probe must fail the test loudly; failures: %q", recorder.failures)
	}
	recorder.failures = nil
	server.requireRouted(1)
	if len(recorder.failures) != 1 || !strings.Contains(recorder.failures[0], "answered 1 requests and turned away 2") {
		t.Errorf("requireRouted must report the two turned-away requests; failures: %q", recorder.failures)
	}
	recorder.failures = nil
	server.requireRouted(2)
	if len(recorder.failures) != 1 {
		t.Errorf("requireRouted must report a count that differs from the probes'; failures: %q", recorder.failures)
	}
}
