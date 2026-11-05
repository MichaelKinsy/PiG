package mcp_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/mcp"
)

// getStreamFetch answers a POST with 202 and a session, and holds a GET until its request is aborted. It is not an
// *http.Client, so it cannot report that a request was written.
type getStreamFetch struct{}

func (getStreamFetch) Do(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}
	return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{"Mcp-Session-Id": {"session-1"}}, Body: http.NoBody}, nil
}

// gatedTokenAuth hands out its first token at once and holds the token of every later request until the test opens the gate.
type gatedTokenAuth struct {
	gate chan struct{}

	mu    sync.Mutex
	calls int
}

func (a *gatedTokenAuth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	a.calls++
	first := a.calls == 1
	a.mu.Unlock()
	if !first {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
	return "token", nil
}

// Pi: packages/mcp/src/transports/streamable-http.ts:238, 406-409. send() starts the GET stream, whose first request asks the auth provider for its token before
// send resolves (the provider's work runs in microtasks), so a caller that goes on to change what the provider hands out, or to close the transport, finds the stream's
// token read. A Go goroutine can start arbitrarily late: a stream that reads its token after the token was brought close to its expiry refreshes it, and
// closing right after a sign-in then waits for that refresh. The first attempt of the stream is waited for whatever the fetch, not only for an *http.Client that
// reports its writes.
func TestStreamableHTTPTransportReadsTheGETStreamsTokenBeforeTheInitializedNotificationReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		auth := &gatedTokenAuth{gate: make(chan struct{})}
		transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: "http://mcp.test/mcp", Fetch: getStreamFetch{}, AuthProvider: auth})
		if err := transport.Start(); err != nil {
			t.Fatal(err)
		}
		sent := make(chan error, 1)
		go func() { sent <- transport.Send(t.Context(), mcp.NewNotification("notifications/initialized", nil)) }()
		// The POST read the first token, and the stream's first attempt waits for the second.
		synctest.Wait()
		var err error
		returned := false
		select {
		case err = <-sent:
			returned = true
			t.Error("Send returned before the GET stream's first request read its token")
		default:
		}
		close(auth.gate)
		synctest.Wait()
		if !returned {
			select {
			case err = <-sent:
			default:
				t.Fatal("Send did not return once the GET stream read its token")
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := transport.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
