package http

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Tests of the router (slice 10 §2, D2, D8): patterns, decoding (one rule for `:name` and rest segments), precedence,
// `HEAD` per path, each route's own param names, route options, conflicts, wraps resolved by phase, contributor order,
// scope and route, allowed hosts, framers, relays, CSP sources, sockets. Ports test/next-node/http/router.test.ts.

// routerRoute is a route named name (its handler answers its name).
func routerRoute(name string) HttpRoute {
	return func(HttpRequest, host.Ctx) (HttpResponse, error) {
		return HttpResponse{Kind: KindHTML, Body: name}, nil
	}
}

// routerEntry is a plugin and its contribution.
type routerEntry struct {
	plugin string
	value  HttpContribution
}

// routerCompose is the router of entries, with the conflicts it reported.
func routerCompose(t *testing.T, entries ...routerEntry) (*Router, []registries.RegistryConflict) {
	t.Helper()
	var conflicts []registries.RegistryConflict
	table := registries.RegistryTable[HttpContribution]{}
	for _, entry := range entries {
		table = append(table, registries.RegistryEntry[HttpContribution]{Plugin: entry.plugin, Version: 1, Value: entry.value})
	}
	router, err := ComposeRouter(table, registries.ComposeContext{Report: func(conflict registries.RegistryConflict) {
		conflicts = append(conflicts, conflict)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return router, conflicts
}

func routerParamsEqual(got, want map[string]string) bool {
	return (len(got) == 0 && len(want) == 0) || reflect.DeepEqual(got, want)
}

// routerExpect checks the route matched for method and path: its pattern ("" for any) and its params (exactly when exact,
// else the listed ones).
func routerExpect(t *testing.T, router *Router, method, path, pattern string, params map[string]string, exact bool) {
	t.Helper()
	match, status := router.Route(method, path)
	if status != Found {
		t.Fatalf("Route(%s, %q) = lookup %v, want a match", method, path, status)
	}
	if pattern != "" && match.Pattern != pattern {
		t.Fatalf("Route(%s, %q).Pattern = %q, want %q", method, path, match.Pattern, pattern)
	}
	if exact {
		if !routerParamsEqual(match.Params, params) {
			t.Fatalf("Route(%s, %q).Params = %v, want %v", method, path, match.Params, params)
		}
		return
	}
	for name, value := range params {
		if got, ok := match.Params[name]; !ok || got != value {
			t.Fatalf("Route(%s, %q).Params[%q] = %q, want %q", method, path, name, got, value)
		}
	}
}

func routerExpectLookup(t *testing.T, router *Router, method, path string, want Lookup) {
	t.Helper()
	if match, status := router.Route(method, path); status != want || (want != Found && match != nil) {
		t.Fatalf("Route(%s, %q) = %v, lookup %v, want lookup %v", method, path, match, status, want)
	}
}

func routerPatternsRouter(t *testing.T) *Router {
	router, _ := routerCompose(t, routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /notes", routerRoute("list")),
		RouteFunc("GET /notes/:id", routerRoute("one")),
		RouteFunc("GET /notes/new", routerRoute("new")),
		RouteFunc("GET /notes/:id/export", routerRoute("export")),
		RouteFunc("GET /files/*", routerRoute("files")),
		RouteFunc("GET /files/special/:name", routerRoute("special")),
		RouteFunc("GET /", routerRoute("root")),
	}}})
	return router
}

