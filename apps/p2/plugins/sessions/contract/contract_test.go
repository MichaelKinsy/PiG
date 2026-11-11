package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contract fingerprints: p2's browser client is Pi's TypeScript and binds by fingerprint, so each Go contract must
// hash as the contract Pi's module declares, name and version included.
func TestSessionContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/sessions/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, contract := range map[string]*contracts.Contract{
		"Catalog":           Catalog,
		"Sessions":          Sessions,
		"ArchivedStarts":    ArchivedStarts,
		"SessionOperations": SessionOperations,
		"SessionMoves":      SessionMoves,
		"SessionTransfer":   SessionTransfer,
		"SessionLocks":      SessionLocks,
		"SessionLock":       SessionLock,
		"SidebarSettingsStore": SidebarSettingsStore,
		"SidebarSettings":   SidebarSettings,
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
