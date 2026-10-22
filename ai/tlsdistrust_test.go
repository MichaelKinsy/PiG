package ai

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tlsdistrust"
	"github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

func compromisedTLSIntermediate(t *testing.T) *x509.Certificate {
	t.Helper()
	for _, certificate := range tlsdistrusttest.CrossSigned(t) {
		if tlsdistrusttest.IsTLS(certificate) {
			return certificate
		}
	}
	t.Fatal("no TLS intermediate in testdata")
	return nil
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(name, "")
	}
}

// A server certificate that the recovered key of a compromised intermediate signed verifies against a trust store that still carries the
// intermediate on a Go before 1.27.3, whose crypto/x509 refuses the same eight keys itself. The streaming transport, which every provider of the
// streaming client shares, must refuse it either way; the hook runs in the transport's own TLS configuration (TestStreamingTransportHookNamesTheCA).
func TestStreamingTransportRefusesACompromisedCA(t *testing.T) {
	clearProxyEnv(t)
	forged := tlsdistrusttest.Forge(t, compromisedTLSIntermediate(t))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{forged.Certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	transport, ok := newStreamingTransport(false).(*proxyTunnelTransport)
	if !ok {
		t.Fatalf("newStreamingTransport returned %T", newStreamingTransport(false))
	}
	transport.direct.TLSClientConfig.RootCAs = forged.Roots
	_, err := (&http.Client{Transport: transport}).Get(server.URL) //nolint:bodyclose // the request must fail
	if !tlsdistrusttest.IsRefusal(err) {
		t.Fatalf("GET through the streaming transport = %v, want a refusal", err)
	}
}

func TestStreamingTransportHookNamesTheCA(t *testing.T) {
	clearProxyEnv(t)
	transport, ok := newStreamingTransport(false).(*proxyTunnelTransport)
	if !ok {
		t.Fatalf("newStreamingTransport returned %T", newStreamingTransport(false))
	}
	issuer := compromisedTLSIntermediate(t)
	err := transport.direct.TLSClientConfig.VerifyConnection(tlsdistrusttest.BlockedState("api.example.test", tlsdistrusttest.EmSignRootG1(t), issuer))
	if !errors.Is(err, tlsdistrust.ErrDistrusted) || !strings.Contains(err.Error(), issuer.Subject.CommonName) {
		t.Fatalf("VerifyConnection on a chain through %s = %v, want ErrDistrusted naming it", issuer.Subject.CommonName, err)
	}
}

func TestCodexWebSocketTLSConfigChecksTheChain(t *testing.T) {
	config := codexWebSocketTLSConfig()
	if config.VerifyConnection == nil {
		t.Fatal("the WebSocket TLS config has no VerifyConnection hook")
	}
	err := config.VerifyConnection(tlsdistrusttest.BlockedState("chatgpt.com", tlsdistrusttest.EmSignRootG1(t), compromisedTLSIntermediate(t)))
	if !errors.Is(err, tlsdistrust.ErrDistrusted) {
		t.Fatalf("VerifyConnection on a chain through a compromised CA = %v, want ErrDistrusted", err)
	}
}

func TestStreamingTransportRefusesTheRemovedRoots(t *testing.T) {
	clearProxyEnv(t)
	transport, ok := newStreamingTransport(false).(*proxyTunnelTransport)
	if !ok {
		t.Fatalf("newStreamingTransport returned %T", newStreamingTransport(false))
	}
	for _, root := range tlsdistrusttest.RemovedRoots(t) {
		err := transport.direct.TLSClientConfig.VerifyConnection(tlsdistrusttest.BlockedState("api.example.test", tlsdistrusttest.EmSignRootG1(t), root))
		if !errors.Is(err, tlsdistrust.ErrDistrusted) {
			t.Errorf("a chain that ends at %s: VerifyConnection = %v, want ErrDistrusted", root.Subject.CommonName, err)
		}
	}
}
