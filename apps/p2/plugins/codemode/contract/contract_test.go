package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contract's fingerprint equals Pi's.
func TestFingerprintAsPi(t *testing.T) {
	const module = "plugins/codemode/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#CodemodeRuntime"]
	if !ok {
		t.Fatal("Pi exports no CodemodeRuntime")
	}
	if CodemodeRuntime.Name != want.Name || CodemodeRuntime.Version != want.Version || CodemodeRuntime.Fingerprint != want.Fingerprint {
		t.Errorf("CodemodeRuntime = {%s v%d %s}, Pi has {%s v%d %s}", CodemodeRuntime.Name, CodemodeRuntime.Version, CodemodeRuntime.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
