package surfaces

import (
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/node/surfaces/web.ts. The web surface's server side (slice 11 §1, decisions "Slice 11's points" and
// "P3" to "P5"): the base bundle built from the app's entry after the server's start (`build`; the page and the surface's files
// wait for it, and answer 503 when it failed), the plugins' web scripts published as registered scripts and one combined file,
// `WebScripts` showing the current set, and the page through the app's own template;
// everything as `Http` contributions of the surface's facet, which the app puts among the server's core facets. The app keeps
// its HTML, CSS, components and themes.

// WebSurfaceOptions is what NewWebSurface takes.
type WebSurfaceOptions struct {
	// Kind is the web facet kind: `web` by default ("").
	Kind    string
	Plugins *plugins.Plugins
	// Entry is the app's web start code (it calls `startPage`): the base bundle's entry.
	Entry string
	// Page is the page's HTML: the app's template, given the request (its Nonce) and the assets to include.
	Page func(request http.HttpRequest, assets PageAssets) (string, error)
	// Paths are the paths serving the page: `["/"]` by default (nil; patterns, `/*` for a page with its own routing).
	Paths []string
	// Public is whether the page and its scripts are served without a principal (a public app); signed in only by default.
	Public bool
	// Conditions are the conditions the base bundle's packages resolve with: the server process's by default (nil;
	// ProcessConditions).
	Conditions []string
}

// PageAssets is what a page includes, in this order: Bootstrap as an inline script with the response's nonce (before the
// stylesheets: it records how each one's load ended), then the stylesheets Styles (the base bundle's first, then each plugin's)
// and the scripts Plugins and Script (deferred, in that order). PageScripts writes the tags.
type PageAssets struct {
	Bootstrap string
	Script    string
	Plugins   string
	Styles    []string
}

// WebSurface is the web surface: its facet (for the server's core facets), its rollout step, its build.
type WebSurface struct {
	// Facet contributes the surface's routes; Scripts serves its scripts objects (Pi's one `chord.web` facet does both: the Go
	// facet is opaque, so the app loads the two).
	Facet   host.Facet
	Scripts host.Facet

	building *surfaceBuild
}

// Step is the rollout step publishing the web kind's current set (after the server's step; after the build).
func (s *WebSurface) Step(name string) plugins.RolloutStep { return s.building.step(name) }

// Build builds the base bundle and publishes the web kind's first set (its facets built on first use), once, after the server's
// load (the page and the surface's files wait for it, and start it when nothing did). It returns how it ended (`Base`: the base
// bundle's hash and the files it read, the app's base code for this surface; `Err` when it did not build).
func (s *WebSurface) Build() SurfaceBuilt { return s.building.build() }

// Built is how the build ended, once it did (nil before).
func (s *WebSurface) Built() *SurfaceBuilt { return s.building.built() }

// WebPrefix is where the web surface's files live (Pi's `WEB_PREFIX`).
const WebPrefix = "/_chord/web/"

// NewWebSurface is the web surface on server: its facet serves the page and the surface's files once Build built the base bundle
// (503 when it failed). The build is not started here.
func NewWebSurface(server host.Host, options WebSurfaceOptions) *WebSurface {
	kind := options.Kind
	if kind == "" {
		kind = "web"
	}
	building := newSurfaceBuild(Web, PlatformBrowser, WebPrefix, surfaceBuildOptions{kind: kind, entry: options.Entry, conditions: options.Conditions, plugins: options.Plugins}, server)
	routes := webRoutes(options, building)
	facet := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http.Http), http.HttpContribution{
			Routes: routes,
			Csp: []http.CspEntry{
				{Directive: "style-src", Sources: []string{"'self'"}},
				{Directive: "img-src", Sources: []string{"'self'", "data:"}},
				{Directive: "font-src", Sources: []string{"'self'"}},
			},
			// Scripts from the surface's files only (and the page's nonce): no other same-origin response runs (Opus L8).
			Scripts: []string{WebPrefix},
		})
	})
	return &WebSurface{Facet: facet, Scripts: ScriptsFacet(Web, building.set), building: building}
}

