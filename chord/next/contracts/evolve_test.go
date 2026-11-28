package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestEvolveRemoteAndGrantable(t *testing.T) {
	v1 := remote(t, "ev", 1, props(),
		rstate(contracts.StateEntry("a", contracts.Str())),
		contracts.Meth("m", contracts.NewGrantable(contracts.NewMethod(contracts.Str()))),
	)
	v2, err := contracts.Evolve(v1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("a", contracts.Int()).
			AddState("b", contracts.NewGrantable(contracts.Bool())).
			RemoveMethod("m")
	})
	wantNoError(t, err)
	if v2.Version != 2 || v2.Fingerprint == v1.Fingerprint {
		t.Fatalf("v2 = %+v", v2)
	}
	// Re-adding removed grantable method as non-grantable is refused
	_, err = contracts.Evolve(v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.AddMethod("m", contracts.Str(), contracts.Nul())
	})
	wantError(t, err, "whether it is grantable never changes across a history")
}
