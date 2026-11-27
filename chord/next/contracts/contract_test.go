package contracts_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestContractHandlesAndOrder(t *testing.T) {
	c := remote(t, "notes", 1, props(contracts.Prop("id", contracts.Str())),
		rstate(contracts.StateEntry("z", contracts.Str()), contracts.StateEntry("a", contracts.Int())),
		contracts.Meth("m2", contracts.NewMethod()),
		contracts.Meth("m1", contracts.NewMethod(contracts.Str())),
	)
	names, err := contracts.MemberNamesOf(c)
	wantNoError(t, err)
	if !slices.Equal(names.State, []string{"z", "a"}) || !slices.Equal(names.Methods, []string{"m2", "m1"}) {
		t.Fatalf("MemberNamesOf = %+v", names)
	}
	members := currentMembers(t, c)
	order := contracts.MemberOrder(members)
	if !slices.Equal(order.State, []string{"a", "z"}) || !slices.Equal(order.Methods, []string{"m1", "m2"}) {
		t.Fatalf("MemberOrder = %+v", order)
	}
	// Copy refused
	copied := *c
	_, err = contracts.ContractDataOf(&copied)
	wantError(t, err, "not a declared contract")
}
