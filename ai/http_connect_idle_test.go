package ai

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The idle timeout applies to the connection once it is established, TLS included: a response that stops after it began ends the request with the
// timeout's error and not with the caller's deadline. The deadline is armed after GotConn, so the test must hold the request's way to the server
// open, with no clock in it: the dispatcher reads the configured timeout whenever it arms a deadline (ConfigureHTTPDispatcher applies to
// connections already open, as Pi's reinstalled dispatcher does), so the request goes out under an hour (the transport's own header timeout is the
// configured one when the transport is built), the server holds it until the test has configured 100 ms, and the response starts after that. The
// deadline that ends the stalled body is armed by the read after the headers, under 100 ms; no deadline that can end the request earlier is.
func TestHTTPIdleTimeoutStillAppliesAfterTLS(t *testing.T) {
	const idleTimeoutMs, idleHoldMs = 100, 3_600_000
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(idleHoldMs); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ConfigureHTTPDispatcher(previous); err != nil {
			t.Fatal(err)
		}
	}()
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseResponse := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		// The body stops here: nothing more is written until the client goes away.
		<-r.Context().Done()
	}))
	defer server.Close()
	defer releaseResponse()
	client := streamingHTTPClient()
	transport := baseHTTPTransport(t, client)
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	ctx, cancel := context.WithCancel(testbudget.Context(t))
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	type answer struct {
		response *http.Response
		err      error
	}
	answered := make(chan answer, 1)
	go func() {
		response, err := client.Do(request) //nolint:bodyclose // the response is handed to the test goroutine below, which closes its body
		answered <- answer{response, err}
	}()
	select {
	case <-arrived:
	case <-t.Context().Done():
		t.Fatal("the test ended while it waited for the request to reach the server")
	}
	if err := ConfigureHTTPDispatcher(idleTimeoutMs); err != nil {
		t.Fatal(err)
	}
	releaseResponse()
	var got answer
	select {
	case got = <-answered:
	case <-t.Context().Done():
		t.Fatal("the test ended while it waited for the response headers")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer func() {
		if err := got.response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(got.response.Body, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(got.response.Body); err == nil || ctx.Err() != nil {
		t.Fatalf("idle timeout lost or replaced by the budget: error=%v context=%v", err, ctx.Err())
	}
}

func BenchmarkStreamingHTTPSConnectionReuse(b *testing.B) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer server.Close()
	client := streamingHTTPClient()
	transport := baseHTTPTransport(b, client)
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	b.ReportAllocs()
	for b.Loop() {
		response, err := client.Get(server.URL)
		if err != nil {
			b.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			b.Fatalf("read=%v close=%v", readErr, closeErr)
		}
	}
}
