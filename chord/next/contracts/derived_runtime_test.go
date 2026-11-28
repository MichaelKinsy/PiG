package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestDerivedRuntime(t *testing.T) {
	c := remote(t, "d", 1, props(), rstate(contracts.StateEntry("s", contracts.Str())), contracts.Meth("m", contracts.NewMethod()))
	m := currentMembers(t, c)
	if !contracts.TakesNoArgs(m.Methods.Value("m")) {
		t.Fatal("expected TakesNoArgs")
	}
}
