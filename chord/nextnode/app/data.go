package app

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/node/app/data.ts
//
// A host's data on its worker (decision "App kit naming and export"): the kit's object a running worker serves, through which the
// server exports the worker's data without stopping it. The worker packs its own open store (its export holds the commit queue while
// it reads: one consistent point) and its finished files into the archive at the path the server names. Only the server may ask (its
// principal); the worker's plugins never see it.
//
// Go forced differences: the generic principal parameter is designed out (a principal is a JSON object). The worker's plugins run in a
// Node subprocess that serves the worker's address, so HostData is a JavaScript core facet (host_data.mjs) forwarding to a Go local
// object (HostDataExport) through the bridge.

// HostData is the data of the worker at `address` (its JSON, as AddressParam writes it): `exportTo` packs it at `path`.
var HostData = mustRemote("chord.app.data", contracts.RemoteDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("address", contracts.Str())},
	// The address a worker has: the server names it (AddressParam); only its kind and params place the object.
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		text, _ := params.Value("address").(string)
		return parseAddress(text)
	},
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
		contracts.Meth("exportTo", contracts.NewMethod(contracts.Obj(contracts.Prop("path", contracts.Str()), contracts.Prop("manifest", contracts.Str())))),
	}})},
})

func mustRemote(name string, declaration contracts.RemoteDeclaration) *contracts.Contract {
	contract, err := contracts.Remote(name, declaration)
	if err != nil {
		panic(err)
	}
	return contract
}

// addressWire is an address as AddressParam writes it.
type addressWire struct {
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params"`
	Key    string          `json:"key"`
}

// parseAddress is `JSON.parse(address) as Address`.
func parseAddress(text string) (contracts.Address, error) {
	var wire addressWire
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return contracts.Address{}, err
	}
	params := delta.NewJsonObject(0)
	if len(wire.Params) > 0 {
		decoded, err := chordjson.Decode(wire.Params)
		if err != nil {
			return contracts.Address{}, err
		}
		if object, ok := decoded.(*delta.JsonObject); ok {
			params = object
		}
	}
	return contracts.Address{Kind: wire.Kind, Params: params, Key: wire.Key}, nil
}

// AddressParam is `address` as HostData's param.
func AddressParam(address contracts.Address) string {
	encoded, _ := chordjson.Marshal(delta.JsonObjectOf("kind", address.Kind, "params", address.Params, "key", address.Key))
	return string(encoded)
}

// HostDataExport is HostData's body on the worker's Go host: the NodeHost serves the worker's address, so HostData is served there by
// hostDataModule (a JavaScript core facet) and forwards `exportTo` to this local object, which the NodeHost reaches through the
// bridge (plugins.BridgedFacet). `token` is the secret of this start that only hostDataModule knows: plugins see the bridged type but
// never the server's data object (a Go forced difference; Pi's plugins cannot reach the facet at all).
var HostDataExport = mustLocal("chord.app.data.export", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("exportTo", contracts.NewMethod(contracts.Obj(contracts.Prop("token", contracts.Str()), contracts.Prop("path", contracts.Str()), contracts.Prop("manifest", contracts.Str())))),
	},
})

// hostDataModuleText is the module of the worker's NodeHost serving HostData (hostDataModule's template).
//
//go:embed host_data.mjs
var hostDataModuleText string

// hostDataModule is the text of the module serving HostData on the NodeHost for the worker whose server principal is server, forwarding
// to HostDataExport with token.
func hostDataModule(server data.Principal, token string) string {
	encoded, _ := chordjson.Marshal(server)
	quoted, _ := json.Marshal(token)
	return strings.NewReplacer("__SERVER__", string(encoded), "__TOKEN__", string(quoted)).Replace(hostDataModuleText)
}

// HostDataFacet is the kit's worker facet on the worker's Go host serving HostDataExport for the worker's plugin store, its kit store and its files
// directory files, to the caller that has token.
func HostDataFacet(dir string, kit store.Store, token string) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(HostDataExport, func(_ *delta.JsonObject, _ host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"exportTo": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						object := args.(*delta.JsonObject)
						if subtle.ConstantTimeCompare([]byte(object.Value("token").(string)), []byte(token)) != 1 {
							return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: "chord.app.data: the server's only"}, nil)
						}
						// The server's own manifest for this address.
						var manifest ArchiveManifest
						if err := json.Unmarshal([]byte(object.Value("manifest").(string)), &manifest); err != nil {
							return nil, err
						}
						// The plugins' store is the NodeHost's own file (store.sqlite): a store opened on it now reads its state at this point, beside
						// the Go kit's (kit.sqlite).
						database, err := node.OpenNodeSqliteDatabase(filepath.Join(dir, archiveStore), node.NodeSqliteDatabaseOptions{})
						if err != nil {
							return nil, err
						}
						plugins, err := store.OpenStore(store.SqliteBackend(database), ctx, store.StoreOptions{})
						if err != nil {
							return nil, errors.Join(err, database.Close())
						}
						packed := PackStore(ctx, plugins, kit, filepath.Join(dir, "files"), manifest, object.Value("path").(string))
						return nil, errors.Join(packed, plugins.Close(ctx))
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
}
