package runtime

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contract fingerprints: p2's browser client is Pi's TypeScript and binds by fingerprint, so each Go contract must hash
// as the contract Pi's module declares, name and version included.

func TestContractsFingerprintAsPi(t *testing.T) {
	pi := oracle.Fingerprints(t, "src/runtime/access.ts", "src/runtime/notices.ts", "src/runtime/session-worker.ts")
	for key, contract := range map[string]*contracts.Contract{
		"src/runtime/access.ts#Access":                 Access,
		"src/runtime/notices.ts#LogNotices":            LogNotices,
		"src/runtime/session-worker.ts#WorkerBoundary": WorkerBoundary,
	} {
		want, ok := pi[key]
		if !ok {
			t.Errorf("%s: Pi exports no such contract", key)
			continue
		}
		if contract.Name != want.Name || contract.Version != want.Version || contract.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", key, contract.Name, contract.Version, contract.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}
