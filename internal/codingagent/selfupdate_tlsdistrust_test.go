package codingagent

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

// The update transport CA sidecar adds a trust anchor for a private update server. Pointing it at a compromised eMudhra intermediate (or a
// root above it) still does not let a certificate that the recovered key signed through: Go 1.27.3's verifier and the hook check every verified chain.
func TestSelfUpdateRefusesACompromisedCAEvenWhenTheSidecarTrustsIt(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	var issuer = tlsdistrusttest.Intermediates(t)[0]
	for _, certificate := range tlsdistrusttest.Intermediates(t) {
		if tlsdistrusttest.IsTLS(certificate) {
			issuer = certificate
			break
		}
	}
	forged := tlsdistrusttest.Forge(t, issuer)
	sidecar := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Raw})
	if err := os.WriteFile(filepath.Join(ConfigRoot(), updateTransportCASidecarName), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("update"))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{forged.Certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	path, _, err := downloadBinaryToFile(context.Background(), &http.Client{}, server.URL, t.TempDir(), 1024)
	if err == nil {
		_ = os.Remove(path)
		t.Fatal("the download from a server whose chain includes a compromised CA succeeded")
	}
	if !tlsdistrusttest.IsRefusal(err) {
		t.Fatalf("error %q is not a refusal of the chain", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("the server saw %d requests; the handshake must fail first", n)
	}
}
