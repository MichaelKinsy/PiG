package app

import (
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
)

// Ports the `web` and `tui` options of packages/chord/src/node/app/app.ts and its `surfacesOf`.

// AppWeb is the kit's web surface options (WebSurfaceOptions without its kind `web` and the kit's plugins): the app's web entry
// (the base bundle's), its page template, the paths serving the page (`["/"]` by default), whether it is public, and the
// conditions its packages resolve with.
type AppWeb struct {
	Entry      string
	Page       func(request chordhttp.HttpRequest, assets nodesurfaces.PageAssets) (string, error)
	Paths      []string
	Public     bool
	Conditions []string
}

// AppTui is the kit's terminal surface options: the app's terminal entry and the conditions its packages resolve with (the
// server process's by default).
type AppTui struct {
	Entry      string
	Conditions []string
}

// AppSurface is a running client surface (Pi's `app.web` / `app.tui`): its base bundle's hash once built (BasesBuilt).
type AppSurface struct {
	built func() *nodesurfaces.SurfaceBuilt
}

// Base is the base bundle's hash once built (BasesBuilt); "" before, and when its build failed.
func (s *AppSurface) Base() string {
	if built := s.built(); built != nil && built.Err == nil {
		return built.Base
	}
	return ""
}

// surface is a client surface of the app: its facets, its rollout step, its build (the kit starts it right after the listen).
type surface struct {
	name   string
	facets []host.HostFacet
	step   func(name string) plugins.RolloutStep
	build  func() nodesurfaces.SurfaceBuilt
	built  func() *nodesurfaces.SurfaceBuilt
}

// surfacesOf is the app's client surfaces, in order: the web's, the terminal's (their base bundles built after the listen).
func surfacesOf(h host.Host, options AppOptions, p *plugins.Plugins) []surface {
	var surfaces []surface
	if options.Web != nil {
		web := nodesurfaces.NewWebSurface(h, nodesurfaces.WebSurfaceOptions{
			Plugins: p, Entry: options.Web.Entry, Page: options.Web.Page, Paths: options.Web.Paths, Public: options.Web.Public,
			Conditions: options.Web.Conditions,
		})
		surfaces = append(surfaces, surface{"web", []host.HostFacet{{Name: "chord.web", Facet: web.Facet}, {Name: "chord.web.scripts", Facet: web.Scripts}}, web.Step, web.Build, web.Built})
	}
	if options.Tui != nil {
		tui := nodesurfaces.NewTuiSurface(h, nodesurfaces.TuiSurfaceOptions{
			Plugins: p, Entry: options.Tui.Entry, Conditions: options.Tui.Conditions,
		})
		surfaces = append(surfaces, surface{"tui", []host.HostFacet{{Name: "chord.tui", Facet: tui.Facet}, {Name: "chord.tui.scripts", Facet: tui.Scripts}}, tui.Step, tui.Build, tui.Built})
	}
	return surfaces
}