func TestRouterPatternsAndPrecedence(t *testing.T) {
	t.Run("matches exact segments, :name and a trailing *, the most specific from the left", func(t *testing.T) {
		router := routerPatternsRouter(t)
		routerExpect(t, router, "GET", "/notes", "GET /notes", map[string]string{}, true)
		routerExpect(t, router, "GET", "/notes/new", "GET /notes/new", map[string]string{}, true)
		routerExpect(t, router, "GET", "/notes/7", "GET /notes/:id", map[string]string{"id": "7"}, true)
		routerExpect(t, router, "GET", "/notes/7/export", "", map[string]string{"id": "7"}, false)
		routerExpect(t, router, "GET", "/files/special/x", "GET /files/special/:name", map[string]string{"name": "x"}, true)
		// The exact branch fails deeper: the rest takes it.
		routerExpect(t, router, "GET", "/files/special/x/y", "GET /files/*", map[string]string{"*": "special/x/y"}, true)
		routerExpect(t, router, "GET", "/", "GET /", map[string]string{}, true)
	})

	t.Run("a /* matches its prefix with an empty rest; :name needs a non-empty segment", func(t *testing.T) {
		router := routerPatternsRouter(t)
		routerExpect(t, router, "GET", "/files", "GET /files/*", map[string]string{"*": ""}, true)
		routerExpectLookup(t, router, "GET", "/notes/", NoMatch)
	})

	// Opus's build review F4, security audit H5: `%2F` in `:name` reached a file path.
	t.Run("percent-decodes :name and rest segments; one decoding to /, \\, NUL, . or .. is a bad path", func(t *testing.T) {
		router := routerPatternsRouter(t)
		routerExpect(t, router, "GET", "/notes/a%20b", "", map[string]string{"id": "a b"}, false)
		routerExpect(t, router, "GET", "/files/a%20b/c", "", map[string]string{"*": "a b/c"}, false)
		for _, bad := range []string{"a%2Fb", "..%2F..%2Fsecret", "a%5Cb", "a%00b", "%2e%2e", "%2E", ".", ".."} {
			routerExpectLookup(t, router, "GET", "/notes/"+bad, BadPath)
			routerExpectLookup(t, router, "GET", "/files/x/"+bad, BadPath)
		}
		routerExpectLookup(t, router, "GET", "/notes/%E0%A4%A", BadPath)
	})

	t.Run("falls back to the GET route for HEAD; another method or path has none", func(t *testing.T) {
		router := routerPatternsRouter(t)
		routerExpect(t, router, "HEAD", "/notes", "GET /notes", map[string]string{}, true)
		routerExpectLookup(t, router, "POST", "/notes", NoMatch)
		routerExpectLookup(t, router, "GET", "/nope", NoMatch)
	})

	// Opus's build review F2, Astra's: one explicit HEAD route anywhere hid every GET fallback.
	t.Run("falls back to GET per path when another path has an explicit HEAD route", func(t *testing.T) {
		router, _ := routerCompose(t, routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{
			RouteFunc("HEAD /download", routerRoute("head")),
			RouteFunc("GET /download", routerRoute("get")),
			RouteFunc("GET /notes", routerRoute("notes")),
		}}})
		routerExpect(t, router, "HEAD", "/download", "HEAD /download", nil, false)
		routerExpect(t, router, "HEAD", "/notes", "GET /notes", nil, false)
	})

	// Opus's build review F3, Astra's: the first route's param name was used for every route at that position.
	t.Run("names each route's params by its own pattern", func(t *testing.T) {
		router, _ := routerCompose(t, routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{
			RouteFunc("GET /:id/a", routerRoute("a")),
			RouteFunc("GET /:name/b", routerRoute("b")),
		}}})
		routerExpect(t, router, "GET", "/v/a", "GET /:id/a", map[string]string{"id": "v"}, true)
		routerExpect(t, router, "GET", "/v/b", "GET /:name/b", map[string]string{"name": "v"}, true)
	})

	t.Run("takes a route with options: page and crossSite", func(t *testing.T) {
		router, _ := routerCompose(t, routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{
			{Key: "GET /p", Route: HttpRouteOptions{Handler: routerRoute("p"), Page: true}},
			{Key: "POST /hook", Route: HttpRouteOptions{Handler: routerRoute("h"), CrossSite: true}},
		}}})
		if match, _ := router.Route("GET", "/p"); match == nil || !match.Page || match.CrossSite {
			t.Fatalf("GET /p = %+v, want page and not crossSite", match)
		}
		if match, _ := router.Route("POST", "/hook"); match == nil || match.Page || !match.CrossSite {
			t.Fatalf("POST /hook = %+v, want crossSite and not page", match)
		}
		routerExpectLookup(t, router, "GET", "/notes", NoMatch)
	})
}

