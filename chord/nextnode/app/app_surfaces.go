package app

import (
	"context"

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

// AppSurface is a running client surface: its base bundle's hash.
type AppSurface struct {
	Base string
}

// surface is a client surface of the app: its facet, its rollout step, its base bundle's inputs.
type surface struct {
	name   string
	facet  host.Facet
	step   func(name string) plugins.RolloutStep
	base   string
	inputs plugins.BaseInputs
}

// surfacesOf is the app's client surfaces, built (their base bundles) in order: the web's, the terminal's.
func surfacesOf(h host.Host, options AppOptions, p *plugins.Plugins, ctx context.Context) ([]surface, error) {
	var surfaces []surface
	if options.Web != nil {
		web, err := nodesurfaces.NewWebSurface(h, nodesurfaces.WebSurfaceOptions{
			Plugins: p, Entry: options.Web.Entry, Page: options.Web.Page, Paths: options.Web.Paths, Public: options.Web.Public,
			Conditions: options.Web.Conditions,
		}, ctx)
		if err != nil {
			return nil, err
		}
		surfaces = append(surfaces, surface{"web", web.Facet, web.Step, web.Base, web.Inputs})
	}
	if options.Tui != nil {
		tui, err := nodesurfaces.NewTuiSurface(h, nodesurfaces.TuiSurfaceOptions{
			Plugins: p, Entry: options.Tui.Entry, Conditions: options.Tui.Conditions,
		}, ctx)
		if err != nil {
			return nil, err
		}
		surfaces = append(surfaces, surface{"tui", tui.Facet, tui.Step, tui.Base, tui.Inputs})
	}
	return surfaces, nil
}
