package mcp_test

import (
	"net/http"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/mcp"
)

// abortedRequestFetch is a McpFetch whose GET blocks until its request is aborted, then until the test lets it return, so
// the aborted request is still inside fetch when the transport sends the DELETE of Close. It records how many other calls
// were inside fetch whenever a DELETE arrived.
type abortedRequestFetch struct {
	release chan struct{}

	mu       sync.Mutex
	inFlight int
	deletes  []int
	headers  []http.Header
}

func (f *abortedRequestFetch) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.inFlight++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	switch request.Method {
	case http.MethodGet:
		<-request.Context().Done()
		<-f.release
		return nil, request.Context().Err()
	case http.MethodDelete:
		f.mu.Lock()
		f.deletes = append(f.deletes, f.inFlight-1)
		f.headers = append(f.headers, request.Header.Clone())
		f.mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}, nil
	}
	return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{"Mcp-Session-Id": {"session-1"}}, Body: http.NoBody}, nil
}

// deleteArrivals are the other calls that were inside fetch when each DELETE arrived, and the DELETEs' headers.
func (f *abortedRequestFetch) deleteArrivals() ([]int, []http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.deletes...), append([]http.Header(nil), f.headers...)
}

// Close aborts the requests in flight and then sends the session DELETE. net/http can fail a request on a connection that an
// aborted request was handing its response over on: the DELETE then dies with that request's context error, and the server
// never ends the session (the flake of TestAgentSessionMCPOAuthClosesTheSessionWithoutRefreshingAnExpiringToken: a GET stream
// answered 405 while the session closed). Node's fetch has no such cross-request failure, so the DELETE of Pi's close() has
// no such precondition. The DELETE must therefore leave only after every request the transport aborted has returned from fetch.
func TestStreamableHTTPTransportSendsTheSessionDeleteOnlyAfterTheAbortedRequestsReturned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fetch := &abortedRequestFetch{release: make(chan struct{})}
		transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: "http://mcp.test/mcp", Fetch: fetch})
		if err := transport.Start(); err != nil {
			t.Fatal(err)
		}
		// The 202 assigns the session, and the initialized notification opens the GET stream, which stays in fetch.
		if err := transport.Send(t.Context(), mcp.NewNotification("notifications/initialized", nil)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		closed := make(chan struct{})
		go func() {
			defer close(closed)
			_ = transport.Close()
		}()
		synctest.Wait()
		// The GET is aborted but has not returned.
		if arrivals, _ := fetch.deleteArrivals(); len(arrivals) != 0 {
			t.Errorf("the DELETE reached fetch while the aborted GET request was still inside it (%d other calls in flight)", arrivals[0])
		}
		close(fetch.release)
		synctest.Wait()

		select {
		case <-closed:
		default:
			t.Fatal("Close did not return once the aborted request returned")
		}
		arrivals, headers := fetch.deleteArrivals()
		if len(arrivals) != 1 || arrivals[0] != 0 {
			t.Fatalf("DELETE arrivals = %v (other calls in flight), want one with none", arrivals)
		}
		if got := headers[0].Get("Mcp-Session-Id"); got != "session-1" {
			t.Errorf("DELETE Mcp-Session-Id = %q, want session-1", got)
		}
	})
}
