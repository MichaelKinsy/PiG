package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Every contract of Pi's contract.ts has Pi's name, version and fingerprint (see sessions/contract).
func TestContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/ports/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, ours := range map[string]*contracts.Contract{"ForwardStore": ForwardStore, "Ports": Ports} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("Pi exports no %s", name)
			continue
		}
		if ours.Name != want.Name || ours.Version != want.Version || ours.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, ours.Name, ours.Version, ours.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}
