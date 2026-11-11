package sessions

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/sessions/sidebar-settings.ts: the owner's order of the sidebar's rollups, each rollup's window
// and which rollups are collapsed, persisted in the server's store and served to the owner.

var sidebarSettingsState = mustState("p2.sidebar-settings-state", documents.StateOptions{
	Schema: p2runtime.Contract(schema.Obj(schema.Properties{
		schema.Prop("order", schema.Arr(schema.Str())),
		schema.Prop("windows", p2runtime.Record(contract.SidebarWindowSchema)),
		schema.Prop("collapsed", p2runtime.Record(schema.Bool())),
	})),
	Version: 1,
	Initial: func() *delta.JsonObject {
		return delta.JsonObjectOf("order", []any{}, "windows", delta.NewJsonObject(0), "collapsed", delta.NewJsonObject(0))
	},
	Persist: true,
	CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 20
	},
})

// serveSidebarSettings serves SidebarSettingsStore (the persisted state) and SidebarSettings (the owner's view).
func serveSidebarSettings(facet host.FacetHost) error {
	err := facet.Serve(contract.SidebarSettingsStore, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		doc := self.State(sidebarSettingsState).(*documents.Singleton)
		member := func(name string) chord.Watchable[delta.JsonValue] {
			return self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value(name), nil
			})
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"order": member("order"), "windows": member("windows"), "collapsed": member("collapsed")},
			Methods: map[string]contracts.MethodFunction{
				"setOrder": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in, _ := objectArgs(args).Value("order").([]any)
					unique := make([]any, 0, len(in))
					for _, key := range in {
						if !slices.Contains(unique, key) {
							unique = append(unique, key)
						}
					}
					_, err := doc.Change(ctx, func(draft *delta.Object) (any, error) {
						order := draft.Array("order")
						_, err := order.Splice(0, order.Len(), unique...)
						return nil, err
					})
					return nil, err
				},
				"setWindow": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					key, _ := in.Value("key").(string)
					_, err := doc.Change(ctx, func(draft *delta.Object) (any, error) {
						return nil, draft.Object("windows").Set(key, in.Value("window"))
					})
					return nil, err
				},
				"setCollapsed": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					key, _ := in.Value("key").(string)
					_, err := doc.Change(ctx, func(draft *delta.Object) (any, error) {
						return nil, draft.Object("collapsed").Set(key, in.Value("collapsed"))
					})
					return nil, err
				},
			},
		}}, nil
	}), host.ServeOptions{})
	if err != nil {
		return err
	}
	return facet.Serve(contract.SidebarSettings, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if _, owner := who.(p2runtime.OwnerPrincipal); err != nil || !owner {
			return contracts.Implementation{}, p2runtime.Forbidden("sidebar settings are the owner's")
		}
		store := self.Get(contract.SidebarSettingsStore, delta.NewJsonObject(0))
		methods := map[string]contracts.MethodFunction{}
		for _, name := range []string{"setOrder", "setWindow", "setCollapsed"} {
			methods[name] = func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return store.Call(name, args, ctx)
			}
		}
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"order":     host.Expose(store.Member("order")),
				"windows":   host.Expose(store.Member("windows")),
				"collapsed": host.Expose(store.Member("collapsed")),
			},
			Methods: methods,
		}}, nil
	}), host.ServeOptions{})
}
