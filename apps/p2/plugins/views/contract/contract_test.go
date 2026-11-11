package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// The views plugin's contracts fingerprint as Pi's (see sessions/contract).
func TestViewsContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/views/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, got := range map[string]*contracts.Contract{"Views": Views, "ViewState": ViewState, "ViewSource": ViewSource, "SavedViews": SavedViews} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("Pi exports no %s", name)
			continue
		}
		if got.Name != want.Name || got.Version != want.Version || got.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, got.Name, got.Version, got.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}
