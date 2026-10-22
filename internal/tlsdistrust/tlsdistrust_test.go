package tlsdistrust_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tlsdistrust"
	"github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

// The SHA-256 fingerprints Mozilla bug 2083449 lists (description and comment 2) for the eight certificates eMudhra issued and the
// eight Sectigo cross-signed, keyed by subject common name.
var directFingerprints = map[string]string{
	"EM EV TLS CA - G3B": "E7568FE6E12331F4FD7DE61DA4C7559D0F8EB192A218D4741A3BA75184333838",
	"EM OV TLS CA - G3B": "98B896625C0C230ADEEC3EB2CF6AAD5001ECF9156A68644E82374CE5833BC25B",
	"EM DV TLS CA - G3B": "42C4ACCD12C4F59597925E1A08A7A90102608543809308D50EC427E118BA1493",
	"EM EV TLS CA - G3C": "432EC25F0D28DE9006D97BF752F17BBB2CD41F669933FCB072D9A1148D0261F3",
	"EM OV TLS CA - G3C": "CB6E15165AC802E9B3F993F16C25D41F4E81C21EBDE71AE1B012AC00BF9C7C3F",
	"EM DV TLS CA - G3C": "0164D7CEAE5BB909C272AF151FF44B1177AE9B47D9424B94F1D9558C30934D19",
	"EM OV CS CA - G3B":  "9E39DE2881273ACC225A149C72BA3E0F51424963669AB633EE1DC7DF5DB067B4",
	"EM OV CS CA - G3C":  "4B2460A466F95FD40113BCDCCA2CEB29B3B093921DE45474DA7DF48874C630B1",
}

var crossSignedFingerprints = map[string]string{
	"EM EV TLS CA - G3B": "579480AEA166290BD3C643589E795FC0D0E54E86505D180F1DFF99EFD3199512",
	"EM OV TLS CA - G3B": "5F1DED4DD590AF74631290CF073B122A2AAC6A1FA479778A0BD43579BD832699",
	"EM DV TLS CA - G3B": "45A883F349AEDFD5F3A649B5498FFB121712BA8B448F3F2A91DBC5A766FCDDBE",
	"EM EV TLS CA - G3C": "FD9D5C2DBBD4995AB69B8D50C439D9527B00529199C2FD01B2174EB58F07021F",
	"EM OV TLS CA - G3C": "7485B51C0ED192878FA1D2F948FB529AE1842CC5086C4B67F7BE5B2039DE9489",
	"EM DV TLS CA - G3C": "62B7F9868263F72F6911FC78CEAF72C1979158B7368BF648683BB2AA39B02E72",
	"EM OV CS CA - G3B":  "A496E23AC0CAB8155FC9B117B97EAE368EFCBB9041AEC8804D5A308D2D01C2D8",
	"EM OV CS CA - G3C":  "84112972B8E59D14560AE7E7119654474AAD119990CF3722BFC1E3B91615E1FD",
}

func fingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestFingerprintsMatchTheBug(t *testing.T) {
	for _, set := range []struct {
		name         string
		certificates []*x509.Certificate
		want         map[string]string
	}{
		{"issued by eMudhra", tlsdistrusttest.Intermediates(t), directFingerprints},
		{"cross-signed by Sectigo", tlsdistrusttest.CrossSigned(t), crossSignedFingerprints},
	} {
		if len(set.certificates) != len(set.want) {
			t.Fatalf("%s: %d certificates, the bug lists %d", set.name, len(set.certificates), len(set.want))
		}
		for _, certificate := range set.certificates {
			name := certificate.Subject.CommonName
			if got := fingerprint(certificate); got != set.want[name] {
				t.Errorf("%s, %s: SHA-256 %s, the bug lists %s", set.name, name, got, set.want[name])
			}
		}
	}
}

func chainOf(t *testing.T, certificate *x509.Certificate) tls.ConnectionState {
	t.Helper()
	return tlsdistrusttest.BlockedState("example.test", tlsdistrusttest.EmSignRootG1(t), certificate)
}