func TestRouterConflicts(t *testing.T) {
	t.Run("reports one pattern from two contributions (also with other param names); the first by plugin name wins", func(t *testing.T) {
		router, conflicts := routerCompose(t,
			routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{RouteFunc("GET /x/:id", routerRoute("a"))}}},
			routerEntry{"@app/b", HttpContribution{
				Routes:  []HttpRouteEntry{RouteFunc("GET /x/:key", routerRoute("b")), RouteFunc("BAD", routerRoute("bad"))},
				Sockets: []HttpSocketEntry{{Pattern: "nope", Handler: &HttpSocket{}}},
			}},
		)
		if match, _ := router.Route("GET", "/x/1"); match == nil || match.Plugin != "@app/a" {
			t.Fatalf("GET /x/1 = %+v, want plugin @app/a", match)
		}
		want := []registries.RegistryConflict{
			{Message: `route "GET /x/:key" is contributed twice`, Plugins: []string{"@app/a", "@app/b"}},
			{Message: `route "BAD" is no "METHOD /pattern"`, Plugins: []string{"@app/b"}},
			{Message: `socket "nope" is no pattern`, Plugins: []string{"@app/b"}},
		}
		if !reflect.DeepEqual(conflicts, want) {
			t.Fatalf("conflicts = %v, want %v", conflicts, want)
		}
	})

	t.Run("refuses a route for a method a Web Request cannot carry, reported", func(t *testing.T) {
		router, conflicts := routerCompose(t, routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{
			RouteFunc("TRACE /x", routerRoute("t")),
			RouteFunc("CONNECT /y", routerRoute("c")),
		}}})
		routerExpectLookup(t, router, "TRACE", "/x", NoMatch)
		want := []registries.RegistryConflict{
			{Message: `route "TRACE /x": TRACE is served for no resource (501)`, Plugins: []string{"@app/a"}},
			{Message: `route "CONNECT /y": CONNECT is served for no resource (501)`, Plugins: []string{"@app/a"}},
		}
		if !reflect.DeepEqual(conflicts, want) {
			t.Fatalf("conflicts = %v, want %v", conflicts, want)
		}
	})
}

// routerWrap is a wrap named name in phase, passing the request on; scope and route as given.
func routerWrap(name string, phase WrapPhase, scope, route string) HttpWrap {
	return HttpWrap{
		Name: name, Phase: phase, Scope: scope, Route: route,
		Wrap: func(request HttpRequest, next func(HttpRequest) (HttpResponse, error), _ host.Ctx) (HttpResponse, error) {
			return next(request)
		},
	}
}

func routerWrapNames(t *testing.T, router *Router, path string) []string {
	t.Helper()
	match, status := router.Route("GET", path)
	if status != Found {
		t.Fatalf("Route(GET, %q) = lookup %v, want a match", path, status)
	}
	names := []string{}
	for _, resolved := range match.Wraps {
		names = append(names, resolved.Wrap.Name)
	}
	return names
}

