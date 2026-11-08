package prefs

// The Chord contract of the server's TUI defaults.
//
// pi: apps/p2/src/runtime/tui-preferences.ts (`TuiPreferences`)

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

// TuiPreferences is the server's TUI defaults (`p2.tui-preferences`, version 2): owners and guests read them (`viewer`). The answer is a TuiServerPreferences
// (validated by SanitizeServerPreferences on the server and again by the client); its schema is left open here.
var TuiPreferences = func() *contracts.Contract {
	contract, err := contracts.Remote("p2.tui-preferences", contracts.RemoteDeclaration{
		Version:     2,
		Params:      contracts.Params{},
		HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("viewer", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
				contracts.Meth("read", contracts.NewMethod(contracts.Schema{}, p2runtime.Contract(schema.Unknown()))),
			}}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()
