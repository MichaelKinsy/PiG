package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contracts' fingerprints equal Pi's (see sessions/contract): the manager and its view per principal.
func TestContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/mcp/contract.ts"
	fingerprints := oracle.Fingerprints(t, module)
	for name, got := range map[string]struct {
		Name        string
		Version     int
		Fingerprint string
	}{
		"McpManager": {McpManager.Name, McpManager.Version, McpManager.Fingerprint},
		"Mcp":        {Mcp.Name, Mcp.Version, Mcp.Fingerprint},
	} {
		want, ok := fingerprints[module+"#"+name]
		if !ok {
			t.Fatalf("Pi exports no %s", name)
		}
		if got.Name != want.Name || got.Version != want.Version || got.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, got.Name, got.Version, got.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}

func TestSignInLinkEncodesTheServerName(t *testing.T) {
	if got := SignInLink("my docs/é"); got != "/mcp/sign-in/my%20docs%2F%C3%A9" {
		t.Fatalf("got %s", got)
	}
}
