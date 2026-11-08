package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contract's fingerprint equals Pi's (see sessions/contract). Pi's other contracts of the module (the rest of the
// plugin's port) are not declared here yet.
func TestChildStoreFingerprintsAsPi(t *testing.T) {
	const module = "plugins/subagents/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#ChildStore"]
	if !ok {
		t.Fatal("Pi exports no ChildStore")
	}
	if ChildStore.Name != want.Name || ChildStore.Version != want.Version || ChildStore.Fingerprint != want.Fingerprint {
		t.Errorf("ChildStore = {%s v%d %s}, Pi has {%s v%d %s}", ChildStore.Name, ChildStore.Version, ChildStore.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
