package ai

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Undici8.10.2 lib/core/connect.js clears its connection timer on secureConnect; the configured HTTP body/header idle timeout does not govern that handshake.
func TestHTTPIdleTimeoutBeginsAfterTLS(t *testing.T) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ConfigureHTTPDispatcher(previous); err != nil {
			t.Fatal(err)
		}
	})
	for _, mode := range []string{"direct", "CONNECT", "dispatcher-CONNECT"} {
		t.Run(mode, func(t *testing.T) {
			proxy := mode != "direct"
			backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "connected") }))
			defer backend.Close()
			target := backend.Listener.Addr().String()
			var workers sync.WaitGroup
			var connected atomic.Bool
			relay := func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				time.Sleep(400 * time.Millisecond)
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = upstream.Close() }()
				var copies sync.WaitGroup
				copies.Go(func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close() })
				copies.Go(func() { _, _ = io.Copy(conn, upstream); _ = conn.Close() })
				copies.Wait()
			}
			client := streamingHTTPClient()
			transport := baseHTTPTransport(t, client)
			transport.Proxy = nil
			roots := x509.NewCertPool()
			roots.AddCert(backend.Certificate())
			transport.TLSClientConfig.RootCAs = roots
			address := backend.URL
			stop := func() {}
			closeIdle := transport.CloseIdleConnections
			if proxy {
				connections := make(chan net.Conn)
				stopped := make(chan struct{})
				workers.Go(func() {
					select {
					case conn := <-connections:
						relay(conn)
					case <-stopped:
					}
				})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "CONNECT" {
						http.Error(w, "CONNECT required", 400)
						return
					}
					connected.Store(true)
					conn, buffer, err := w.(http.Hijacker).Hijack()
					if err != nil {
						return
					}
					_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
					if err := buffer.Flush(); err != nil {
						_ = conn.Close()
						return
					}
					select {
					case connections <- conn:
					case <-stopped:
						_ = conn.Close()
					}
				}))
				stop = func() { close(stopped); server.Close() }
				proxyURL, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				transport.Proxy = http.ProxyURL(proxyURL)
				if mode == "dispatcher-CONNECT" {
					transport.Proxy = nil
					manual, err := proxyOriginTransport(transport, server.URL)
					if err != nil {
						t.Fatal(err)
					}
					client.Transport = &nodeFetchTransport{base: manual, connectionIdle: true}
					closeIdle = manual.CloseIdleConnections
				}
			} else {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				stop = func() { _ = listener.Close() }
				address = "https://" + listener.Addr().String()
				workers.Go(func() {
					conn, err := listener.Accept()
					if err == nil {
						relay(conn)
					}
				})
			}
			defer func() { closeIdle(); stop(); workers.Wait() }()
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("HTTP idle timeout interrupted connection establishment: %v", err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != "connected" {
				t.Fatalf("body=%q error=%v", body, err)
			}
			if proxy && !connected.Load() {
				t.Fatal("HTTPS bypassed its CONNECT route")
			}
			if response.TLS == nil || !response.TLS.HandshakeComplete {
				t.Fatal("transport lost TLS connection state")
			}
		})
	}
}