func TestRouterWraps(t *testing.T) {
	t.Run("orders by phase (outermost first), then contributor order; own wraps around their plugin's routes only", func(t *testing.T) {
		router, _ := routerCompose(t,
			routerEntry{"@app/a", HttpContribution{
				Routes: []HttpRouteEntry{RouteFunc("GET /a", routerRoute("a"))},
				Wraps:  []HttpWrap{routerWrap("a-transform", PhaseTransform, "", ""), routerWrap("a-observe", PhaseObserve, "", "")},
			}},
			routerEntry{"@app/b", HttpContribution{
				Routes: []HttpRouteEntry{RouteFunc("GET /b", routerRoute("b"))},
				Wraps:  []HttpWrap{routerWrap("b-all", PhaseHeaders, ScopeAll, ""), routerWrap("b-own", PhaseObserve, "", "")},
			}},
		)
		if got, want := routerWrapNames(t, router, "/a"), []string{"a-observe", "b-all", "a-transform"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("wraps of /a = %v, want %v", got, want)
		}
		if got, want := routerWrapNames(t, router, "/b"), []string{"b-own", "b-all"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("wraps of /b = %v, want %v", got, want)
		}
	})

	t.Run("narrows to one route; a route nobody contributes is a reported conflict", func(t *testing.T) {
		router, conflicts := routerCompose(t, routerEntry{"@app/a", HttpContribution{
			Routes: []HttpRouteEntry{RouteFunc("GET /a", routerRoute("a")), RouteFunc("GET /b", routerRoute("b"))},
			Wraps: []HttpWrap{
				routerWrap("only-a", PhaseObserve, "", "GET /a"),
				routerWrap("ghost", PhaseObserve, "", "GET /c"),
			},
		}})
		if got, want := routerWrapNames(t, router, "/a"), []string{"only-a"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("wraps of /a = %v, want %v", got, want)
		}
		if got := routerWrapNames(t, router, "/b"); len(got) != 0 {
			t.Fatalf("wraps of /b = %v, want none", got)
		}
		want := []registries.RegistryConflict{
			{Message: `wrap "ghost" names route "GET /c", which nobody contributes`, Plugins: []string{"@app/a"}},
		}
		if !reflect.DeepEqual(conflicts, want) {
			t.Fatalf("conflicts = %v, want %v", conflicts, want)
		}
	})
}

func TestRouterHostsAndSockets(t *testing.T) {
	t.Run("collects hosts lowercased, framers, relays and CSP sources; matches sockets with their largest frame", func(t *testing.T) {
		terminal := &HttpSocket{}
		relay := &HttpRelay{Name: "radius", Verify: func(*http.Request) (*RelayVerified, error) { return nil, nil }}
		contribution := HttpContribution{
			Hosts:      []string{"Notes.Example.com"},
			Sockets:    []HttpSocketEntry{{Pattern: "/terminal/:id", Handler: terminal}},
			MaxPayload: 10,
			Framers:    []string{"https://frame.example.com"},
			Relays:     []*HttpRelay{relay},
			Csp:        []CspEntry{{Directive: "img-src", Sources: []string{"https://cdn.example.com"}}},
		}
		router, _ := routerCompose(t,
			routerEntry{"@app/a", contribution},
			routerEntry{"@app/b", HttpContribution{Hosts: []string{"b.example.com"}, Csp: []CspEntry{{Directive: "img-src", Sources: []string{"data:"}}}}},
		)
		if got, want := router.Hosts.Values(), []string{"notes.example.com", "b.example.com"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("hosts = %v, want %v", got, want)
		}
		if got, want := router.Framers.Values(), []string{"https://frame.example.com"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("framers = %v, want %v", got, want)
		}
		if len(router.Relays) != 1 || router.Relays[0] != relay {
			t.Fatalf("relays = %v, want [radius]", router.Relays)
		}
		wantCsp := []CspEntry{{Directive: "img-src", Sources: []string{"https://cdn.example.com", "data:"}}}
		if !reflect.DeepEqual(router.Csp, wantCsp) {
			t.Fatalf("csp = %v, want %v", router.Csp, wantCsp)
		}
		match, status := router.Socket("/terminal/7")
		if status != Found {
			t.Fatalf("Socket(/terminal/7) = lookup %v, want a match", status)
		}
		if match.Pattern != "/terminal/:id" || match.Plugin != "@app/a" || !reflect.DeepEqual(match.Params, map[string]string{"id": "7"}) || match.MaxPayload != 10 {
			t.Fatalf("socket = %s", fmt.Sprintf("%+v", *match))
		}
		if _, status := router.Socket("/terminal/.."); status != BadPath {
			t.Fatalf("Socket(/terminal/..) lookup = %v, want bad path", status)
		}
		if _, status := router.Socket("/nope"); status != NoMatch {
			t.Fatalf("Socket(/nope) lookup = %v, want no match", status)
		}
		if _, ok := router.SocketHandlers[terminal]; !ok {
			t.Fatal("socket handlers lack the terminal handler")
		}
	})
}
