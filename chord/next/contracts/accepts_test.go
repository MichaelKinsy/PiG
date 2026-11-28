package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestCheckAcceptsV170(t *testing.T) {
	v1 := remote(t, "notes", 1, props(contracts.Prop("id", contracts.Str())),
		rstate(contracts.StateEntry("items", contracts.Arr(contracts.Str()))),
		contracts.Meth("add", contracts.NewMethod(contracts.Str(), contracts.Bool())),
		contracts.Meth("old", contracts.NewMethod(contracts.Str())),
	)
	v2 := must(contracts.Evolve(v1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("items", contracts.Arr(contracts.Int())).
			Args("add", contracts.Int()).
			RemoveMethod("old")
	}))

	checked, err := contracts.CheckAccepts(v2, contracts.AcceptsTable{
		{Name: "1", Value: contracts.VersionMappings{
			State: []contracts.Entry[contracts.StateMapping]{
				{Name: "items", Value: stateFn()},
			},
			Methods: []contracts.Entry[contracts.MethodMapping]{
				{Name: "add", Value: contracts.MethodMapping{Args: fn}},
				{Name: "old", Value: contracts.MethodMapping{Refuse: true}},
			},
		}},
	})
	wantNoError(t, err)
	if len(checked.Versions) != 1 || checked.Versions[0].Version != 1 || checked.Versions[0].Fingerprint != v1.Fingerprint {
		t.Fatalf("checked = %+v", checked)
	}

	// Missing state mapping
	_, err = contracts.CheckAccepts(v2, contracts.AcceptsTable{
		{Name: "1", Value: contracts.VersionMappings{
			Methods: []contracts.Entry[contracts.MethodMapping]{
				{Name: "add", Value: contracts.MethodMapping{Args: fn}},
				{Name: "old", Value: contracts.MethodMapping{Refuse: true}},
			},
		}},
	})
	wantError(t, err, "notes accepts[1].state.items: missing mapping")

	// Current version needs no entry
	_, err = contracts.CheckAccepts(v2, contracts.AcceptsTable{
		{Name: "2", Value: contracts.VersionMappings{}},
	})
	wantError(t, err, "notes accepts[2]: current version needs no entry in accepts")
}
