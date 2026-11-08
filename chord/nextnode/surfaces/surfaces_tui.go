package surfaces

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/node/surfaces/tui.ts. The terminal surface's server side (slice 11 §4, decisions "Slice 11, P3" and
// "P8"): the terminal kind's base bundle (one CommonJS script the sandbox evaluates) built from the app's terminal entry after
// the server's start (`Build`; the launcher's start and the surface's files wait for it, and answer 503 when it failed), the
// kind's plugin scripts published as registered scripts and one combined file, `TuiScripts` showing the current set, all served
// to the launcher, which signs in with a token. Routes and objects are `Http` contributions of the surface's facet.

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

// TuiSurface is the terminal surface: its facet (for the server's core facets), its rollout step, its build.
type TuiSurface struct {
	// Facet contributes the surface's routes; Scripts serves its scripts objects (see WebSurface).
	Facet   host.Facet
	Scripts host.Facet

	building *surfaceBuild
}

// Step is the rollout step publishing the terminal kind's current set (after the build).
func (s *TuiSurface) Step(name string) plugins.RolloutStep { return s.building.step(name) }

// Build builds the base bundle and publishes the terminal kind's first set, once, after the server's load (the launcher's start
// and the surface's files wait for it, and start it when nothing did). It returns how it ended (`Err` when it did not build).
func (s *TuiSurface) Build() SurfaceBuilt { return s.building.build() }

// Built is how the build ended, once it did (nil before).
func (s *TuiSurface) Built() *SurfaceBuilt { return s.building.built() }

// TuiPrefix is where the terminal surface's files live (Pi's `TUI_PREFIX`).
const TuiPrefix = "/_chord/tui/"

// NewTuiSurface is the terminal surface on server: its facet serves the launcher's start and the surface's files once Build built
// the base bundle (503 when it failed). The build is not started here.
func NewTuiSurface(server host.Host, options TuiSurfaceOptions) *TuiSurface {
	kind := options.Kind
	if kind == "" {
		kind = "tui"
	}
	building := newSurfaceBuild(Tui, PlatformSandbox, TuiPrefix, surfaceBuildOptions{kind: kind, entry: options.Entry, conditions: options.Conditions, plugins: options.Plugins}, server)
	serve := func(request http.HttpRequest, ctx host.Ctx) (http.HttpResponse, error) {
		return building.file(request.Path(), ctx)
	}
	noStore := []http.ResponseHeader{http.HeaderOf("Cache-Control", "no-store")}
	facet := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http.Http), http.HttpContribution{Routes: []http.HttpRouteEntry{
			http.RouteFunc("GET "+TuiPrefix+"start", func(_ http.HttpRequest, ctx host.Ctx) (http.HttpResponse, error) {
				parts, err := building.parts(ctx)
				if parts == nil {
					return unavailable(err)
				}
				start := TuiStart{Base: parts.base.Hash, Script: parts.base.Script, Combined: parts.published.Set().Combined}
				return http.HttpResponse{Kind: http.KindJSON, Value: delta.JsonObjectOf("base", start.Base, "script", start.Script, "combined", start.Combined), Headers: noStore}, nil
			}),
			{Key: "GET " + TuiPrefix + "base", Route: http.HttpRouteOptions{Public: true, Handler: func(_ http.HttpRequest, ctx host.Ctx) (http.HttpResponse, error) {
				parts, err := building.parts(ctx)
				if parts == nil {
					return unavailable(err)
				}
				return http.HttpResponse{Kind: http.KindJSON, Value: parts.base.Hash, Headers: noStore}, nil
			}}},
			http.RouteFunc("GET "+TuiPrefix+":file", serve),
			http.RouteFunc("GET "+TuiPrefix+"plugin/:file", serve),
		}})
	})
	return &TuiSurface{Facet: facet, Scripts: ScriptsFacet(Tui, building.set), building: building}
}
