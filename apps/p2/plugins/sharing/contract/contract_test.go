package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contract fingerprints equal Pi's (see sessions/contract).
func TestSharingContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/sharing/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, contract := range map[string]*contracts.Contract{
		"Shares":        Shares,
		"Presence":      Presence,
		"GuestPresence": GuestPresence,
		"Sharing":       Sharing,
	} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("%s: Pi exports no such contract", name)
			continue
		}
		if contract.Name != want.Name || contract.Version != want.Version || contract.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, contract.Name, contract.Version, contract.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}
