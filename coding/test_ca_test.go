package coding

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA is the throwaway certificate authority TestMain makes the process trust through SSL_CERT_FILE (on the platforms that load their roots from the
// environment). It signs only the leaf certificates of the servers that name it, so every other httptest TLS server keeps presenting net/http/httptest's
// built-in certificate, which no client of this package trusts.
var testCA *testCertificateAuthority

type testCertificateAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// trustTestCertificateAuthority generates the CA, writes its certificate (and nothing else) to a file under dir and points SSL_CERT_FILE at it, before any
// client of the process has loaded the system roots.
func trustTestCertificateAuthority(dir string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pig coding test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	file := filepath.Join(dir, "test-ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return err
	}
	testCA = &testCertificateAuthority{cert: cert, key: key}
	return os.Setenv("SSL_CERT_FILE", file)
}

// newTLSServer starts a TLS server whose leaf certificate (127.0.0.1, ::1, localhost) the test CA signed, so the process's default client trusts it.
func (ca *testCertificateAuthority) newTLSServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "pig coding test server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// Guard: a plain httptest TLS server presents net/http/httptest's built-in certificate, which the default client configuration of this test binary must reject.
// Trusting that certificate (as a past TestMain did through SSL_CERT_FILE) would make every TLS test server of the package pass the handshake and the tests
// that need a secure request to be refused pass for the wrong reason.
func TestDefaultClientRejectsTheHTTPTestCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	response, err := http.Get(server.URL)
	if err == nil {
		_ = response.Body.Close()
		t.Fatalf("the default client accepted the httptest certificate: status %s", response.Status)
	}
	var unknown x509.UnknownAuthorityError
	if !strings.Contains(err.Error(), "certificate") && !errors.As(err, &unknown) {
		t.Fatalf("the default client failed with %v, want a certificate verification error", err)
	}
	if systemRootsFromEnvironment {
		trusted := testCA.newTLSServer(t, http.NotFoundHandler())
		response, err := http.Get(trusted.URL)
		if err != nil {
			t.Fatalf("the default client rejected a server certified by the test CA: %v", err)
		}
		_ = response.Body.Close()
	}
}