func TestVerifyRejectsEveryCompromisedIntermediateOnBothPaths(t *testing.T) {
	for path, certificates := range map[string][]*x509.Certificate{
		"issued by eMudhra":       tlsdistrusttest.Intermediates(t),
		"cross-signed by Sectigo": tlsdistrusttest.CrossSigned(t),
	} {
		for _, certificate := range certificates {
			err := tlsdistrust.Verify(chainOf(t, certificate))
			var distrusted *tlsdistrust.DistrustedError
			if !errors.As(err, &distrusted) || !errors.Is(err, tlsdistrust.ErrDistrusted) {
				t.Errorf("%s, %s: Verify = %v, want a DistrustedError", path, certificate.Subject.CommonName, err)
				continue
			}
			if !strings.Contains(distrusted.CA, certificate.Subject.CommonName) || distrusted.ServerName != "example.test" {
				t.Errorf("%s, %s: error names %q for %q", path, certificate.Subject.CommonName, distrusted.CA, distrusted.ServerName)
			}
			for _, want := range []string{"example.test", certificate.Subject.CommonName, "SPKI SHA-256", "bug 2083449"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s, %s: message %q lacks %q", path, certificate.Subject.CommonName, err, want)
				}
			}
		}
	}
}

func TestVerifyRejectsTheRemovedRootsAndKeepsG1(t *testing.T) {
	for _, root := range tlsdistrusttest.RemovedRoots(t) {
		leaf := &x509.Certificate{RawSubjectPublicKeyInfo: []byte("leaf")}
		err := tlsdistrust.Verify(tls.ConnectionState{ServerName: "example.test", VerifiedChains: [][]*x509.Certificate{{leaf, root}}})
		if !errors.Is(err, tlsdistrust.ErrDistrusted) || !strings.Contains(err.Error(), root.Subject.CommonName) {
			t.Errorf("%s: Verify = %v, want a distrust error naming it", root.Subject.CommonName, err)
		}
	}
	g1 := tlsdistrusttest.EmSignRootG1(t)
	if err := tlsdistrust.Check("example.test", g1); err != nil {
		t.Errorf("emSign Root CA - G1 is still trusted by NSS 3.131.1: %v", err)
	}
}

func TestVerifyChecksEveryVerifiedChain(t *testing.T) {
	clean := &x509.Certificate{RawSubjectPublicKeyInfo: []byte("clean")}
	blocked := tlsdistrusttest.CrossSigned(t)[0]
	state := tls.ConnectionState{ServerName: "example.test", VerifiedChains: [][]*x509.Certificate{{clean}, {clean, blocked}}}
	if err := tlsdistrust.Verify(state); !errors.Is(err, tlsdistrust.ErrDistrusted) {
		t.Fatalf("a second chain through a blocked key: Verify = %v", err)
	}
	if err := tlsdistrust.Verify(tls.ConnectionState{ServerName: "example.test", VerifiedChains: [][]*x509.Certificate{{clean}}}); err != nil {
		t.Fatalf("a clean chain: Verify = %v", err)
	}
	if err := tlsdistrust.Verify(tls.ConnectionState{}); err != nil {
		t.Fatalf("no verified chain (InsecureSkipVerify): Verify = %v", err)
	}
}

func TestBlocklistIsExactlyTheIncidentKeys(t *testing.T) {
	// Eight intermediates plus three roots, one digest each: the issued and the cross-signed certificate of a CA share a key.
	seen := map[string]bool{}
	for _, set := range [][]*x509.Certificate{tlsdistrusttest.Intermediates(t), tlsdistrusttest.CrossSigned(t), tlsdistrusttest.RemovedRoots(t)} {
		for _, certificate := range set {
			if err := tlsdistrust.Check("x", certificate); err == nil {
				t.Errorf("%s is not blocked", certificate.Subject.CommonName)
			}
			sum := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
			seen[hex.EncodeToString(sum[:])] = true
		}
	}
	if len(seen) != 11 {
		t.Fatalf("testdata carries %d distinct keys, want 11 (8 intermediates, 3 roots)", len(seen))
	}
}

// handshake runs one TLS handshake over a loopback socket (net.Pipe has no buffer, and the two sides' writes deadlock).
func handshake(t *testing.T, server, client *tls.Config) error {
	t.Helper()
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
	return tls.Client(conn, client).Handshake()
}

