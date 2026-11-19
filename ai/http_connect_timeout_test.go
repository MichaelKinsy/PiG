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

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Undici 8.12.4 lib/core/connect.js clears its connection timer on secureConnect; the configured HTTP body/header idle timeout does not govern that handshake.
//
// The relay holds the connection, and with it the TLS handshake, until the test releases it, which it does after the handshake has waited three
// times the idle timeout under that timeout: a product that governed the handshake would have ended it at one. The wait is a lower bound, so a stall of
// the host only makes it longer. Once the handshake is held the test configures an hour, as the stalled-body test does, because the deadline that
// begins at GotConn is armed with whatever is configured then and the response that follows is not what this test is about; the transport itself is
// built under an hour for the same reason (its header timeout is the configured one when it is built).
func TestHTTPIdleTimeoutBeginsAfterTLS(t *testing.T) {
	const idleTimeoutMs, idleHoldMs = 100, 3_600_000
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(idleHoldMs); err != nil {
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
			held := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseRelay := func() { releaseOnce.Do(func() { close(release) }) }
			relay := func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				held <- struct{}{}
				<-release
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
			ctx, cancel := context.WithCancel(testbudget.Context(t))
			defer func() { cancel(); releaseRelay(); closeIdle(); stop(); workers.Wait() }()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := ConfigureHTTPDispatcher(idleTimeoutMs); err != nil {
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
			case <-held:
			case <-t.Context().Done():
				t.Fatal("the test ended while it waited for the relay to hold the connection")
			}
			time.Sleep(3 * idleTimeoutMs * time.Millisecond)
			if err := ConfigureHTTPDispatcher(idleHoldMs); err != nil {
				t.Fatal(err)
			}
			releaseRelay()
			var got answer
			select {
			case got = <-answered:
			case <-t.Context().Done():
				t.Fatal("the test ended while it waited for the response")
			}
			if got.err != nil {
				t.Fatalf("HTTP idle timeout interrupted connection establishment: %v", got.err)
			}
			response := got.response
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
