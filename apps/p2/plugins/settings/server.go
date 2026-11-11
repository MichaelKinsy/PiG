// Package settings is p2's settings plugin (apps/p2/plugins/settings/server.ts): the Settings sheet's server side,
// settings.json as the model configuration holds it, changed through its save.
package settings

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	settingscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/settings/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/settings/server.ts.

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

// Server is the plugin's server facet: it serves `Settings` to owners only.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return facet.Serve(settingscontract.Settings, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("settings are owner-only")
		}
		config := self.Get(contract.ModelConfig, delta.NewJsonObject(0))
		native := p2runtime.NativeOf[contract.ModelResourcesValue](self.Registry(contract.ModelResources), "The model configuration")
		settings := self.Derive([]any{config.Member("snapshot")}, func(values []any) (delta.JsonValue, error) {
			return objectOf(values[0]).Value("settings"), nil
		})
		failure := self.Derive([]any{config.Member("snapshot")}, func(values []any) (delta.JsonValue, error) {
			errs, _ := objectOf(values[0]).Value("errors").([]any)
			for _, each := range errs {
				if entry := objectOf(each); entry != nil && entry.Value("resource") == "settings" {
					return entry.Value("message"), nil
				}
			}
			return nil, nil
		})
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"settings": settings, "error": failure},
			Methods: map[string]contracts.MethodFunction{
				"update": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// The method blocks on the model configuration's reload and save, which the host's loop must be free to serve: it
					// suspends out of its section first, as the models plugin's own saves do.
					chordctx.Suspend(ctx)
					value, err := native()
					if err != nil {
						return nil, err
					}
					opened := value.Resources()
					input := objectOf(args)
					if input == nil {
						return nil, errors.New("update takes {set, unset}")
					}
					// Wire input: a partial settings.json under the same closed schema, so no unknown setting gets in.
					changes, err := p2runtime.ValidateSettings(input.Value("set"))
					if err != nil {
						return nil, err
					}
					unset, _ := input.Value("unset").([]any)
					// Merged into the file's settings as they are now, not the page's copy: a default model chosen in the
					// picker or an edit of the file meanwhile stays. The watcher may not have seen an edit yet: read it first.
					if err := opened.Reload(ctx); err != nil {
						return nil, err
					}
					next := delta.NewJsonObject(0)
					if current := opened.Settings(); current != nil {
						for key, each := range current.All() {
							next.Set(key, each)
						}
					}
					for key, each := range changes.All() {
						next.Set(key, each)
					}
					for _, key := range unset {
						if name, ok := key.(string); ok {
							next.Delete(name)
						}
					}
					if err := opened.SaveSettings(ctx, next); err != nil {
						return nil, err
					}
					return nil, nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
})
