package ai

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A TLS handshake whose certificate does not name the host fails in Node's undici with a cause chain whose deepest error is tls.checkServerIdentity's
// (ERR_TLS_CERT_ALTNAME_INVALID). Pi appends it to every provider's message the same way: the three SDK kinds read "Connection error.: <cause>", the Google
// kind "fetch failed: <cause>" (the Google kind is not covered here: PiG's google Stream returns the transport error from its setup, without the detail; RUN against pi b4afdddf44e0 with a certificate for other.test, alt.test and 10.9.8.7 on https://localhost:<port>, 2026-11-14).
const tlsMismatchDetail = "Hostname/IP does not match certificate's altnames: Host: localhost. is not in the cert's altnames: DNS:other.test, DNS:alt.test, IP Address:10.9.8.7 (ERR_TLS_CERT_ALTNAME_INVALID)"

// TestTLSHostnameMismatchShowsNodesCause runs in a child process that trusts the server's certificate through SSL_CERT_FILE: Go reads the system roots once
// per process, so a test of the shared binary cannot add a root after another test has made a TLS request.
func TestTLSHostnameMismatchShowsNodesCause(t *testing.T) {
	if os.Getenv("PIG_TLS_MISMATCH_CHILD") == "" {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "other.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true,
			DNSNames: []string{"other.test", "alt.test"}, IPAddresses: []net.IP{net.ParseIP("10.9.8.7")}}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		if err := os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "key.pem"), keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestTLSHostnameMismatchShowsNodesCause$", "-test.v")
		cmd.Env = append(os.Environ(), "PIG_TLS_MISMATCH_CHILD="+dir, "SSL_CERT_FILE="+filepath.Join(dir, "cert.pem"), "SSL_CERT_DIR="+dir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, output)
		}
		return
	}
	dir := os.Getenv("PIG_TLS_MISMATCH_CHILD")
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := "https://localhost:" + address.Port()
	for _, tc := range []struct {
		api  API
		want string
	}{
		{APIAnthropicMessages, "Connection error.: " + tlsMismatchDetail},
		{APIOpenAICompletions, "Connection error.: " + tlsMismatchDetail},
		{APIOpenAIResponses, "Connection error.: " + tlsMismatchDetail},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: tc.api}, base, false)
			defer func() { _ = provider.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{MaxRetries: new(0)})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason != StopReasonError || result.ErrorMessage != tc.want {
				t.Fatalf("stop reason %s, message %q; want %q", result.StopReason, result.ErrorMessage, tc.want)
			}
			if strings.Contains(result.ErrorMessage, "x509") {
				t.Fatalf("Go text leaked: %q", result.ErrorMessage)
			}
		})
	}
}
