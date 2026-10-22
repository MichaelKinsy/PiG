// Package tlsdistrusttest holds the public certificates of the October 2026 eMudhra incident and builds, for
// tests only, a server certificate signed by a compromised intermediate.
//
// Sources (all public): the certificates in testdata/emudhra-intermediates.pem and testdata/sectigo-cross-signed.pem are
// the PEM files of the "emudhra-attestation.zip" and "emudhra-attestation-aaa-cross.zip" attachments (ids 9664066 and
// 9664067) of https://bugzilla.mozilla.org/show_bug.cgi?id=2083449; TestFingerprintsMatchTheBug compares their SHA-256
// fingerprints with the ones the bug lists. testdata/emsign-roots.pem holds emSign ECC Root CA - G3, emSign Root CA - C1
// and emSign ECC Root CA - C3 (removed in NSS 3.131.1) and emSign Root CA - G1 (still trusted) as NSS_3_131_RTM
// lib/ckfw/builtins/certdata.txt carries them.
//
// Each of the eight intermediates has a P-384 private key below 256, recoverable from its public key by trying every
// scalar (Mozilla bug 2083449 comment 5; https://scotthelme.co.uk/eight-bits-of-trust-emudhras-ca-keys-cracked-in-milliseconds/).
// [Forge] does exactly that, in memory, to prove that a client without the distrust accepts a certificate a third party
// can mint. Import this package only from _test.go files: TestNoProductionCodeImportsTheTestHelper enforces it.
package tlsdistrusttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed" // embeds the PEM files below
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/emudhra-intermediates.pem
var intermediatesPEM []byte

//go:embed testdata/sectigo-cross-signed.pem
var crossSignedPEM []byte

//go:embed testdata/emsign-roots.pem
var rootsPEM []byte

func parse(t testing.TB, data []byte) []*x509.Certificate {
	t.Helper()
	var certificates []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return certificates
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse testdata certificate: %v", err)
		}
		certificates = append(certificates, certificate)
	}
}

// Intermediates are the eight intermediates eMudhra issued under emSign Root TLS CA - G3 and emSign Root CS CA - G3.
func Intermediates(t testing.TB) []*x509.Certificate { return parse(t, intermediatesPEM) }

// CrossSigned are the same eight keys as Sectigo's "AAA Certificate Services" cross-signed them in May 2025.
func CrossSigned(t testing.TB) []*x509.Certificate { return parse(t, crossSignedPEM) }

// RemovedRoots are emSign ECC Root CA - G3, emSign Root CA - C1 and emSign ECC Root CA - C3.
func RemovedRoots(t testing.TB) []*x509.Certificate { return parse(t, rootsPEM)[:3] }

// EmSignRootG1 is emSign Root CA - G1, which NSS 3.131.1 still trusts and which must not be blocked.
func EmSignRootG1(t testing.TB) *x509.Certificate { return parse(t, rootsPEM)[3] }

// IsTLS reports whether certificate is one of the six TLS intermediates (the other two sign code).
func IsTLS(certificate *x509.Certificate) bool {
	return strings.Contains(certificate.Subject.CommonName, " TLS CA - ")
}

// Recover returns the private key of certificate, a P-384 CA whose private scalar is below 256.
func Recover(t testing.TB, certificate *x509.Certificate) *ecdsa.PrivateKey {
	t.Helper()
	for scalar := byte(1); ; scalar++ {
		raw := make([]byte, 48)
		raw[47] = scalar
		key, err := ecdsa.ParseRawPrivateKey(elliptic.P384(), raw)
		if err != nil {
			t.Fatalf("scalar %d: %v", scalar, err)
		}
		if key.PublicKey.Equal(certificate.PublicKey) {
			return key
		}
		if scalar == 255 {
			t.Fatalf("%s: no private scalar below 256", certificate.Subject.CommonName)
		}
	}
}

// ForgedServer is a server certificate for ServerName that issuer's recovered key signed.
type ForgedServer struct {
	Certificate tls.Certificate
	// Roots trusts issuer alone, as a trust store that still carries the intermediate (or a root above it) would.
	Roots      *x509.CertPool
	ServerName string
}

// Forge signs a server certificate with the recovered key of issuer.
func Forge(t testing.TB, issuer *x509.Certificate) ForgedServer {
	t.Helper()
	signer := Recover(t, issuer)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "forged.test"},
		DNSNames:     []string{"forged.test"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &leafKey.PublicKey, signer)
	if err != nil {
		t.Fatalf("sign a certificate with %s: %v", issuer.Subject.CommonName, err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(issuer)
	return ForgedServer{
		Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: leafKey},
		Roots:       roots,
		ServerName:  "forged.test",
	}
}

// BlockedState is the connection state a verifier reports for a chain that ends at certificate.
func BlockedState(serverName string, chain ...*x509.Certificate) tls.ConnectionState {
	return tls.ConnectionState{ServerName: serverName, VerifiedChains: [][]*x509.Certificate{chain}}
}

// IsRefusal reports whether err is a refusal of a chain through a compromised CA: this package's DistrustedError, or the "no valid chains built"
// error of Go 1.27.3 and later, whose crypto/x509 carries the same eight digests (verify.go distrustedSPKIHashes) and refuses the chain
// before a VerifyConnection hook runs. The package cannot import tlsdistrust (its own tests import this one), so it matches the message.
func IsRefusal(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "refusing the certificate chain") || strings.Contains(message, "no valid chains built")
}
