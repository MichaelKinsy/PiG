// Package tlsdistrust refuses TLS connections whose verified certificate chain contains a CA key that is
// known to be compromised, or a root certificate that Mozilla removed from NSS after the October 2026
// eMudhra incident (Mozilla bug 2083449).
//
// The private keys of eight eMudhra intermediate CAs are recoverable from their public certificates (each
// is a P-384 key whose private scalar is below 256). Sectigo cross-signed all eight under "AAA Certificate
// Services", which Apple and Microsoft still trust. Chrome blocks the intermediates with its CRLSet and
// Firefox with OneCRL, but Go, curl and most other non-browser clients check neither revocation nor
// Certificate Transparency, and an operating system trust store keeps a removed root until its vendor
// ships an update. Matching the SubjectPublicKeyInfo digest catches the eMudhra-issued certificate and the
// Sectigo cross-signed certificate of the same key, whichever path the verifier built.
//
// pig additive (D191): every outbound TLS client of PiG applies [Verify] through [Harden] or
// [HardenTransport]; importing this package hardens [http.DefaultTransport] for every client that leaves
// its Transport nil.
package tlsdistrust

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// ErrDistrusted is matched by errors.Is for every [DistrustedError].
var ErrDistrusted = errors.New("tls: distrusted CA in certificate chain")

// DistrustedError reports the first distrusted certificate found in a verified chain.
type DistrustedError struct {
	// ServerName is the name the client verified the chain for.
	ServerName string
	// CA is the name of the distrusted certificate's subject.
	CA string
	// SPKISHA256 is the lower-case hex SHA-256 of the certificate's SubjectPublicKeyInfo.
	SPKISHA256 string
	// Reason says why the certificate is distrusted.
	Reason string
}

func (e *DistrustedError) Error() string {
	return fmt.Sprintf("tls: refusing the certificate chain for %q: it includes %s (SPKI SHA-256 %s), %s", e.ServerName, e.CA, e.SPKISHA256, e.Reason)
}

func (e *DistrustedError) Unwrap() error { return ErrDistrusted }

const (
	reasonKey  = "a CA whose private key is publicly recoverable (Mozilla bug 2083449, October 2026)"
	reasonRoot = "a root that Mozilla removed from NSS 3.131.1 after the eMudhra CA compromise (Mozilla bugs 2083449, 2083709, 2084101)"
)

type entry struct {
	name   string
	spki   string
	reason string
}

// The eight intermediates share one key per CA between the certificate eMudhra issued and the one Sectigo
// cross-signed, so one SPKI digest per CA covers both. Source of every digest: the SHA-256 of the
// DER SubjectPublicKeyInfo of the certificate in the "emudhra-attestation.zip" (direct) and
// "emudhra-attestation-aaa-cross.zip" (cross-signed) attachments of https://bugzilla.mozilla.org/show_bug.cgi?id=2083449
// (comments 3 and 4), whose certificate SHA-256 fingerprints equal the ones the bug lists (CCADB IDs are
// the bug's: direct, then cross-signed). The three roots come from NSS_3_131_RTM lib/ckfw/builtins/certdata.txt,
// the last release that carries them (removed in NSS 3.131.1, announced 2026-10-20). TestBlocklist re-derives
// every digest from the certificates in testdata.
var blocklist = [...]entry{
	{"EM DV TLS CA - G3B (CCADB A012302, A012347)", "ebed6475c2c2d80fcb199f3fe8dd6e6b3ce60b77eb15d4e42c3dec0c6f5b5105", reasonKey},
	{"EM DV TLS CA - G3C (CCADB A012294, A012342)", "49dd353dc2b919a3770645117d0e60c16115e7d1bc259eefe734b00ea434fe5f", reasonKey},
	{"EM EV TLS CA - G3B (CCADB A012304, A012333)", "e0254c7fcdc265e2f0f8a428dabb75ab4ae039b4872d62f5ee57f0877d5a8a41", reasonKey},
	{"EM EV TLS CA - G3C (CCADB A012296, A012334)", "5c0660a2b164910d8ff589c18e14552e9e0ec237dcefe2cb736d41a4811f81a0", reasonKey},
	{"EM OV TLS CA - G3B (CCADB A012308, A012354)", "43bffa19d6ef651451f6841e019931e96f6f2eaeff28ab4b6f1ce4576de2717b", reasonKey},
	{"EM OV TLS CA - G3C (CCADB A012300, A012343)", "63ecefee431d49523bc1136c4e72d0206a477086caf606cd5cf025686c109cf8", reasonKey},
	{"EM OV CS CA - G3B (CCADB A012306, A012352)", "ec4ffa1ad0ac88081314b7aaffef6a91f97cd57291bb5ba686c5415f5eb4f6b6", reasonKey},
	{"EM OV CS CA - G3C (CCADB A012298, A012350)", "92c063d67e8f14db71d288e11266ffdaf69cbf115f7bab14f3bc2f68d25e0e30", reasonKey},
	{"emSign ECC Root CA - G3", "8d417db2dd8bf5e3084d1e3f196d583849d81bdd4c00c70b9d39369e96b8c782", reasonRoot},
	{"emSign Root CA - C1", "b7408b4d2be0238ba37004dd34e276c6019bd2f24c9db7d4980f5f6c359a4bcc", reasonRoot},
	{"emSign ECC Root CA - C3", "eabc185c4e82d942b1a5978ba3c0181487d6b3b9974e5c49f72f6d0bd9637150", reasonRoot},
}

