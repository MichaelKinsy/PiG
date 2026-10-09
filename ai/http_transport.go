package ai

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/bodyreadhook"
)

type fetchTransport struct{ client *http.Client }

func (transport fetchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	client := transport.client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, standard := base.(*http.Transport); standard && request.URL.Opaque != "" {
		copy := *client
		copy.Transport = originFormTransport{base: base}
		client = &copy
	}
	return client.Do(request)
}

type originFormTransport struct{ base http.RoundTripper }

func (transport originFormTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.base.RoundTrip(originFormRequest(request))
}

// originFormRequest preserves the raw WHATWG path when a standard Go transport serializes a request, without changing the URL observed by a caller-supplied fetch function.
func originFormRequest(request *http.Request) *http.Request {
	prefix := "//" + request.URL.Host
	if !strings.HasPrefix(request.URL.Opaque, prefix+"/") {
		return request
	}
	copy := new(*request)
	url := *request.URL
	url.Opaque = strings.TrimPrefix(url.Opaque, prefix)
	copy.URL = &url
	return copy
}

// providerHTTPClient replaces only HTTP execution, keeping the provider's retry and transport-error policy. The caller-owned client decides redirects. Neither client nor the process default is mutated.
func providerHTTPClient(configured, fetch *http.Client) *http.Client {
	if fetch == nil {
		return configured
	}
	client := *configured
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := &nodeFetchTransport{base: fetchTransport{client: fetch}}
	if retry, ok := configured.Transport.(*retryTransport); ok {
		transport.base = &providerRequestTransport{base: transport.base}
		wrapped := *retry
		wrapped.base = transport
		client.Transport = &wrapped
	} else {
		client.Transport = transport
	}
	return &client
}

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
		return fmt.Errorf("Invalid HTTP idle timeout: %d", timeoutMs)
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
	// ready is set by the HTTP transport only after connection establishment, including proxy CONNECT and TLS, completes. Nil keeps standalone wrappers active.
	ready    *atomic.Bool
	bodyRead bodyReadOwner
}

func (c *idleTimeoutConn) Read(p []byte) (int, error) {
	c.bodyRead.pending()
	c.setReadDeadline()
	if !bodyreadhook.BeforeRead(c.Conn) {
		return c.Conn.Read(p)
	}
	n, err := c.Conn.Read(p)
	bodyreadhook.AfterRead(c.LocalAddr().String(), n)
	return n, err
}

func (c *idleTimeoutConn) Write(p []byte) (int, error) {
	c.setWriteDeadline()
	return c.Conn.Write(p)
}

func (c *idleTimeoutConn) setReadDeadline() {
	if c.ready != nil && !c.ready.Load() {
		return
	}
	timeout := c.timeout()
	if timeout <= 0 {
		_ = c.SetReadDeadline(time.Time{})
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(timeout))
}

func (c *idleTimeoutConn) setWriteDeadline() {
	if c.ready != nil && !c.ready.Load() {
		return
	}
	timeout := c.timeout()
	if timeout <= 0 {
		_ = c.SetWriteDeadline(time.Time{})
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
}

// streamingHTTPClient builds the provider HTTP/1.1 client with environment proxy routing. Connection establishment has the pinned Undici budget; configurable read/write/header idle deadlines apply after GotConn. There is no overall timeout for long-lived SSE requests; the caller context owns cancellation.
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
	client.Transport = &retryTransport{base: &nodeFetchTransport{base: &providerRequestTransport{base: client.Transport}, connectionIdle: true}}
	return client
}

// streamingHTTPClientNoRetry builds the streaming client without the provider
// retry transport, for providers upstream does not wrap with
// retryProviderRequest. Only mistral-conversations is excluded: retrying it
// when maxRetries>0 would diverge from upstream, which leaves it on the SDK's
// own (disabled) retries.
func streamingHTTPClientNoRetry() *http.Client {
	client := baseStreamingHTTPClient(false)
	client.Transport = &nodeFetchTransport{base: client.Transport, connectionIdle: true}
	return client
}

// upstream: node_modules/undici/lib/core/connect.js:buildConnector
const defaultHTTPConnectTimeout = 10 * time.Second

func activateHTTPIdleTimeout(conn net.Conn) {
	for {
		switch current := conn.(type) {
		case *idleTimeoutConn:
			if current.ready != nil {
				current.ready.Store(true)
			}
			return
		case *tls.Conn:
			conn = current.NetConn()
		case *proxyBufferedConn:
			conn = current.Conn
		default:
			return
		}
	}
}

// dialStreamingTLS uses one connection budget through DNS, TCP and secureConnect. HTTP idleness starts only when the transport publishes GotConn.
func dialStreamingTLS(ctx context.Context, network, address string, dial func(context.Context, string, string) (net.Conn, error), config *tls.Config, timeout time.Duration) (net.Conn, error) {
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dial(connectCtx, network, address)
	if err != nil {
		return nil, err
	}
	tracked := &idleTimeoutConn{Conn: conn, timeout: configuredHTTPIdleTimeout, ready: new(atomic.Bool)}
	options := config.Clone()
	if options == nil {
		options = &tls.Config{}
	}
	if options.ServerName == "" {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		options.ServerName = host
	}
	secure := tls.Client(tracked, options)
	if err := secure.HandshakeContext(connectCtx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return secure, nil
}

func streamingDialer(dialer net.Dialer) *net.Dialer {
	// upstream: packages/coding-agent/src/core/http-dispatcher.ts:DEFAULT_AUTO_SELECT_FAMILY_ATTEMPT_TIMEOUT_MS
	dialer.FallbackDelay = 2000 * time.Millisecond
	return &dialer
}

func baseStreamingHTTPClient(insecure bool) *http.Client {
	dialer := streamingDialer(net.Dialer{
		KeepAlive: 15 * time.Second,
	})
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			connectCtx, cancel := context.WithTimeout(ctx, defaultHTTPConnectTimeout)
			defer cancel()
			conn, err := dialer.DialContext(connectCtx, network, address)
			if err != nil {
				return nil, err
			}
			return &idleTimeoutConn{Conn: conn, timeout: configuredHTTPIdleTimeout, ready: new(atomic.Bool)}, nil
		},
		TLSClientConfig: &tls.Config{
			//nolint:gosec // G402: opt-in per-provider insecure TLS for self-signed/internal-CA endpoints; never the default.
			// pig additive (D36): opt-in TLS-skip for self-signed/internal-CA endpoints.
			InsecureSkipVerify: insecure,
		},
		// A TLS upgrade after proxy CONNECT has its own connector budget, as in Undici. Direct TLS uses DialTLSContext's shared TCP/TLS budget.
		TLSHandshakeTimeout:   defaultHTTPConnectTimeout,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       120 * time.Second,
		ResponseHeaderTimeout: configuredHTTPIdleTimeout(),
		DisableCompression:    false,
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialStreamingTLS(ctx, network, address, dialer.DialContext, transport.TLSClientConfig, defaultHTTPConnectTimeout)
	}
	return &http.Client{Transport: newProxyTunnelTransport(transport)}
}