// webRoutes are the surface's routes, each waiting for the build (503 when it failed): its files (served forever), its current
// base hash (public), and the page.
func webRoutes(options WebSurfaceOptions, building *surfaceBuild) []http.HttpRouteEntry {
	serve := func(request http.HttpRequest, ctx host.Ctx) (http.HttpResponse, error) {
		return building.file(request.Path(), ctx)
	}
	routes := []http.HttpRouteEntry{
		{Key: "GET " + WebPrefix + ":file", Route: http.HttpRouteOptions{Public: options.Public, Handler: serve}},
		{Key: "GET " + WebPrefix + "plugin/:file", Route: http.HttpRouteOptions{Public: options.Public, Handler: serve}},
		{Key: "GET " + WebPrefix + "assets/:file", Route: http.HttpRouteOptions{Public: options.Public, Handler: serve}},
		// Asked by a page before it reconnects: another base means its code is outdated. Public: an outdated page whose session
		// ended still learns it should reload.
		{Key: "GET " + WebPrefix + "base", Route: http.HttpRouteOptions{Public: true, Handler: func(_ http.HttpRequest, ctx host.Ctx) (http.HttpResponse, error) {
			parts, err := building.parts(ctx)
			if parts == nil {
				return unavailable(err)
			}
			return http.HttpResponse{Kind: http.KindJSON, Value: parts.base.Hash, Headers: []http.ResponseHeader{http.HeaderOf("Cache-Control", "no-store")}}, nil
		}}},
	}
	paths := options.Paths
	if paths == nil {
		paths = []string{"/"}
	}
	for _, path := range paths {
		routes = append(routes, http.HttpRouteEntry{Key: "GET " + path, Route: http.HttpRouteOptions{
			Public: options.Public, Page: true,
			Handler: func(request http.HttpRequest, ctx host.Ctx) (http.HttpResponse, error) {
				parts, err := building.parts(ctx)
				if parts == nil {
					return unavailable(err)
				}
				body, err := options.Page(request, pageAssets(parts.base, parts.published))
				if err != nil {
					return http.HttpResponse{}, err
				}
				return http.HttpResponse{Kind: http.KindHTML, Body: body}, nil
			},
		}})
	}
	return routes
}

// unavailable is the response of a route whose surface did not build (503), or the error that ended the wait for it.
func unavailable(err error) (http.HttpResponse, error) {
	if err != nil {
		return http.HttpResponse{}, err
	}
	return http.HttpResponse{Kind: http.KindError, Status: 503}, nil
}

// pageAssets is what the page includes now: the bootstrap, the base script, the current combined file, the stylesheets.
func pageAssets(base *BaseBundle, published *PublishedScripts) PageAssets {
	set := published.Set()
	var styles []string
	if base.Style != "" {
		styles = append(styles, base.Style)
	}
	for _, entry := range set.Plugins {
		if entry.CSS != nil {
			styles = append(styles, *entry.CSS)
		}
	}
	return PageAssets{Bootstrap: bootstrap(base.Hash, set), Script: base.Script, Plugins: set.Combined, Styles: styles}
}

// bootstrap is the page's inline bootstrap: the base hash and each plugin's stylesheet (set's, the combined file's) for
// `startPage`; how each stylesheet's load ended (`links`, by `href`: `load` or `error`, from a capturing listener the page's
// start removes: a failed load still has a `sheet`, empty); and the `chordFacet` the combined file's scripts call before the
// base bundle runs (they queue; `startPage` takes them).
func bootstrap(hash string, set next.ScriptSet) string {
	styles := delta.NewJsonObject(len(set.Plugins))
	for _, entry := range set.Plugins {
		if entry.CSS != nil {
			styles.Set(entry.Name, *entry.CSS)
		}
	}
	text, err := jsstring.MarshalJSON(delta.JsonObjectOf("base", hash, "styles", styles, "links", delta.NewJsonObject(0), "queued", []any{}))
	if err != nil {
		panic(err)
	}
	// Inside a script element: no `<` (a `</script>` in a name would end it).
	page := strings.ReplaceAll(string(text), "<", `\u003c`)
	watching := `chordPage.watching=function(e){var t=e.target;if(t&&t.tagName==="LINK")chordPage.links[t.getAttribute("href")]=e.type};` +
		`document.addEventListener("load",chordPage.watching,true);document.addEventListener("error",chordPage.watching,true);`
	return "globalThis.chordPage=" + page + ";" + watching + "globalThis." + next.Register + "=function(p,d,f){chordPage.queued.push([p,d,f])};"
}

// PageScripts is the page's tags for assets: the inline bootstrap with nonce, the stylesheets, then the deferred scripts.
func PageScripts(assets PageAssets, nonce string) string {
	lines := []string{`<script nonce="` + nonce + `">` + assets.Bootstrap + `</script>`}
	for _, href := range assets.Styles {
		lines = append(lines, `<link rel="stylesheet" href="`+href+`">`)
	}
	lines = append(lines, `<script defer src="`+assets.Plugins+`"></script>`, `<script defer src="`+assets.Script+`"></script>`)
	return strings.Join(lines, "\n")
}
