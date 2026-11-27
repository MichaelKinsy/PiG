package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestFingerprintStability(t *testing.T) {
	c1 := remote(t, "fp", 1, props(contracts.Prop("id", contracts.Str())),
		rstate(contracts.StateEntry("a", contracts.Str()), contracts.StateEntry("b", contracts.Int())),
	)
	c2 := remote(t, "fp", 1, props(contracts.Prop("id", contracts.Str())),
		rstate(contracts.StateEntry("b", contracts.Int()), contracts.StateEntry("a", contracts.Str())),
	)
	if c1.Fingerprint != c2.Fingerprint {
		t.Fatalf("member order changed fingerprint: %s vs %s", c1.Fingerprint, c2.Fingerprint)
	}
	c3 := remote(t, "fp", 1, props(contracts.Prop("id", contracts.Str())),
		rstate(contracts.StateEntry("a", contracts.NewGrantable(contracts.Str())), contracts.StateEntry("b", contracts.Int())),
	)
	if c1.Fingerprint == c3.Fingerprint {
		t.Fatal("grantable should change fingerprint")
	}
}
