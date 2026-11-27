package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestDeclareTypes(t *testing.T) {
	c := remote(t, "t", 1, props(), rstate(contracts.StateEntry("s", contracts.Str())))
	if c.Kind != contracts.KindRemote || c.Version != 1 || c.Fingerprint == "" {
		t.Fatalf("unexpected contract %+v", c)
	}
}
