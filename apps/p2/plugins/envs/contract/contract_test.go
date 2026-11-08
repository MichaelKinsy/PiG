package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contract's fingerprint equals Pi's (see sessions/contract). Pi's other contracts of the module (the rest of the
// plugin's port) are not declared here yet.
func TestEnvServiceFingerprintsAsPi(t *testing.T) {
	const module = "plugins/envs/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#EnvService"]
	if !ok {
		t.Fatal("Pi exports no EnvService")
	}
	if EnvService.Name != want.Name || EnvService.Version != want.Version || EnvService.Fingerprint != want.Fingerprint {
		t.Errorf("EnvService = {%s v%d %s}, Pi has {%s v%d %s}", EnvService.Name, EnvService.Version, EnvService.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
