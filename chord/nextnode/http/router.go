package http

import (
	"fmt"
	nethttp "net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/next-node/http/router.ts. The router (slice 10 §2, D1, D2, D8): the registry `Http` that plugins
// contribute routes, wraps, sockets, allowed hosts, framers, relays and CSP sources to, and its `compose`, which builds the
// router every request reads: patterns matched segment by segment (exact before `:name` before a trailing `*`), each
// route's wraps resolved in phase and contributor order, conflicts reported with the first by plugin name winning.
//
// Forced Go differences.
//   - A `Record<string, ...>` is an ordered slice of entries (HttpRouteEntry, HttpSocketEntry, CspEntry): its keys' order is
//     the conflict-report order. `HttpRoute | HttpRouteOptions` is HttpRouteOptions (RouteFunc builds one from a handler).
//   - A route, a wrap hook and a relay's `verify` throw: they return an error (a panic is recovered as one).
//   - Functions have no identity in Go: a socket handler is an *HttpSocket and a relay an *HttpRelay, compared by pointer
//     (Pi's `Set<HttpSocket>` and `relays.includes`).
//   - `route` and `socket` answer a Lookup alongside the match: Pi's `"bad_path" | undefined`.
//   - `WebSocketHandle` is Pi's `ws` WebSocket as websocket.go gives it.
//   - A Web `Request` given to a relay's `verify` is a *net/http.Request.

// HttpRoute answers a request with a response description, in the request's scope (`ctx.Principal()`, its Done).
type HttpRoute func(request HttpRequest, ctx host.Ctx) (HttpResponse, error)

// HttpRouteOptions is a route with its options: Page admits a top-level navigation from another site (a link to it) with
// its authentication; CrossSite admits any foreign request (a webhook), always anonymous.
type HttpRouteOptions struct {
	Handler   HttpRoute
	Page      bool
	CrossSite bool
}

// HttpRouteEntry is one route of a contribution by its `"METHOD /pattern"`.
type HttpRouteEntry struct {
	Key   string
	Route HttpRouteOptions
}

// RouteFunc is a route of a contribution without options.
func RouteFunc(key string, handler HttpRoute) HttpRouteEntry {
	return HttpRouteEntry{Key: key, Route: HttpRouteOptions{Handler: handler}}
}

// WrapPhase is a wrap's phase, outermost first.
type WrapPhase string

const (
	PhaseObserve   WrapPhase = "observe"
	PhaseHeaders   WrapPhase = "headers"
	PhaseTransform WrapPhase = "transform"
)

// Scopes of a wrap.
const (
	ScopeOwn = "own"
	ScopeAll = "all"
)

// HttpWrap is a wrap (D2): runs around routes in its phase, around its own plugin's routes unless Scope is "all".
type HttpWrap struct {
	Name  string
	Phase WrapPhase
	// Scope is "own" (default, also ""): around the contributing plugin's routes; "all": every route.
	Scope string
	// Route is only around this route (`"GET /notes/:id"`), "" for none; a route nobody contributes is a reported conflict.
	Route string
	Wrap  func(request HttpRequest, next func(HttpRequest) (HttpResponse, error), ctx host.Ctx) (HttpResponse, error)
}

// HttpSocket is a plugin socket's handler (D6): a WebSocket after the checks and authentication, its `error` listener
// attached; its scope lasts until the socket closes. Message reading begins when Handle returns.
type HttpSocket struct {
	Handle func(socket *WebSocketHandle, request HttpRequest, ctx host.Ctx) error
}

// HttpSocketEntry is a plugin socket by pattern (no method).
type HttpSocketEntry struct {
	Pattern string
	Handler *HttpSocket
}

// HttpRelay is a relay (p2's Radius): a forwarder on loopback that sends forwarded headers. Verify recognizes its requests
// (a secret header, a signature) and names the origin the request came from and the origin allowed to frame it.
type HttpRelay struct {
	Name   string
	Verify func(request *nethttp.Request) (*RelayVerified, error)
}

// RelayVerified is what a relay says of a request it recognizes; Framer nil for none.
type RelayVerified struct {
	Origin string
	Framer *string
}

// CspEntry is extra CSP sources of one directive (`img-src` → `https://cdn.example.com`).
type CspEntry struct {
	Directive string
	Sources   []string
}

// HttpContribution is what a plugin contributes to `Http`: a plain value, or a writable of one (its change recomposes the
// router).
type HttpContribution struct {
	// Routes by `"METHOD /pattern"`: exact segments, `:name`, a trailing `/*`.
	Routes []HttpRouteEntry
	Wraps  []HttpWrap
	// Sockets are plugin sockets by pattern (no method).
	Sockets []HttpSocketEntry
	// MaxPayload is the largest frame this contribution's sockets accept; 1 MiB by default (0).
	MaxPayload int
	// Hosts are allowed besides loopback, as the client names them (`notes.example.com`): requests through them are https.
	Hosts []string
	// Framers are origins allowed to frame the app's pages (CSP `frame-ancestors`).
	Framers []string
	// Relays are relays whose forwarded requests are admitted (verified) on loopback.
	Relays []*HttpRelay
	// Csp is extra CSP sources by directive.
	Csp []CspEntry
}

// Lookup is what a route or socket lookup found.
type Lookup int

const (
	// Found: a match.
	Found Lookup = iota
	// NoMatch: no route or socket (Pi's `undefined`).
	NoMatch
	// BadPath: a malformed path (Pi's `"bad_path"`).
	BadPath
)

// Router is the router a request uses: as it was when the request started.
type Router struct {
	routes  map[string]*node[routeEntry]
	sockets *node[socketEntry]
	// Hosts are the hosts contributions allow (lowercased).
	Hosts *StringSet
	// Framers are the origins allowed to frame pages.
	Framers *StringSet
	// Relays are the relays, in contributor order.
	Relays []*HttpRelay
	// Csp is the extra CSP sources by directive.
	Csp []CspEntry
	// SocketHandlers are the socket handlers it serves: a socket handed out by one no longer here is closed.
	SocketHandlers map[*HttpSocket]struct{}
}

// RouteMatch is a matched route: its pattern, plugin, params, handler, options and wraps (outermost first).
type RouteMatch struct {
	Pattern   string
	Plugin    string
	Params    map[string]string
	Handler   HttpRoute
	Page      bool
	CrossSite bool
	Wraps     []ResolvedWrap
}

// SocketMatch is a matched plugin socket.
type SocketMatch struct {
	Pattern    string
	Plugin     string
	Params     map[string]string
	Handler    *HttpSocket
	MaxPayload int
}

// ResolvedWrap is a wrap as a route runs it, with its plugin.
type ResolvedWrap struct {
	Wrap   *HttpWrap
	Plugin string
}

// Http is the registry the HTTP component defines and serves (`http.facet`, plugin `chord.http`).
var Http = must(registries.DefineRegistry[HttpContribution, *Router]("chord.http", registries.DefineRegistryOptions{Version: 1}))

// defaultMaxPayload is a plugin socket's largest frame unless its contribution says otherwise: 1 MiB.
const defaultMaxPayload = 1024 * 1024

// UNCARRIED are the methods a Web `Request` cannot carry (the Fetch standard's forbidden methods): answered 501 at the
// wire, a route for one refused at compose.
var UNCARRIED = map[string]bool{"CONNECT": true, "TRACE": true, "TRACK": true}

// ComposeRouter builds the router from table (the registry's `compose`): pure, conflicts reported, the first by plugin name
// wins.
func ComposeRouter(table registries.RegistryTable[HttpContribution], ctx registries.ComposeContext) (*Router, error) {
	report := ctx.Report
	routes := map[string]*node[routeEntry]{}
	sockets := newNode[socketEntry]()
	var entries []*routeEntry
	router := &Router{
		routes: routes, sockets: sockets, Hosts: NewStringSet(), Framers: NewStringSet(),
		SocketHandlers: map[*HttpSocket]struct{}{},
	}
	for _, each := range table {
		plugin, value := each.Plugin, each.Value
		for _, host := range value.Hosts {
			router.Hosts.Add(strings.ToLower(host))
		}
		for _, framer := range value.Framers {
			router.Framers.Add(framer)
		}
		router.Relays = append(router.Relays, value.Relays...)
		for _, extra := range value.Csp {
			router.Csp = addCsp(router.Csp, extra)
		}
		for _, route := range value.Routes {
			key := route.Key
			method, segments, ok := parseRoute(key)
			if !ok {
				report(registries.RegistryConflict{Message: fmt.Sprintf("route %s is no \"METHOD /pattern\"", jsonString(key)), Plugins: []string{plugin}})
				continue
			}
			if UNCARRIED[method] {
				report(registries.RegistryConflict{Message: fmt.Sprintf("route %s: %s is served for no resource (501)", jsonString(key), method), Plugins: []string{plugin}})
				continue
			}
			tree := routes[method]
			if tree == nil {
				tree = newNode[routeEntry]()
				routes[method] = tree
			}
			entry := &routeEntry{
				pattern: key, plugin: plugin, handler: route.Route.Handler, page: route.Route.Page,
				crossSite: route.Route.CrossSite, segments: segments,
			}
			if taken := insert(tree, segments, entry); taken == nil {
				entries = append(entries, entry)
			} else {
				report(registries.RegistryConflict{Message: fmt.Sprintf("route %s is contributed twice", jsonString(key)), Plugins: []string{taken.plugin, plugin}})
			}
		}
		maxPayload := value.MaxPayload
		if maxPayload == 0 {
			maxPayload = defaultMaxPayload
		}
		for _, socket := range value.Sockets {
			segments, ok := parsePattern(socket.Pattern)
			if !ok {
				report(registries.RegistryConflict{Message: fmt.Sprintf("socket %s is no pattern", jsonString(socket.Pattern)), Plugins: []string{plugin}})
				continue
			}
			entry := &socketEntry{pattern: socket.Pattern, plugin: plugin, handler: socket.Handler, segments: segments, maxPayload: maxPayload}
			if taken := insert(sockets, segments, entry); taken == nil {
				router.SocketHandlers[socket.Handler] = struct{}{}
			} else {
				report(registries.RegistryConflict{Message: fmt.Sprintf("socket %s is contributed twice", jsonString(socket.Pattern)), Plugins: []string{taken.plugin, plugin}})
			}
		}
	}
	resolveWraps(table, entries, report)
	return router, nil
}

func addCsp(list []CspEntry, extra CspEntry) []CspEntry {
	for i := range list {
		if list[i].Directive == extra.Directive {
			list[i].Sources = append(append([]string(nil), list[i].Sources...), extra.Sources...)
			return list
		}
	}
	return append(list, CspEntry{Directive: extra.Directive, Sources: append([]string(nil), extra.Sources...)})
}

// Route is the route for method and path, with its params and wraps; BadPath for a malformed one.
func (r *Router) Route(method, path string) (*RouteMatch, Lookup) {
	// `HEAD` falls back to `GET` per path: an explicit `HEAD` route elsewhere hides no `GET` route.
	if method == "HEAD" {
		var found *RouteMatch
		status := NoMatch
		if head := r.routes["HEAD"]; head != nil {
			found, status = lookupRoute(head, path)
		}
		if status != NoMatch || r.routes["GET"] == nil {
			return found, status
		}
		return lookupRoute(r.routes["GET"], path)
	}
	tree := r.routes[method]
	if tree == nil {
		return nil, NoMatch
	}
	return lookupRoute(tree, path)
}

// Socket is the plugin socket for path.
func (r *Router) Socket(path string) (*SocketMatch, Lookup) {
	entry, params, status := lookup(r.sockets, path)
	if status != Found {
		return nil, status
	}
	return &SocketMatch{Pattern: entry.pattern, Plugin: entry.plugin, Params: params, Handler: entry.handler, MaxPayload: entry.maxPayload}, Found
}

func lookupRoute(tree *node[routeEntry], path string) (*RouteMatch, Lookup) {
	entry, params, status := lookup(tree, path)
	if status != Found {
		return nil, status
	}
	return &RouteMatch{
		Pattern: entry.pattern, Plugin: entry.plugin, Params: params, Handler: entry.handler, Page: entry.page,
		CrossSite: entry.crossSite, Wraps: entry.wraps,
	}, Found
}

type segmentKind int

const (
	segExact segmentKind = iota
	segParam
	segRest
)

// segment is one pattern segment: an exact one, `:name`, or the trailing rest (`*`).
type segment struct {
	kind segmentKind
	text string
}

// routeEntry is what a route contributes, with its wraps once resolved.
type routeEntry struct {
	pattern   string
	plugin    string
	handler   HttpRoute
	page      bool
	crossSite bool
	segments  []segment
	wraps     []ResolvedWrap
}

func (e *routeEntry) segs() []segment { return e.segments }

// socketEntry is what a plugin socket contributes.
type socketEntry struct {
	pattern    string
	plugin     string
	handler    *HttpSocket
	segments   []segment
	maxPayload int
}

func (e *socketEntry) segs() []segment { return e.segments }

type entryOf interface{ segs() []segment }

// node is a node of a pattern tree: its children by kind, and the entry ending here.
type node[E any] struct {
	exact map[string]*node[E]
	// param is any `:name` at this position (the names are each entry's own).
	param *node[E]
	rest  *E
	entry *E
}

func newNode[E any]() *node[E] { return &node[E]{exact: map[string]*node[E]{}} }

var methodPattern = regexp.MustCompile(`^[A-Z]+$`)

// parseRoute is `"GET /notes/:id"` as its method and segments.
func parseRoute(key string) (string, []segment, bool) {
	space := strings.IndexByte(key, ' ')
	if space <= 0 {
		return "", nil, false
	}
	method := key[:space]
	if !methodPattern.MatchString(method) {
		return "", nil, false
	}
	segments, ok := parsePattern(key[space+1:])
	if !ok {
		return "", nil, false
	}
	return method, segments, true
}

// parsePattern is a pattern's segments (`/files/*`, `/notes/:id`); false for one that is not a path or has `*` before its end.
func parsePattern(pattern string) ([]segment, bool) {
	if !strings.HasPrefix(pattern, "/") {
		return nil, false
	}
	parts := strings.Split(pattern[1:], "/")
	segments := make([]segment, 0, len(parts))
	for index, part := range parts {
		switch {
		case part == "*":
			if index != len(parts)-1 {
				return nil, false
			}
			segments = append(segments, segment{kind: segRest})
		case strings.HasPrefix(part, ":"):
			if len(part) == 1 {
				return nil, false
			}
			segments = append(segments, segment{kind: segParam, text: part[1:]})
		default:
			segments = append(segments, segment{kind: segExact, text: part})
		}
	}
	return segments, true
}

// insert puts entry at segments in tree; it returns the entry already there (the first by plugin name keeps it).
func insert[E any](tree *node[E], segments []segment, entry *E) *E {
	current := tree
	for _, each := range segments {
		switch each.kind {
		case segRest:
			if current.rest != nil {
				return current.rest
			}
			current.rest = entry
			return nil
		case segExact:
			next := current.exact[each.text]
			if next == nil {
				next = newNode[E]()
				current.exact[each.text] = next
			}
			current = next
		default:
			// `:a` and `:b` at one position share the node: one shape, so the same pattern twice is a conflict.
			if current.param == nil {
				current.param = newNode[E]()
			}
			current = current.param
		}
	}
	if current.entry != nil {
		return current.entry
	}
	current.entry = entry
	return nil
}

// lookup is the entry path matches in tree with its params named by the entry's own pattern, the most specific segment by
// segment from the left (exact before `:name` before `*`); BadPath for a segment that does not decode, or a `:name` or rest
// segment decoding to one containing `/`, `\` or NUL, or to `.` or `..` (a param must never be a path).
func lookup[E any, P interface {
	*E
	entryOf
}](tree *node[E], path string) (*E, map[string]string, Lookup) {
	raw := strings.Split(path[1:], "/")
	decoded := make([]string, 0, len(raw))
	for _, part := range raw {
		if !strings.Contains(part, "%") {
			decoded = append(decoded, part)
			continue
		}
		text, err := url.PathUnescape(part)
		if err != nil || !utf8.ValidString(text) {
			return nil, nil, BadPath
		}
		decoded = append(decoded, text)
	}
	found := walk(tree, decoded, 0, nil)
	if found == nil {
		return nil, nil, NoMatch
	}
	params := map[string]string{}
	capture := 0
	for _, each := range P(found.entry).segs() {
		if each.kind != segParam {
			continue
		}
		value := found.captures[capture]
		capture++
		if !safe(value) {
			return nil, nil, BadPath
		}
		params[each.text] = value
	}
	if found.rest >= 0 {
		rest := decoded[found.rest:]
		for _, part := range rest {
			if !safe(part) {
				return nil, nil, BadPath
			}
		}
		params["*"] = strings.Join(rest, "/")
	}
	return found.entry, params, Found
}

// safe is whether a decoded segment may be a param: no `/`, `\` or NUL, not `.` or `..`.
func safe(segment string) bool {
	return segment != "." && segment != ".." && !strings.ContainsAny(segment, "/\\\x00")
}

// found is a match found by walk: its entry, its `:name` values in order, where the rest starts (for `*`), -1 for none.
type found[E any] struct {
	entry    *E
	captures []string
	rest     int
}

// walk goes depth first in precedence order: the first match found is the most specific from the left.
func walk[E any](current *node[E], parts []string, index int, captures []string) *found[E] {
	if index == len(parts) {
		if current.entry != nil {
			return &found[E]{entry: current.entry, captures: captures, rest: -1}
		}
		// `/files/*` matches `/files`: the rest is empty.
		if current.rest == nil {
			return nil
		}
		return &found[E]{entry: current.rest, captures: captures, rest: index}
	}
	part := parts[index]
	if exact := current.exact[part]; exact != nil {
		if via := walk(exact, parts, index+1, captures); via != nil {
			return via
		}
	}
	if current.param != nil && part != "" {
		next := append(append([]string(nil), captures...), part)
		if via := walk(current.param, parts, index+1, next); via != nil {
			return via
		}
	}
	if current.rest == nil {
		return nil
	}
	return &found[E]{entry: current.rest, captures: captures, rest: index}
}

var phases = []WrapPhase{PhaseObserve, PhaseHeaders, PhaseTransform}

func phaseIndex(phase WrapPhase) int {
	for i, each := range phases {
		if each == phase {
			return i
		}
	}
	return -1
}

// resolveWraps gives every route its wraps: by phase (outermost first), within a phase in contributor (table) order; `own`
// ones around their plugin's routes, `all` ones around every route; one naming a route nobody contributes is reported.
func resolveWraps(table registries.RegistryTable[HttpContribution], routes []*routeEntry, report func(registries.RegistryConflict)) {
	var all []ResolvedWrap
	for _, each := range table {
		for i := range each.Value.Wraps {
			all = append(all, ResolvedWrap{Wrap: &each.Value.Wraps[i], Plugin: each.Plugin})
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return phaseIndex(all[a].Wrap.Phase) < phaseIndex(all[b].Wrap.Phase) })
	for _, each := range all {
		wrap := each.Wrap
		if wrap.Route == "" {
			continue
		}
		contributed := false
		for _, route := range routes {
			if route.pattern == wrap.Route {
				contributed = true
				break
			}
		}
		if !contributed {
			report(registries.RegistryConflict{
				Message: fmt.Sprintf("wrap %s names route %s, which nobody contributes", jsonString(wrap.Name), jsonString(wrap.Route)),
				Plugins: []string{each.Plugin},
			})
		}
	}
	for _, route := range routes {
		route.wraps = nil
		for _, each := range all {
			wrap := each.Wrap
			if (wrap.Scope == ScopeAll || each.Plugin == route.plugin) && (wrap.Route == "" || wrap.Route == route.pattern) {
				route.wraps = append(route.wraps, each)
			}
		}
	}
}
