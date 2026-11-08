package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The RadiusService contract's fingerprint equals Pi's (see sessions/contract).
func TestRadiusServiceFingerprintsAsPi(t *testing.T) {
	const module = "plugins/radius/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#RadiusService"]
	if !ok {
		t.Fatal("Pi exports no RadiusService")
	}
	if RadiusService.Name != want.Name || RadiusService.Version != want.Version || RadiusService.Fingerprint != want.Fingerprint {
		t.Errorf("RadiusService = {%s v%d %s}, Pi has {%s v%d %s}", RadiusService.Name, RadiusService.Version, RadiusService.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
