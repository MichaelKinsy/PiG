package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The Settings contract's fingerprint equals Pi's (see sessions/contract).
func TestSettingsFingerprintsAsPi(t *testing.T) {
	const module = "plugins/settings/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#Settings"]
	if !ok {
		t.Fatal("Pi exports no Settings")
	}
	if Settings.Name != want.Name || Settings.Version != want.Version || Settings.Fingerprint != want.Fingerprint {
		t.Errorf("Settings = {%s v%d %s}, Pi has {%s v%d %s}", Settings.Name, Settings.Version, Settings.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
