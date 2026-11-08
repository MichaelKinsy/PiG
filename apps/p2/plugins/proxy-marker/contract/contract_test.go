package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The ProxyMarker contract's fingerprint equals Pi's (see sessions/contract).
func TestProxyMarkerFingerprintsAsPi(t *testing.T) {
	const module = "plugins/proxy-marker/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#ProxyMarker"]
	if !ok {
		t.Fatal("Pi exports no ProxyMarker")
	}
	if ProxyMarker.Name != want.Name || ProxyMarker.Version != want.Version || ProxyMarker.Fingerprint != want.Fingerprint {
		t.Errorf("ProxyMarker = {%s v%d %s}, Pi has {%s v%d %s}", ProxyMarker.Name, ProxyMarker.Version, ProxyMarker.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
