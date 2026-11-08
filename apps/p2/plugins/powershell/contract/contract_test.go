package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contract's fingerprint equals Pi's.
func TestFingerprintAsPi(t *testing.T) {
	const module = "plugins/powershell/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#PowerShell"]
	if !ok {
		t.Fatal("Pi exports no PowerShell")
	}
	if PowerShell.Name != want.Name || PowerShell.Version != want.Version || PowerShell.Fingerprint != want.Fingerprint {
		t.Errorf("PowerShell = {%s v%d %s}, Pi has {%s v%d %s}", PowerShell.Name, PowerShell.Version, PowerShell.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
