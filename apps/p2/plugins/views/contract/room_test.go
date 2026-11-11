package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// The rooms' contracts fingerprint as Pi's (plugins/views/room-contract.ts, Pi main 682c7b93c): a frame's room, a page's links, and the
// worker's openings and backing for the server.
func TestRoomContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/views/room-contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, got := range map[string]*contracts.Contract{"ViewRoom": ViewRoom, "ViewLinks": ViewLinks, "ViewOpenings": ViewOpenings, "ViewBacking": ViewBacking} {
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
