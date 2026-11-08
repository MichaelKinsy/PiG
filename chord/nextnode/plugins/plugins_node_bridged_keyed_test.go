package plugins

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// A contract with a keyed member cannot be forwarded by a proxy facet (a keyed member is the document handle of its object): the
// bridge refuses it before anything ships. The app kit's `Files` (keyed member `summary`) is such a contract (D461 in
// rows/chord-ui.md).
func TestABridgedFacetRefusesAContractWithAKeyedMember(t *testing.T) {
	keyed, err := contracts.Local("test.keyed", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str()), contracts.Mem("docs", contracts.Keyed(contracts.Str()))},
	})
	if err != nil {
		t.Fatal(err)
	}
	goHost := bgGoHost(t)
	err = validBridged([]BridgedFacet{{Name: "@app/keyed", Host: goHost, Contracts: []*contracts.Contract{keyed}}})
	if err == nil || !strings.Contains(err.Error(), `keyed member "docs"`) {
		t.Fatalf("validBridged = %v, want a refusal naming the keyed member", err)
	}
}
