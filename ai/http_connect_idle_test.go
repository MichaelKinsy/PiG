package ai

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPIdleTimeoutStillAppliesAfterTLS(t *testing.T) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(100); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ConfigureHTTPDispatcher(previous); err != nil {
			t.Fatal(err)
		}
	}()
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "last")
	}))
	defer server.Close()
	defer close(release)
	client := streamingHTTPClient()
	transport := baseHTTPTransport(t, client)
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err == nil || ctx.Err() != nil {
		t.Fatalf("idle timeout lost or replaced by overall timeout: error=%v context=%v", err, ctx.Err())
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
