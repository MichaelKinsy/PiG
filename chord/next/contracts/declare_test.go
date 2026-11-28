package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestDeclareRemoteAndLocal(t *testing.T) {
	// Reserved member name "granted" refused
	_, err := declareRemote("n", 1, props(), rstate(contracts.StateEntry("granted", contracts.Str())))
	wantError(t, err, `"granted" is reserved`)

	// Duplicate across state and methods refused
	_, err = declareRemote("n", 1, props(),
		rstate(contracts.StateEntry("x", contracts.Str())),
		contracts.Meth("x", contracts.NewMethod()),
	)
	wantError(t, err, `"x" is declared twice`)

	// Flag colliding with member refused
	_, err = contracts.Remote("n", contracts.RemoteDeclaration{
		Version:     1,
		Params:      props(),
		HostAddress: serverAddress,
		State:       rstate(contracts.StateEntry("x", contracts.Str())),
		Flags:       []contracts.FlagEntry{contracts.Flag("x", contracts.MessageData{Namespace: "a", Key: "b", Params: []any{}})},
	})
	wantError(t, err, `"x" is a member`)

	// Local contract works
	loc := local(t, "loc", 1, props(),
		contracts.Mem("count", contracts.Int()),
		contracts.Mem("inc", contracts.NewMethod()),
	)
	if loc.Kind != contracts.KindLocal || loc.Version != 1 {
		t.Fatalf("local = %+v", loc)
	}
}
