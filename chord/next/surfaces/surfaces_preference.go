package surfaces

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/surfaces/preference.ts. A user's preference kept by the server (slice 11 §4b): one pick per
// principal, kept by the kit's server and held by every page and terminal of that principal, so choosing restyles or
// re-renders them all at once. Bounded: the store keeps the picks of the maxPicks principals who chose last (a public
// app's guests choose too).

// maxPicks is how many principals' picks a store keeps: past it the earliest chooser's goes (they get the preference's
// none).
const maxPicks = 10_000

var serverKind = func() *contracts.HostKind {
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		panic(err)
	}
	return kind
}()

// PreferenceContract is a preference's remote contract name: the caller's pick on the server (`pick`), and `choose` to
// change it.
func PreferenceContract(name string, pick contracts.Schema) (*contracts.Contract, error) {
	return contracts.Remote(name, contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
			return serverKind.Address(nil), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("pick", pick)},
			Methods: []contracts.MethodEntry{contracts.Meth("choose", contracts.NewMethod(pick))},
		})},
	})
}

// PreferenceFacet is the kit's server facet of the preference choice: every principal's pick in one persisted document
// of the store store by principal key, and each principal's Choice (none before a choice).
func PreferenceFacet(choice *contracts.Contract, store string, pick contracts.Schema, none delta.JsonValue) (host.Facet, error) {
	picks, err := documents.DefineState(store, documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("picks", contracts.Map(pick))),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("picks", delta.NewJsonObject(0)) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 50
		},
	})
	if err != nil {
		return host.Facet{}, err
	}
	// A persisted state needs a local type with empty params.
	pickStore, err := contracts.Local(store, contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("picks", contracts.Map(pick)),
			contracts.Mem("set", contracts.NewMethod(contracts.Obj(contracts.Prop("key", contracts.Str()), contracts.Prop("pick", pick)))),
		},
	})
	if err != nil {
		return host.Facet{}, err
	}
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(pickStore, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			document := self.State(picks).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"picks": self.Derive([]any{document}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("picks"), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						arguments := args.(*delta.JsonObject)
						key, next := arguments.Value("key").(string), arguments.Value("pick")
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							all := draft.Object("picks")
							// Re-added last: the keys keep the order of choosing, the first the earliest.
							all.Delete(key)
							if err := all.Set(key, next); err != nil {
								return nil, err
							}
							keys := all.Keys()
							for _, old := range keys[:max(0, len(keys)-maxPicks)] {
								all.Delete(old)
							}
							return nil, nil
						})
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(choice, func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
			picks := self.Get(pickStore, delta.NewJsonObject(0))
			key := data.PrincipalKey(principal, chordctx.Background(), nil)
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					// A key that is a principal's canonical JSON: only an own key is a pick.
					"pick": self.Derive([]any{picks.Member("picks")}, func(values []any) (delta.JsonValue, error) {
						if all, ok := values[0].(*delta.JsonObject); ok && all.Has(key) {
							return all.Value(key), nil
						}
						return none, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"choose": func(next delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return picks.Call("set", delta.JsonObjectOf("key", key, "pick", next), ctx)
					},
				},
			}}, nil
		}, host.ServeOptions{})
	}), nil
}
