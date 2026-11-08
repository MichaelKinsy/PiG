package surfacehost

import (
	"context"
	"sort"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// the desktop is a third surface beside Chord's web and terminal ones, defined here and not in
// Chord's Pi-shaped surfaces packages. Its two contracts have the shape of the web and terminal ones
// (chord.web.scripts and chord.web.current: surfaces.ScriptSetSchema as state, `publish` to change it), and its server
// facet is the same facet as nextnode/surfaces.ScriptsFacet over them, written with the public host API. Nothing is
// registered in Chord: the facet reaches the in-process p2 server through hosts.ServerOptions.Facets.

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// DesktopScripts is the current scripts of the desktop client kind (a surfaces.ScriptSet), a remote object on the server,
// the counterpart of chord.web.scripts. The desktop's scripts are Go functions compiled into the app, so the set is how
// the server says which scripts it expects and which base the client must run.
var DesktopScripts = must(contracts.Remote("chord.desktop.scripts", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		State: []contracts.Property{contracts.Prop("set", surfaces.ScriptSetSchema)},
	})},
}))

// desktopCurrent is the local object holding the published set, the counterpart of chord.web.current.
var desktopCurrent = must(contracts.Local("chord.desktop.current", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("set", surfaces.ScriptSetSchema),
		contracts.Mem("publish", contracts.NewMethod(surfaces.ScriptSetSchema)),
	},
}))

// ScriptsFacet is the server facet serving the desktop's published set (starting at published()) and its scripts object
// to every principal that may hold it (nextnode/surfaces.ScriptsFacet's for the web and terminal).
func ScriptsFacet(published func() surfaces.ScriptSet) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		err := h.Serve(desktopCurrent, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			set := self.Writable(published().JSON())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"set": self.Derive([]any{set}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"publish": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return nil, set.Set(args) },
				},
			}}, nil
		}, host.ServeOptions{})
		if err != nil {
			return err
		}
		return h.Serve(DesktopScripts, func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.Implementation, error) {
			now := self.Get(desktopCurrent, delta.NewJsonObject(0))
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"set": self.Derive([]any{now.Member("set")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

// BaseHash names the client's base: the code of this build's host packages. A server publishing another base says the
// client is outdated (the app is replaced, as a page reloads). It changes only with this contract's version.
const BaseHash = "p2desktop-base-1"

// SetOf is the set a build publishes for plugins: its base, no combined file (the scripts are in the binary), each plugin's
// name and digest in name order.
func SetOf(plugins []surfaces.RegisteredScript) surfaces.ScriptSet {
	set := surfaces.ScriptSet{Base: BaseHash}
	for _, script := range plugins {
		set.Plugins = append(set.Plugins, surfaces.ScriptEntry{Name: script.Plugin, Digest: script.Digest, URL: "builtin:" + script.Plugin})
	}
	sort.Slice(set.Plugins, func(i, j int) bool { return set.Plugins[i].Name < set.Plugins[j].Name })
	return set
}

// Publish sets the set the server shows every desktop client, as a rollout does (the counterpart of the `publish` of
// chord.web.current): each client's scripts object gets one frame.
func Publish(h host.Host, set surfaces.ScriptSet) error {
	_, err := h.Root(func(ctx host.Ctx) (any, error) {
		current, err := ctx.Get(desktopCurrent, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return current.Call("publish", set.JSON(), ctx)
	}, chordctx.Background(), host.RootOptions{})
	return err
}

// ServerFacet is the server facet to load for a build with plugins: it serves the desktop surface with the set SetOf says.
// A client waits for the scripts object's first value, so a server without it leaves the desktop host connecting.
func ServerFacet(plugins []surfaces.RegisteredScript) host.HostFacet {
	return host.HostFacet{Name: "p2desktop.surface", Facet: ScriptsFacet(func() surfaces.ScriptSet { return SetOf(plugins) })}
}
