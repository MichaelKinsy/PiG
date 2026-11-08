package surfaces

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/node/surfaces/tui.ts. The terminal surface's server side (slice 11 §4, decisions "Slice 11, P3" and
// "P8"): the terminal kind's base bundle (one CommonJS script the sandbox evaluates) built from the app's terminal entry at
// start, the kind's plugin scripts published as registered scripts and one combined file, `TuiScripts` showing the current
// set, all served to the launcher, which signs in with a token. Routes and objects are `Http` contributions of the surface's
// facet.

// TuiSurfaceOptions is what NewTuiSurface takes.
type TuiSurfaceOptions struct {
	// Kind is the terminal facet kind: `tui` by default ("").
	Kind    string
	Plugins *plugins.Plugins
	// Entry is the app's terminal start code (it calls `startTerminalRuntime`): the base bundle's entry.
	Entry string
	// Conditions are the conditions the base bundle's packages resolve with: the server process's by default (nil).
	Conditions []string
}

// TuiStart is what a launcher downloads at its start (`GET /_chord/tui/start`): the base bundle and the combined plugin file.
type TuiStart struct {
	Base     string `json:"base"`
	Script   string `json:"script"`
	Combined string `json:"combined"`
}

// TuiSurface is the terminal surface: its facet (for the server's core facets), its rollout step, its base bundle's hash.
type TuiSurface struct {
	// Facet contributes the surface's routes; Scripts serves its scripts objects (see WebSurface).
	Facet   host.Facet
	Scripts host.Facet
	// Base is its base bundle's hash.
	Base string
	// Inputs are the files its base bundle was built from, with their bytes' hashes: the app's base code for this surface.
	Inputs plugins.BaseInputs

	published *PublishedScripts
}

// Step is the rollout step publishing the terminal kind's current set.
func (s *TuiSurface) Step(name string) plugins.RolloutStep { return s.published.Step(name) }

// TuiPrefix is where the terminal surface's files live (Pi's `TUI_PREFIX`).
const TuiPrefix = "/_chord/tui/"

// NewTuiSurface builds the terminal's base bundle and takes the kind's current set, then returns the surface.
func NewTuiSurface(server host.Host, options TuiSurfaceOptions, ctx context.Context) (*TuiSurface, error) {
	base, err := BuildBase(options.Entry, PlatformSandbox, TuiPrefix, options.Conditions)
	if err != nil {
		return nil, err
	}
	kind := options.Kind
	if kind == "" {
		kind = "tui"
	}
	published := NewPublishedScripts(Tui, kind, TuiPrefix, base.Hash, options.Plugins, server)
	if _, err := published.Start(ctx); err != nil {
		return nil, err
	}
	file := func(path string) *ServedFile {
		if found, ok := base.Files[path]; ok {
			return &found
		}
		return published.File(path)
	}
	serve := func(request http.HttpRequest, _ host.Ctx) (http.HttpResponse, error) {
		return Served(file(request.Path())), nil
	}
	noStore := []http.ResponseHeader{http.HeaderOf("Cache-Control", "no-store")}
	facet := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http.Http), http.HttpContribution{Routes: []http.HttpRouteEntry{
			http.RouteFunc("GET "+TuiPrefix+"start", func(http.HttpRequest, host.Ctx) (http.HttpResponse, error) {
				start := TuiStart{Base: base.Hash, Script: base.Script, Combined: published.Set().Combined}
				return http.HttpResponse{Kind: http.KindJSON, Value: delta.JsonObjectOf("base", start.Base, "script", start.Script, "combined", start.Combined), Headers: noStore}, nil
			}),
			{Key: "GET " + TuiPrefix + "base", Route: http.HttpRouteOptions{Public: true, Handler: func(http.HttpRequest, host.Ctx) (http.HttpResponse, error) {
				return http.HttpResponse{Kind: http.KindJSON, Value: base.Hash, Headers: noStore}, nil
			}}},
			http.RouteFunc("GET "+TuiPrefix+":file", serve),
			http.RouteFunc("GET "+TuiPrefix+"plugin/:file", serve),
		}})
	})
	return &TuiSurface{Facet: facet, Scripts: ScriptsFacet(Tui, published.Set), Base: base.Hash, Inputs: base.Inputs, published: published}, nil
}
