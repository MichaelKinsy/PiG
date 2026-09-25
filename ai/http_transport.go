package ai

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const DefaultHTTPIdleTimeoutMs = 300_000

var configuredHTTPIdleTimeoutMs atomic.Int64

func init() {
	configuredHTTPIdleTimeoutMs.Store(DefaultHTTPIdleTimeoutMs)
}

// ConfigureHTTPDispatcher updates the package-global idle timeout used by new
// streaming HTTP clients. Zero disables the per-read/per-write idle deadline.
// Mirrors upstream's configureHttpDispatcher(settingsManager.getHttpIdleTimeoutMs()).
func ConfigureHTTPDispatcher(timeoutMs int) error {
	if timeoutMs < 0 {
		return fmt.Errorf("invalid HTTP idle timeout: %d", timeoutMs)
	}
	configuredHTTPIdleTimeoutMs.Store(int64(timeoutMs))
	return nil
}

// ConfiguredHTTPIdleTimeoutMs returns the currently configured package-global
// HTTP idle timeout used for new streaming clients.
func ConfiguredHTTPIdleTimeoutMs() int {
	return int(configuredHTTPIdleTimeoutMs.Load())
}

func configuredHTTPIdleTimeout() time.Duration {
	ms := configuredHTTPIdleTimeoutMs.Load()
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

type idleTimeoutConn struct {
	net.Conn
	timeout func() time.Duration
}

func (c *idleTimeoutConn) Read(p []byte) (int, error) {
	c.setReadDeadline()
	return c.Conn.Read(p)
}

func (c *idleTimeoutConn) Write(p []byte) (int, error) {
	c.setWriteDeadline()
	return c.Conn.Write(p)
}

func (c *idleTimeoutConn) setReadDeadline() {
	timeout := c.timeout()
	if timeout <= 0 {
		_ = c.SetReadDeadline(time.Time{})
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(timeout))
}

func (c *idleTimeoutConn) setWriteDeadline() {
	timeout := c.timeout()
	if timeout <= 0 {
		_ = c.SetWriteDeadline(time.Time{})
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
}

// streamingHTTPClient builds the HTTP client used for LLM SSE streaming.
//
// Faithfulness notes:
//
//   - ProxyFromEnvironment mirrors upstream's EnvHttpProxyAgent so provider
//     traffic honors HTTP(S)_PROXY and NO_PROXY.
//   - ForceAttemptHTTP2 stays false to match upstream allowH2=false.
//   - Configurable per-read/per-write idle deadlines mirror upstream's
//     bodyTimeout/headersTimeout.
//   - No overall client Timeout: SSE streams are long-lived; the caller
//     controls cancellation via context.
func streamingHTTPClient() *http.Client {
	return newStreamingHTTPClient(false)
}

// newStreamingHTTPClient builds the streaming client wrapped with the provider
// retry transport (retry.provider.maxRetries / maxRetryDelayMs). The retry
// policy is inert at the default maxRetries=0. When insecure is true, server
// TLS certificate verification is skipped. Insecure is an opt-in for
// OpenAI-compatible endpoints behind self-signed or internal-CA certificates
// (on-prem gateways); it is never the default and is set only when a provider
// is explicitly configured insecure.
func newStreamingHTTPClient(insecure bool) *http.Client {
	client := baseStreamingHTTPClient(insecure)
	client.Transport = &retryTransport{base: &nodeFetchTransport{base: client.Transport}}
	return client
}

// streamingHTTPClientNoRetry builds the streaming client without the provider
// retry transport, for providers upstream does not wrap with
// retryProviderRequest. Only mistral-conversations is excluded: retrying it
// when maxRetries>0 would diverge from upstream, which leaves it on the SDK's
// own (disabled) retries.
func streamingHTTPClientNoRetry() *http.Client {
	client := baseStreamingHTTPClient(false)
	client.Transport = &nodeFetchTransport{base: client.Transport}
	return client
}

func baseStreamingHTTPClient(insecure bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 15 * time.Second,
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,

			// Connection establishment.
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return &idleTimeoutConn{Conn: conn, timeout: configuredHTTPIdleTimeout}, nil
			},

			// TLS: enable session resumption for 0-RTT on reconnect.
			TLSClientConfig: &tls.Config{
				// Go 1.26 defaults to TLS 1.3 with post-quantum ML-KEM key
				// exchange. Session tickets enable 0-RTT resumption.
				//nolint:gosec // G402: opt-in per-provider insecure TLS for
				// self-signed/internal-CA on-prem endpoints. Never the default.
				// pig additive (D36): opt-in TLS-skip for self-signed/internal-CA endpoints.
				InsecureSkipVerify: insecure,
			},
			TLSHandshakeTimeout: 10 * time.Second,

			// Upstream uses undici EnvHttpProxyAgent({ allowH2: false }).
			ForceAttemptHTTP2: false,

			// Connection pool: keep connections warm between turns.
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     120 * time.Second,

			// Match upstream's configurable header idle timeout. Body idleness is
			// enforced by idleTimeoutConn on every read/write operation.
			ResponseHeaderTimeout: configuredHTTPIdleTimeout(),

			// Compression: enable transparent gzip for non-streaming responses
			// (auth token requests, model listings). SSE responses are chunked
			// and typically not gzipped, so this is harmless.
			DisableCompression: false,
		},
		// No overall timeout: SSE streams are long-lived. The caller
		// controls cancellation via context.Context.
	}
}
