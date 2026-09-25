package ai

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// baseHTTPTransport unwraps the provider retry transport that streamingHTTPClient
// now wraps around the shaped *http.Transport, so shape assertions inspect the
// base transport.
func baseHTTPTransport(t *testing.T, c *http.Client) *http.Transport {
	t.Helper()
	transport := c.Transport
	for {
		switch wrapped := transport.(type) {
		case *retryTransport:
			transport = wrapped.base
		case *nodeFetchTransport:
			transport = wrapped.base
		default:
			tr, ok := transport.(*http.Transport)
			if !ok {
				t.Fatal("base transport is not *http.Transport")
			}
			return tr
		}
	}
}

func TestStreamingHTTPClient_MatchesUpstreamHTTPDispatcherShape(t *testing.T) {
	c := streamingHTTPClient()
	tr := baseHTTPTransport(t, c)
	if tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be false to match upstream allowH2=false")
	}
	if tr.Proxy == nil {
		t.Fatal("Proxy should honor environment proxy settings")
	}
}

func TestStreamingHTTPClient_InsecureTLSIsExplicit(t *testing.T) {
	if baseHTTPTransport(t, newStreamingHTTPClient(false)).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("default client skips TLS verification")
	}
	if !baseHTTPTransport(t, newStreamingHTTPClient(true)).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("insecure client verifies TLS certificates")
	}
}

func TestStreamingHTTPClient_NoTimeout(t *testing.T) {
	c := streamingHTTPClient()
	if c.Timeout != 0 {
		t.Errorf("client timeout should be 0 for SSE streaming, got %v", c.Timeout)
	}
}

func TestStreamingHTTPClient_WrapsRetryTransport(t *testing.T) {
	transport, ok := streamingHTTPClient().Transport.(*retryTransport)
	if !ok {
		t.Fatal("streamingHTTPClient should wrap the retry transport for the providers upstream wraps")
	}
	if _, ok := transport.base.(*nodeFetchTransport); !ok {
		t.Error("streamingHTTPClient should map Go transport failures to Node fetch errors")
	}
	// mistral opts out: upstream does not wrap mistral-conversations, so its
	// client must not carry the retry transport.
	nodeTransport, ok := streamingHTTPClientNoRetry().Transport.(*nodeFetchTransport)
	if !ok {
		t.Fatal("streamingHTTPClientNoRetry should map Go transport failures to Node fetch errors")
	}
	if _, ok := nodeTransport.base.(*retryTransport); ok {
		t.Error("streamingHTTPClientNoRetry (mistral) must not wrap the retry transport")
	}
}

func TestStreamingHTTPClient_KeepAlivePool(t *testing.T) {
	c := streamingHTTPClient()
	tr := baseHTTPTransport(t, c)
	if tr.MaxIdleConnsPerHost < 4 {
		t.Errorf("MaxIdleConnsPerHost should be >= 4, got %d", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout < 90_000_000_000 { // 90s in ns
		t.Errorf("IdleConnTimeout too short: %v", tr.IdleConnTimeout)
	}
}

func TestConfigureHTTPDispatcher_UpdatesFutureClients(t *testing.T) {
	t.Cleanup(func() {
		if err := ConfigureHTTPDispatcher(DefaultHTTPIdleTimeoutMs); err != nil {
			t.Fatalf("restore default ConfigureHTTPDispatcher: %v", err)
		}
	})

	if err := ConfigureHTTPDispatcher(12_345); err != nil {
		t.Fatalf("ConfigureHTTPDispatcher: %v", err)
	}
	if got := ConfiguredHTTPIdleTimeoutMs(); got != 12_345 {
		t.Fatalf("ConfiguredHTTPIdleTimeoutMs() = %d, want 12345", got)
	}

	c := streamingHTTPClient()
	tr := baseHTTPTransport(t, c)
	if tr.ResponseHeaderTimeout != 12345*time.Millisecond {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, 12345*time.Millisecond)
	}
	if tr.DialContext == nil {
		t.Fatal("DialContext should be configured")
	}
}

func TestConfigureHTTPDispatcher_RejectsNegativeTimeout(t *testing.T) {
	before := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(-1); err == nil {
		t.Fatal("expected error for negative timeout")
	}
	if got := ConfiguredHTTPIdleTimeoutMs(); got != before {
		t.Fatalf("ConfiguredHTTPIdleTimeoutMs() changed on error: got %d want %d", got, before)
	}
}

func TestIdleTimeoutConn_SetsDeadlinesFromConfiguredTimeout(t *testing.T) {
	var readDeadline, writeDeadline time.Time
	base := &recordingConn{
		setReadDeadline: func(t time.Time) error {
			readDeadline = t
			return nil
		},
		setWriteDeadline: func(t time.Time) error {
			writeDeadline = t
			return nil
		},
	}
	conn := &idleTimeoutConn{
		Conn:    base,
		timeout: func() time.Duration { return 50 * time.Millisecond },
	}

	readDone := make(chan struct{})
	base.read = func(p []byte) (int, error) {
		close(readDone)
		return 0, net.ErrClosed
	}
	if _, err := conn.Read(nil); err == nil {
		t.Fatal("expected read error")
	}
	<-readDone
	if readDeadline.IsZero() {
		t.Fatal("Read did not set a deadline")
	}

	writeDone := make(chan struct{})
	base.write = func(p []byte) (int, error) {
		close(writeDone)
		return 0, net.ErrClosed
	}
	if _, err := conn.Write(nil); err == nil {
		t.Fatal("expected write error")
	}
	<-writeDone
	if writeDeadline.IsZero() {
		t.Fatal("Write did not set a deadline")
	}
}

type recordingConn struct {
	net.Conn
	read             func([]byte) (int, error)
	write            func([]byte) (int, error)
	setReadDeadline  func(time.Time) error
	setWriteDeadline func(time.Time) error
}

func (c *recordingConn) Read(p []byte) (int, error) {
	if c.read != nil {
		return c.read(p)
	}
	return 0, net.ErrClosed
}

func (c *recordingConn) Write(p []byte) (int, error) {
	if c.write != nil {
		return c.write(p)
	}
	return 0, net.ErrClosed
}

func (c *recordingConn) SetReadDeadline(t time.Time) error {
	if c.setReadDeadline != nil {
		return c.setReadDeadline(t)
	}
	return nil
}

func (c *recordingConn) SetWriteDeadline(t time.Time) error {
	if c.setWriteDeadline != nil {
		return c.setWriteDeadline(t)
	}
	return nil
}
