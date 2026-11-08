package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The Logs contract's fingerprint equals Pi's (see sessions/contract).
func TestLogsContractFingerprintsAsPi(t *testing.T) {
	const module = "plugins/logs/contract.ts"
	pi := oracle.Fingerprints(t, module)
	want, ok := pi[module+"#Logs"]
	if !ok {
		t.Fatal("Pi exports no Logs contract")
	}
	if Logs.Name != want.Name || Logs.Version != want.Version || Logs.Fingerprint != want.Fingerprint {
		t.Errorf("Logs = {%s v%d %s}, Pi has {%s v%d %s}", Logs.Name, Logs.Version, Logs.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
