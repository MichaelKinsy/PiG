package app

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// No Pi file: the Go form of the server host's data operations on the plugins' store (D462).
//
// Pi's server has one host and one store: `exportHostData`, `importHostData` and `deleteHostData` act on the records and rows of a
// plugin's keyed documents and `host(...)` columns. PiG's server has two (the Go host's store and the NodeHost's, D462), so the app
// reaches the NodeHost's part through ServerHostData, a remote type placed on the server that the NodeHost serves (the server host
// delegates it over its link, D605) and whose JavaScript facet calls Pi's `exportHostData`, `importHostData` and `deleteHostData` of
// the NodeHost. The Node side reads and writes JSONL files at paths the server names (both processes share the machine).
//
// pig divergence (D462): the server's data is two parts, the NodeHost's (`server.archive`, Pi's shape) and the Go host's (`kit-server.archive`), each committed by its own host.

// ServerHostData is the server's data on the NodeHost: `exportTo` writes the records and rows belonging to `address` (its JSON, as
// AddressParam writes it) as a JSONL host data archive at `path`; `importFrom` imports the archive at `path` under `address` (`existing`
// is "refuse" or "replace"); `deleteFor` deletes what belongs to `address`.
var ServerHostData = mustRemote("chord.app.server-data", contracts.RemoteDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
		return Server.Address(delta.NewJsonObject(0)), nil
	},
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
		contracts.Meth("exportTo", contracts.NewMethod(contracts.Obj(contracts.Prop("address", contracts.Str()), contracts.Prop("path", contracts.Str())))),
		contracts.Meth("importFrom", contracts.NewMethod(contracts.Obj(contracts.Prop("address", contracts.Str()), contracts.Prop("path", contracts.Str()), contracts.Prop("existing", contracts.Str())))),
		contracts.Meth("deleteFor", contracts.NewMethod(contracts.Obj(contracts.Prop("address", contracts.Str())))),
	}})},
})

// serverHostData calls member of ServerHostData with args and waits for it.
func (a *App) serverHostData(member string, args *delta.JsonObject, ctx context.Context) error {
	reference, err := a.Host.Get(ctx, ServerHostData, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	defer reference.Release()
	_, err = reference.Call(member, args, ctx)
	return err
}
