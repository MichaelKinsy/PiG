package ai

import (
	"net"
	"net/http"
	"net/http/httptrace"
	"testing"
)

type dialPhaseTimeout struct{}

func (dialPhaseTimeout) Error() string   { return "i/o timeout" }
func (dialPhaseTimeout) Timeout() bool   { return true }
func (dialPhaseTimeout) Temporary() bool { return true }

// timeoutAfterTrace fails every request with a net timeout. With connected set it first reports the connection through GotConn,
// as net/http does before it writes the request.
type timeoutAfterTrace struct{ connected bool }

func (rt timeoutAfterTrace) RoundTrip(request *http.Request) (*http.Response, error) {
	if rt.connected {
		client, server := net.Pipe()
		defer func() { _ = client.Close(); _ = server.Close() }()
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.GotConn != nil {
			trace.GotConn(httptrace.GotConnInfo{Conn: client})
		}
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: dialPhaseTimeout{}}
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: dialPhaseTimeout{}}
}

// openai 7.19 client.js:820-834 gives the long headers-timeout text only when fetch's cause is undici's HeadersTimeoutError
// (UND_ERR_HEADERS_TIMEOUT), which undici raises on an established connection. A timeout while connecting is undici's
// ConnectTimeoutError, which the SDK reports as "Request timed out.".
func TestOpenAISDKTimeoutBeforeTheConnectionIsTheShortTimeoutText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		connected bool
		want      string
	}{
		{"dial timeout", false, "Request timed out."},
		{"timeout on the connection", true, wantOpenAISDKHeadersTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &nodeFetchTransport{base: timeoutAfterTrace{connected: tc.connected}, connectionIdle: true}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:9/v1/chat/completions", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err == nil {
				_ = response.Body.Close()
				t.Fatal("RoundTrip succeeded")
			}
			got := openAISDKTransportError(t.Context(), err)
			if got == nil || got.Error() != tc.want {
				t.Fatalf("SDK error = %v, want %q", got, tc.want)
			}
		})
	}
}
