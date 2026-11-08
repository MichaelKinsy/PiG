package surfaces

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Ports packages/chord/src/node/surfaces/current.ts. The scripts objects a surface serves (slice 11 §1, decision "Slice 11, P5"):
// on the server, one local object per surface holds the published set (a writable, set by each publish), and the surface's
// scripts object (WebScripts, TuiScripts), made per principal, shows it. A rollout publishes without waiting for clients: each
// client's hold gets one frame, and a reconnect's snapshot brings the set it missed.

// SurfaceName is a surface: the web (pages) or the terminal.
type SurfaceName string

const (
	Web SurfaceName = "web"
	Tui SurfaceName = "tui"
)

// current is the local object holding a surface's published set, by surface.
var current = map[SurfaceName]*contracts.Contract{
	Web: currentContract("chord.web.current"),
	Tui: currentContract("chord.tui.current"),
}

// scripts is the scripts object each surface serves.
var scripts = map[SurfaceName]*contracts.Contract{Web: next.WebScripts, Tui: next.TuiScripts}

// currentContract is the contract of a surface's published set: its `set` and `publish`.
func currentContract(name string) *contracts.Contract {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("set", next.ScriptSetSchema),
			contracts.Mem("publish", contracts.NewMethod(next.ScriptSetSchema)),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

// ScriptsFacet is the surface's facet serving its published set (starting at published() when first held: the surface's first set,
// or an empty one before its build, which publishes the first; it is read at each attempt) and its scripts object for every principal that may hold it.
//
// A surface that is none of the two fails the facet's load, as PublishTo refuses it.
func ScriptsFacet(surface SurfaceName, published func() next.ScriptSet) host.Facet {
	contract, ok := current[surface]
	if !ok {
		return host.DefineFacet(func(host.FacetHost, host.FacetSelf) error {
			return fmt.Errorf("surfaces: unknown surface %q", surface)
		})
	}
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		err := h.Serve(contract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			// The latest set: an attempt after a failure starts at what was published last.
			set := self.Writable(published().JSON())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"set": self.Derive([]any{set}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"publish": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						return nil, set.Set(args)
					},
				},
			}}, nil
		}, host.ServeOptions{})
		if err != nil {
			return err
		}
		return h.Serve(scripts[surface], func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.Implementation, error) {
			now := self.Get(contract, delta.NewJsonObject(0))
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"set": self.Derive([]any{now.Member("set")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

// PublishTo publishes set as surface's current set on h (loaded with ScriptsFacet).
func PublishTo(h host.Host, surface SurfaceName, set next.ScriptSet, ctx context.Context) error {
	contract, ok := current[surface]
	if !ok {
		return fmt.Errorf("surfaces: unknown surface %q", surface)
	}
	reference, err := h.Get(ctx, contract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	defer reference.Release()
	_, err = reference.Call("publish", set.JSON(), ctx)
	return err
}
