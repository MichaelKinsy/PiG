package surfaces

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	clientsurfaces "github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Ports packages/chord/src/node/surfaces/current.ts. The scripts objects a surface serves (slice 11 §1, decision "Slice 11,
// P5"): on the server, one local object per surface holds the published set (a writable, set by each publish), and the
// surface's scripts object (`WebScripts`, `TuiScripts`), made per principal, shows it. A rollout publishes without waiting for
// clients: each client's hold gets one frame, and a reconnect's snapshot brings the set it missed.
//
// Forced Go difference. Pi's `scriptsFacet` returns a Facet its callers call `setup` on; Go's Facet is opaque, so
// serveScripts is the setup function itself, called from the surface's facet.

// SurfaceName is a surface: the web (pages) or the terminal.
type SurfaceName string

const (
	// SurfaceWeb is the web surface.
	SurfaceWeb SurfaceName = "web"
	// SurfaceTui is the terminal surface.
	SurfaceTui SurfaceName = "tui"
)

// current is the local object holding a surface's published set, by surface.
var current = map[SurfaceName]*contracts.Contract{
	SurfaceWeb: currentContract("chord.web.current"),
	SurfaceTui: currentContract("chord.tui.current"),
}

// scripts is the scripts object each surface serves.
var scripts = map[SurfaceName]*contracts.Contract{SurfaceWeb: clientsurfaces.WebScripts, SurfaceTui: clientsurfaces.TuiScripts}

// currentContract is the contract of a surface's published set: its `set` and `publish`.
func currentContract(name string) *contracts.Contract {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("set", clientsurfaces.ScriptSetSchema),
			contracts.Mem("publish", contracts.NewMethod(clientsurfaces.ScriptSetSchema)),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

// serveScripts is the setup of the surface's facet serving its published set (starting at published's set, the set taken before
// the load) and its scripts object for every principal that may hold it.
func serveScripts(surface SurfaceName, published *PublishedScripts) func(host.FacetHost) error {
	currentType := current[surface]
	return func(facet host.FacetHost) error {
		if err := facet.Serve(currentType, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			// The latest set: an attempt after a failure starts at what was published last.
			set := self.Writable(published.Set().JSON())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"set": self.Derive([]any{set}, func(values []any) (delta.JsonValue, error) { return values[0].(delta.JsonValue), nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"publish": func(next delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						return nil, set.Set(next)
					},
				},
			}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(scripts[surface], func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.Implementation, error) {
			now := self.Get(currentType, delta.NewJsonObject(0))
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"set": self.Derive([]any{now.Member("set")}, func(values []any) (delta.JsonValue, error) { return values[0].(delta.JsonValue), nil }),
				},
			}}, nil
		}, host.ServeOptions{})
	}
}

// publishTo publishes set as surface's current set on h (loaded with serveScripts).
func publishTo(h host.Host, surface SurfaceName, set clientsurfaces.ScriptSet, ctx context.Context) error {
	reference, err := h.Get(ctx, current[surface], delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	defer reference.Release()
	_, err = reference.Call("publish", set.JSON(), ctx)
	return err
}
