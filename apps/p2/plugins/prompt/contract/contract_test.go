package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contract's fingerprint equals Pi's (see sessions/contract).
func TestPromptResourcesFingerprintsAsPi(t *testing.T) {
	const module = "plugins/prompt/contract.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#PromptResources"]
	if !ok {
		t.Fatal("Pi exports no PromptResources")
	}
	if PromptResources.Name != want.Name || PromptResources.Version != want.Version || PromptResources.Fingerprint != want.Fingerprint {
		t.Errorf("PromptResources = {%s v%d %s}, Pi has {%s v%d %s}", PromptResources.Name, PromptResources.Version, PromptResources.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
