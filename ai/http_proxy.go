package ai

// Ports packages/coding-agent/src/core/http-dispatcher.ts

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// ApplyHTTPProxySettings sets absent HTTP_PROXY and HTTPS_PROXY variables. An explicitly empty environment value remains authoritative.
func ApplyHTTPProxySettings(httpProxy string) error {
	proxy := strings.TrimSpace(httpProxy)
	if proxy == "" {
		return nil
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, proxy); err != nil {
				return err
			}
		}
	}
	return nil
}

func proxyEnv(lower, upper string) string {
	if value, ok := os.LookupEnv(lower); ok {
		return value
	}
	return os.Getenv(upper)
}

type proxyRequestContextKey struct{}

type proxyTunnelTransport struct {
	direct                *http.Transport
	httpProxy, httpsProxy *http.Transport
	httpError, httpsError error
}

func newProxyTunnelTransport(direct *http.Transport) *proxyTunnelTransport {
	transport := &proxyTunnelTransport{direct: direct}
	httpProxy := proxyEnv("http_proxy", "HTTP_PROXY")
	httpsProxy := proxyEnv("https_proxy", "HTTPS_PROXY")
	if httpsProxy == "" {
		httpsProxy = httpProxy
	}
	transport.httpProxy, transport.httpError = proxyOriginTransport(direct, httpProxy)
	if httpsProxy == httpProxy {
		transport.httpsProxy, transport.httpsError = transport.httpProxy, transport.httpError
	} else {
		transport.httpsProxy, transport.httpsError = proxyOriginTransport(direct, httpsProxy)
	}
	return transport
}

func (t *proxyTunnelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport, err := t.httpProxy, t.httpError
	if request.URL.Scheme == "https" {
		transport, err = t.httpsProxy, t.httpsError
	}
	if proxyBypassed(request.URL) {
		return t.direct.RoundTrip(request)
	}
	if err != nil {
		return nil, err
	}
	if transport == nil {
		transport = t.direct
	}
	// net/http detaches dial cancellation for connection reuse. Keep the requesting operation's lifetime attached while establishing its CONNECT tunnel.
	ctx := context.WithValue(request.Context(), proxyRequestContextKey{}, request.Context())
	return transport.RoundTrip(request.WithContext(ctx))
}
func (t *proxyTunnelTransport) CloseIdleConnections() {
	t.direct.CloseIdleConnections()
	if t.httpProxy != nil {
		t.httpProxy.CloseIdleConnections()
	}
	if t.httpsProxy != nil && t.httpsProxy != t.httpProxy {
		t.httpsProxy.CloseIdleConnections()
	}
}

// proxyBypassed mirrors EnvHttpProxyAgent's NO_PROXY host/subdomain and optional port matching, including explicit loopback entries. Loopback is not bypassed implicitly.
func proxyBypassed(target *url.URL) bool {
	value := proxyEnv("no_proxy", "NO_PROXY")
	if value == "*" {
		return true
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		name, entryPort := entry, ""
		if h, p, err := net.SplitHostPort(entry); err == nil {
			name, entryPort = h, p
		} else if strings.Count(entry, ":") == 1 {
			if h, p, ok := strings.Cut(entry, ":"); ok {
				if _, err := strconv.Atoi(p); err == nil {
					name, entryPort = h, p
				}
			}
		}
		name = strings.Trim(name, "[]")
		name = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(name, "*."), "."), "."))
		if entryPort != "" && entryPort != "0" && entryPort != port {
			continue
		}
		if host == name || strings.HasSuffix(host, "."+name) {
			return true
		}
	}
	return false
}

func proxyOriginTransport(direct *http.Transport, raw string) (*http.Transport, error) {
	if raw == "" {
		return nil, nil
	}
	proxy, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "https") {
		return nil, fmt.Errorf("invalid HTTP proxy URL %q", raw)
	}
	transport := direct.Clone()
	dial := direct.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if requestCtx, ok := ctx.Value(proxyRequestContextKey{}).(context.Context); ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			stop := context.AfterFunc(requestCtx, cancel)
			defer func() { stop(); cancel() }()
		}
		port := proxy.Port()
		if port == "" {
			if proxy.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		var conn net.Conn
		var err error
		proxyAddress := net.JoinHostPort(proxy.Hostname(), port)
		if proxy.Scheme == "https" && direct.DialTLSContext != nil {
			conn, err = direct.DialTLSContext(ctx, network, proxyAddress)
		} else {
			conn, err = dial(ctx, network, proxyAddress)
		}
		if err != nil {
			return nil, err
		}
		success := false
		defer func() {
			if !success {
				_ = conn.Close()
			}
		}()
		rawConn := conn
		stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
		defer stop()
		if proxy.Scheme == "https" && direct.DialTLSContext == nil {
			tlsConn := tls.Client(conn, &tls.Config{ServerName: proxy.Hostname()})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				return nil, err
			}
			conn = tlsConn
		}
		request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if proxy.User != nil {
			password, _ := proxy.User.Password()
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)))
		}
		if err := request.Write(conn); err != nil {
			return nil, err
		}
		// Undici's Client inherits Node's 16 KiB response-header limit. The limit applies only to CONNECT headers, not to the tunneled response body.
		limited := &io.LimitedReader{R: conn, N: 16_384}
		reader := bufio.NewReader(limited)
		//nolint:bodyclose // A successful CONNECT transfers the live socket to the transport; closing its synthetic body would drain or terminate the tunnel. Error paths close conn above.
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("proxy CONNECT: %s", response.Status)
		}
		limited.N = math.MaxInt64
		success = true
		return &proxyBufferedConn{Conn: conn, reader: reader}, nil
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := transport.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		// The target TLS upgrade starts after CONNECT and has a distinct connector budget.
		return dialStreamingTLS(ctx, network, address, func(context.Context, string, string) (net.Conn, error) { return conn, nil }, transport.TLSClientConfig, defaultHTTPConnectTimeout)
	}
	return transport, nil
}

type proxyBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *proxyBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
