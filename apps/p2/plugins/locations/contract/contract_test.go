package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contract has Pi's name, version and fingerprint (see sessions/contract).
func TestContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/locations/contract.ts"
	pi := oracle.Fingerprints(t, module)
	want, ok := pi[module+"#AgentLocations"]
	if !ok {
		t.Fatal("Pi exports no AgentLocations")
	}
	if AgentLocations.Name != want.Name || AgentLocations.Version != want.Version || AgentLocations.Fingerprint != want.Fingerprint {
		t.Errorf("AgentLocations = {%s v%d %s}, Pi has {%s v%d %s}", AgentLocations.Name, AgentLocations.Version, AgentLocations.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
