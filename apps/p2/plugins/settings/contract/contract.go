// Package contract ports apps/p2/plugins/settings/contract.ts: the owner's settings.json editor, the settings as the
// server holds them and a change of some of them.
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// Settings is settings.json for the Settings sheet (`p2.settings`, owner only); the file stays the source, saved through
// the models plugin. `settings` is the last valid settings.json (the server keeps it while the file is invalid), `error`
// why settings.json could not be loaded (null while it is valid). `update` sets the top-level settings in `set` (a partial
// settings.json, each value whole) and removes the ones in `unset` (back to their defaults); the other settings stay as
// the file has them now. The answer comes after the server reloaded the file.
var Settings = must(contracts.Remote("p2.settings", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("settings", schema.Unknown()),
				p2runtime.StateProp("error", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
			},
			Methods: []contracts.MethodEntry{
				contracts.Meth("update", contracts.NewMethod(p2runtime.Contract(schema.Obj(schema.Properties{
					schema.Prop("set", schema.Unknown()),
					schema.Prop("unset", schema.Arr(schema.Str(schema.MaxLength(64)), schema.MaxItems(64))),
				})))),
			},
		}),
	},
}))