// byDigest indexes blocklist by raw digest. A malformed digest is a programming error found by TestBlocklist.
var byDigest = sync.OnceValue(func() map[[sha256.Size]byte]*entry {
	index := make(map[[sha256.Size]byte]*entry, len(blocklist))
	for i := range blocklist {
		var digest [sha256.Size]byte
		raw, err := hex.DecodeString(blocklist[i].spki)
		if err != nil || len(raw) != sha256.Size {
			panic("tlsdistrust: malformed blocklist digest for " + blocklist[i].name)
		}
		copy(digest[:], raw)
		index[digest] = &blocklist[i]
	}
	return index
})

// Check returns a [DistrustedError] when certificate is on the blocklist.
func Check(serverName string, certificate *x509.Certificate) error {
	digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	blocked, ok := byDigest()[digest]
	if !ok {
		return nil
	}
	return &DistrustedError{ServerName: serverName, CA: blocked.name, SPKISHA256: blocked.spki, Reason: blocked.reason}
}

// Verify is a tls.Config.VerifyConnection hook. It rejects a connection when any certificate of any
// verified chain, leaf and trust anchor included, is on the blocklist. Rejecting on any chain rather than
// only when every chain is blocked is deliberate: the verifier may have found a second path for a chain
// whose forged certificate was signed by a distrusted key, and no legitimate site is issued under these CAs.
//
// A connection made with InsecureSkipVerify has no verified chains and is not checked: that option is the
// caller's explicit decision to trust the peer without verification.
func Verify(state tls.ConnectionState) error {
	for _, chain := range state.VerifiedChains {
		for _, certificate := range chain {
			if err := Check(state.ServerName, certificate); err != nil {
				return err
			}
		}
	}
	return nil
}

// Harden returns a copy of config (a new config when it is nil) whose VerifyConnection runs [Verify] before
// any hook config already had.
func Harden(config *tls.Config) *tls.Config {
	if config == nil {
		config = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		config = config.Clone()
	}
	prior := config.VerifyConnection
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if err := Verify(state); err != nil {
			return err
		}
		if prior != nil {
			return prior(state)
		}
		return nil
	}
	return config
}

// HardenTransport applies [Harden] to transport's TLSClientConfig.
func HardenTransport(transport *http.Transport) {
	transport.TLSClientConfig = Harden(transport.TLSClientConfig)
}

// defaultTransportConfig is what [Harden] returns for a nil config, as a statically initialized value: the default transport has no
// TLS config of its own, and building one in init cost four allocations in every binary that links this package.
var defaultTransportConfig = tls.Config{MinVersion: tls.VersionTLS12, VerifyConnection: Verify}

// init hardens http.DefaultTransport before any client of a program that links this package can use it.
// Clients that leave Transport nil, http.DefaultClient and every Clone of the default transport made later
// inherit the hook.
func init() {
	transport, ok := http.DefaultTransport.(*http.Transport)
	switch {
	case !ok:
	case transport.TLSClientConfig == nil:
		transport.TLSClientConfig = &defaultTransportConfig
	default:
		HardenTransport(transport)
	}
}
