package tlsdistrust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

// TestBlocklist re-derives every digest in the source from the public certificates and checks that no entry is a stranger.
func TestBlocklist(t *testing.T) {
	if len(blocklist) != 11 || len(byDigest()) != 11 {
		t.Fatalf("blocklist has %d entries and %d distinct digests, want 11: eight intermediates and three roots", len(blocklist), len(byDigest()))
	}
	derived := map[string]string{}
	for _, certificate := range tlsdistrusttest.Intermediates(t) {
		sum := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
		derived[hex.EncodeToString(sum[:])] = certificate.Subject.CommonName
	}
	for _, certificate := range tlsdistrusttest.RemovedRoots(t) {
		sum := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
		derived[hex.EncodeToString(sum[:])] = certificate.Subject.CommonName
	}
	for _, blocked := range blocklist {
		name, ok := derived[blocked.spki]
		if !ok {
			t.Errorf("%s: digest %s matches no certificate in testdata", blocked.name, blocked.spki)
			continue
		}
		if !strings.HasPrefix(blocked.name, name) {
			t.Errorf("digest %s is %s, but the entry is named %q", blocked.spki, name, blocked.name)
		}
	}
	if len(derived) != 11 {
		t.Errorf("testdata derives %d digests, want 11", len(derived))
	}
}

// A chain Go's verifier accepts (a synthetic CA that the client trusts) is refused by the hook alone once the CA's key is on the blocklist, in a real
// handshake. Go 1.27.3 refuses the eight real keys itself before the hook runs, so this is the test that the hook completes a handshake refusal.
func TestHookRefusesWhatGoAccepts(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic Compromised Root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "synthetic.test"}, DNSNames: []string{"synthetic.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	server := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}, MinVersion: tls.VersionTLS12}
	client := &tls.Config{RootCAs: roots, ServerName: "synthetic.test", MinVersion: tls.VersionTLS12}

	handshake := func(config *tls.Config) error {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = tls.Server(conn, server).Handshake()
			_ = conn.Close()
		}()
		conn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		return tls.Client(conn, config).Handshake()
	}

	if err := handshake(Harden(client)); err != nil {
		t.Fatalf("the synthetic CA is not on the blocklist yet, so the hardened client accepts it: %v", err)
	}
	digest := sha256.Sum256(ca.RawSubjectPublicKeyInfo)
	index := byDigest()
	index[digest] = &entry{name: "Synthetic Compromised Root", spki: hex.EncodeToString(digest[:]), reason: reasonKey}
	t.Cleanup(func() { delete(index, digest) })
	if err := handshake(client); err != nil {
		t.Fatalf("a client without the hook accepts the chain: %v", err)
	}
	err = handshake(Harden(client))
	var distrusted *DistrustedError
	if !errors.As(err, &distrusted) || distrusted.CA != "Synthetic Compromised Root" || distrusted.ServerName != "synthetic.test" {
		t.Fatalf("hardened handshake = %v, want a DistrustedError for the synthetic root and server", err)
	}
}