// A server certificate signed by the recovered key of any TLS intermediate verifies for a client that trusts that intermediate before Go 1.27.3,
// whose crypto/x509 carries the same eight digests and refuses the chain first (verify.go distrustedSPKIHashes: "x509: no valid chains built").
// Either way the hardened client must not complete the handshake; TestHookRefusesWhatGoAccepts runs the hook alone.
func TestForgedCertificateIsRefused(t *testing.T) {
	for path, certificates := range map[string][]*x509.Certificate{
		"issued by eMudhra":       tlsdistrusttest.Intermediates(t),
		"cross-signed by Sectigo": tlsdistrusttest.CrossSigned(t),
	} {
		for _, issuer := range certificates {
			if !tlsdistrusttest.IsTLS(issuer) {
				continue
			}
			forged := tlsdistrusttest.Forge(t, issuer)
			server := &tls.Config{Certificates: []tls.Certificate{forged.Certificate}, MinVersion: tls.VersionTLS12}
			plain := &tls.Config{RootCAs: forged.Roots, ServerName: forged.ServerName, MinVersion: tls.VersionTLS12}
			err := handshake(t, server, tlsdistrust.Harden(plain))
			if !tlsdistrusttest.IsRefusal(err) {
				t.Errorf("%s, %s: the hardened client's handshake = %v, want a refusal", path, issuer.Subject.CommonName, err)
			}
			t.Logf("%s, %s: plain client: %v; hardened client: %v", path, issuer.Subject.CommonName, handshake(t, server, plain), err)
			if plain.VerifyConnection != nil {
				t.Fatal("Harden changed the config it was given")
			}
		}
	}
}

func TestHardenRunsThePriorHook(t *testing.T) {
	sentinel := errors.New("prior hook")
	var calls int
	config := tlsdistrust.Harden(&tls.Config{VerifyConnection: func(tls.ConnectionState) error { calls++; return sentinel }})
	if err := config.VerifyConnection(tls.ConnectionState{}); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("clean chain: err %v after %d prior calls", err, calls)
	}
	err := config.VerifyConnection(chainOf(t, tlsdistrusttest.Intermediates(t)[0]))
	if !errors.Is(err, tlsdistrust.ErrDistrusted) || calls != 1 {
		t.Fatalf("blocked chain: err %v after %d prior calls; the distrust must come first", err, calls)
	}
	if tlsdistrust.Harden(nil).VerifyConnection == nil {
		t.Fatal("Harden(nil) has no hook")
	}
}

// http.DefaultTransport, the transport of every client that leaves Transport nil, carries the hook once this package is linked,
// and a clone of it keeps the hook.
func TestDefaultTransportRefusesAForgedServer(t *testing.T) {
	issuer := tlsdistrusttest.Intermediates(t)[0]
	for _, certificate := range tlsdistrusttest.Intermediates(t) {
		if tlsdistrusttest.IsTLS(certificate) {
			issuer = certificate
			break
		}
	}
	forged := tlsdistrusttest.Forge(t, issuer)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{forged.Certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	defaults, ok := http.DefaultTransport.(*http.Transport)
	if !ok || defaults.TLSClientConfig == nil || defaults.TLSClientConfig.VerifyConnection == nil {
		t.Fatal("http.DefaultTransport has no VerifyConnection hook")
	}
	transport := defaults.Clone()
	if err := transport.TLSClientConfig.VerifyConnection(chainOf(t, issuer)); !errors.Is(err, tlsdistrust.ErrDistrusted) {
		t.Fatalf("a clone of the default transport: VerifyConnection on a chain through %s = %v, want ErrDistrusted", issuer.Subject.CommonName, err)
	}
	transport.TLSClientConfig.RootCAs = forged.Roots
	transport.TLSClientConfig.ServerName = forged.ServerName
	_, err := (&http.Client{Transport: transport}).Get(server.URL) //nolint:bodyclose // the request must fail
	if !tlsdistrusttest.IsRefusal(err) {
		t.Fatalf("GET through a clone of the default transport = %v, want a refusal", err)
	}
}

func TestHardenTransportWrapsTheExistingConfig(t *testing.T) {
	transport := &http.Transport{}
	tlsdistrust.HardenTransport(transport)
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.VerifyConnection == nil {
		t.Fatal("HardenTransport left no hook")
	}
}
