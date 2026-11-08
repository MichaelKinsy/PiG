package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// Contract fingerprints: p2's browser client is Pi's TypeScript and binds by fingerprint, so the Go contract must hash as the
// contract Pi's module declares, name and version included (a mismatch is "contract_mismatch" on the wire and the Providers sheet
// stays empty).
func TestProviderLoginFingerprintsAsPi(t *testing.T) {
	const module = "plugins/login/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#ProviderLogin"]
	if !ok {
		t.Fatal("Pi exports no ProviderLogin")
	}
	if ProviderLogin.Name != want.Name || ProviderLogin.Version != want.Version || ProviderLogin.Fingerprint != want.Fingerprint {
		t.Errorf("ProviderLogin = {%s v%d %s}, Pi has {%s v%d %s}", ProviderLogin.Name, ProviderLogin.Version, ProviderLogin.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
