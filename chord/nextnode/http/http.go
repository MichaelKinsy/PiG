package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/next-node/http/http.ts. The HTTP component (slice 10 §2; decisions "The HTTP core is a fetch
// handler", "The HTTP server is a native object", "The HTTP component's security baseline", "Settings are arguments,
// extensions are registries"): `CreateHttp` takes the app's settings (where to listen, the links endpoint, the close bound)
// and builds one Web-standard handler, `Fetch`, from the registries its facet serves (`Http`, `Authenticators`), an upgrade
// adapter (`Upgrade`), and the facet, whose host-lifetime listener object is Chord's own server. Every request is admitted
// (provenance), routed, checked for foreign origins, authenticated unless foreign, and served in one `host.Root` scope with
// its principal that lasts until its body is delivered; every answer carries the default headers.
//
// Forced Go differences. A Promise is a blocking call or a done channel; `AbortController` is a context.CancelCauseFunc
// with its context; `Fetch` returns a *net/http.Response; `Upgrade` takes the request in hand (node.go); `HttpComponent<P>`'s
// principal type is data.Principal; an `HttpError` union is one struct tagged by Kind. `close` returns nothing: it cannot
// reject.

// HttpOptions is what `CreateHttp` takes: the app's settings, fixed for the component's life (changing one is a restart).
type HttpOptions struct {
	// Listen is where Chord's own listener listens; nil: no listener (an app mounting `Fetch`). Port 0 picks one.
	Listen *ListenAddress
	// Links is the links endpoint: its path, and each socket's options; nil: none.
	Links *LinksOptions
	// CloseTimeout is how long `Close` waits for running requests and closing sockets: 30 s by default (0).
	CloseTimeout time.Duration
	OnError      func(HttpError)
}

// LinksOptions is the links endpoint's path and what it gives each socket (`maxPayload` 1 MiB by default).
type LinksOptions struct {
	Path string
	// Options is given to every socket unless OptionsFunc is set.
	Options LinkOptions
	// OptionsFunc is per request and principal; an error is reported `hook_failed` and refuses the upgrade (403).
	OptionsFunc func(request HttpRequest, principal data.Principal) (LinkOptions, error)
}

// HttpErrorKind tells what an HttpError reports.
type HttpErrorKind string

const (
	ErrRouteFailed    HttpErrorKind = "route_failed"
	ErrWrapFailed     HttpErrorKind = "wrap_failed"
	ErrSocketFailed   HttpErrorKind = "socket_failed"
	ErrResponseFailed HttpErrorKind = "response_failed"
	ErrHookFailed     HttpErrorKind = "hook_failed"
)

// HookName is a hook the component calls.
type HookName string

const (
	HookAuthenticate HookName = "authenticate"
	HookRelay        HookName = "relay"
	HookLinksOptions HookName = "links.options"
)

// HttpError is what the component reports: route_failed (Pattern, Plugin), wrap_failed (Name, Plugin), socket_failed
// (Pattern, Plugin), response_failed (Pattern: a response that could not be made or read whole) and hook_failed (Hook, and
// the Plugin of a contributed hook; none for the app's `links.options`).
type HttpError struct {
	Kind    HttpErrorKind
	Pattern string
	Plugin  string
	Name    string
	Hook    HookName
	Error   error
}

// HttpComponent is the running component.
type HttpComponent interface {
	// Facet is its facet (plugin `chord.http`): serves `Http` and `Authenticators`, and with Listen the listener, a
	// host-lifetime object. The app loads it among its core facets, the same object at every reload.
	Facet() host.Facet
	// Fetch answers a request (an app's own server mounts it); connection is what the server knows of the socket: without
	// LocalAddress and LocalPort no request is local. Requests before the publication wait for it (503 past the host's
	// `readyTimeout`).
	Fetch(request *nethttp.Request, connection Connection) *nethttp.Response
	// Upgrade is the upgrade adapter: the app's handler calls it for a request asking for an upgrade (the links endpoint
	// and plugin sockets).
	Upgrade(w nethttp.ResponseWriter, request *nethttp.Request)
	// Listening is where the listener listens, once it does (the real port when Listen.Port was 0); an error once the
	// component closed or ctx ended.
	Listening(ctx context.Context) (ListenAddress, error)
	// Close refuses new requests and upgrades (503), closes sockets and links (1001, terminated at CloseTimeout), waits for
	// running requests up to CloseTimeout (an aborted ctx: not at all), then seals them. Run it before `host.Close`, which
	// seals every request's scope at once (the listener's close runs it then, too late to drain).
	Close(ctx context.Context)
}

// CreateHttp builds the HTTP component on h (a request's scope is a `host.Root` with its principal).
func CreateHttp(h host.Host, options HttpOptions, ctx context.Context) HttpComponent {
	return newComponent(h, options, ctx, realAfter)
}

// HttpListener is the listener: a host-lifetime object of plugin `chord.http` whose native is Chord's own server.
var HttpListener = must(contracts.Local("chord.http.listener", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))

// HttpFollower is the follower: a host-lifetime object of plugin `chord.http` that follows the router. It has no Pi
// counterpart as a type: Pi's `#follow` watches the router on the event loop; Go's sources are followed from inside the
// host's sections (an `OnNative` routine).
var HttpFollower = must(contracts.Local("chord.http.follower", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))

// registrySet is the registries a request reads, resolved once.
type registrySet struct {
	router         *registries.RegistryValueSource
	authenticators *registries.RegistryValueSource
}

// running is a request or an upgrade from its arrival to its end: what `close` waits for, then seals.
type running struct {
	cancel context.CancelCauseFunc
	mu     sync.Mutex
	done   <-chan struct{}
	// scope is the request's scope once it has one: `close` waits for its seal to be seen by the route's context (Go links
	// contexts by goroutines where an AbortSignal's abort is synchronous).
	scope host.Ctx
}

func (r *running) doneChan() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

func (r *running) setDone(done <-chan struct{}) {
	r.mu.Lock()
	r.done = done
	r.mu.Unlock()
}

// defaultCloseTimeout is `close`'s bound unless `closeTimeout` says otherwise: 30 s.
const defaultCloseTimeout = 30 * time.Second

// resolution is the registries while they resolve.
type resolution struct {
	done chan struct{}
	set  *registrySet
	err  error
}

// listeningPromise is Pi's `listening` promise.
type listeningPromise struct {
	once sync.Once
	done chan struct{}
	addr ListenAddress
	err  error
}

func (p *listeningPromise) resolve(addr ListenAddress) {
	p.once.Do(func() { p.addr = addr; close(p.done) })
}

func (p *listeningPromise) reject(err error) {
	p.once.Do(func() { p.err = err; close(p.done) })
}

type component struct {
	host    host.Host
	options HttpOptions
	onError func(HttpError)
	ctx     context.Context
	// base is the component's context without its cancellation: each request's scope adds its own signal.
	base     context.Context
	defaults *DefaultHeaders
	nonces   *Nonces
	upgrades *Upgrades
	facet    host.Facet

	mu      sync.Mutex
	running map[*running]struct{}
	// resolving is the registries while they resolve (once; again after a failed try), registries the values themselves.
	resolving  *resolution
	registries *registrySet
	// closing is aborted at `close`: the router's watch.
	closing context.Context
	// latest is the router the follower saw last and revoking whether a task is revoking against it.
	latest        *Router
	revoking      bool
	closingCancel context.CancelFunc
	closeOnce     sync.Once
	listening     *listeningPromise
}

var (
	_ HttpComponent = (*component)(nil)
	_ Handler       = (*component)(nil)
)

func newComponent(h host.Host, options HttpOptions, ctx context.Context, after afterFunc) *component {
	c := &component{
		host: h, options: options, onError: options.OnError, ctx: ctx, base: chordctx.WithoutAbortSignal(ctx),
		defaults: NewDefaultHeaders(), nonces: NewNonces(), upgrades: NewUpgrades(), running: map[*running]struct{}{},
		listening: &listeningPromise{done: make(chan struct{})},
	}
	c.upgrades.after = after
	c.closing, c.closingCancel = context.WithCancel(context.Background())
	listen := options.Listen
	c.facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := facet.Serve(registries.Registry[any, any](Http), registries.RegistryProvider[HttpContribution, *Router]{Compose: ComposeRouter}.Erase(), host.ServeOptions{}); err != nil {
			return err
		}
		if err := facet.Serve(registries.Registry[any, any](Authenticators), registries.RegistryProvider[Authenticator, []AuthenticatorEntry]{Compose: composeAuthenticators}.Erase(), host.ServeOptions{}); err != nil {
			return err
		}
		// The follower: a host-lifetime object following the router (Pi's `#follow`, started at the first resolution).
		if err := facet.Serve(HttpFollower, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			router := self.Registry(registries.Registry[any, any](Http))
			self.OnNative(router, func(call chord.NativeWatchCall[any], _ host.Ctx) *host.Handled {
				if call.Kind == chord.WatchCallValue {
					c.follow(call.Value.(*Router))
				}
				return nil
			})
			return contracts.Implementation{}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		if listen == nil {
			return nil
		}
		return facet.Serve(HttpListener, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			self.Open(
				func(opening context.Context) (any, error) { return c.listen(*listen, opening) },
				func(value any, _ host.NativeCloseReason, closing context.Context) error {
					listener := value.(*NodeListener)
					listener.Stop()
					c.Close(closing)
					listener.Closed()
					return nil
				},
			)
			return contracts.Implementation{}, nil
		}, host.ServeOptions{})
	})
	return c
}

func (c *component) Facet() host.Facet { return c.facet }

// Fetch answers request.
func (c *component) Fetch(request *nethttp.Request, connection Connection) *nethttp.Response {
	served := c.Serve(WebIncoming(request), connection)
	answer := served.Answer
	// A whole body is out of the component's hands once its `Response` is; a pulled one ends when it is read.
	if answer.Stream != nil {
		stream := answer.Stream
		go func() {
			<-stream.Closed()
			served.Finished()
		}()
		return webResponse(answer)
	}
	served.Finished()
	return webResponse(answer)
}

// webResponse is answer as a `Response`.
func webResponse(answer Answer) *nethttp.Response {
	header := nethttp.Header{}
	for _, line := range answer.Headers.Lines() {
		header.Add(line.Name, line.Value)
	}
	response := &nethttp.Response{
		Status: strconv.Itoa(answer.Status) + " " + statusText[answer.Status], StatusCode: answer.Status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: header, ContentLength: -1,
	}
	switch {
	case answer.Stream != nil:
		response.Body = answer.Stream
	case answer.Text != "":
		response.Body = io.NopCloser(strings.NewReader(answer.Text))
		response.ContentLength = int64(len(answer.Text))
	default:
		response.Body = nethttp.NoBody
		response.ContentLength = 0
	}
	return response
}

func (c *component) Listening(ctx context.Context) (ListenAddress, error) {
	select {
	case <-c.listening.done:
		return c.listening.addr, c.listening.err
	case <-ctx.Done():
		return ListenAddress{}, context.Cause(ctx)
	}
}

// Close is `close`.
func (c *component) Close(ctx context.Context) {
	c.closeOnce.Do(func() { c.runClose(ctx) })
}

func (c *component) isClosing() bool { return c.closing.Err() != nil }

// Serve serves request: its answer, and what its consumer calls once the body is written or abandoned. It never fails:
// whatever fails answers (503 before the publication or while closing, 403, 404, 500 reported).
func (c *component) Serve(request Incoming, connection Connection) Served {
	controller, cancel := context.WithCancelCause(context.Background())
	finished := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(finished) }) }
	current := &running{cancel: cancel, done: finished}
	c.mu.Lock()
	c.running[current] = struct{}{}
	c.mu.Unlock()
	request.Cancelled(func(reason error) { cancel(reason) })
	var answer Answer
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				answer = c.refusal(500, nil, nil, false)
			}
		}()
		served := c.serveSteps(request, connection, controller, finished, current)
		answer = served.answer
		// The request is over once its consumer finished and its scope (if it got one) ended.
		if served.scope != nil {
			current.setDone(joined(finished, served.scope))
		}
	}()
	go func() {
		<-current.doneChan()
		c.mu.Lock()
		delete(c.running, current)
		c.mu.Unlock()
		cancel(errors.New("http: the request finished"))
	}()
	return Served{Answer: answer, Finished: finish}
}

// joined is closed once both a and b are.
func joined(a, b <-chan struct{}) <-chan struct{} {
	both := make(chan struct{})
	go func() {
		<-a
		<-b
		close(both)
	}()
	return both
}

// Refusal is the answer for a refused or failed request, outside any scope, with the default headers.
func (c *component) Refusal(status int) Answer { return c.refusal(status, nil, nil, false) }

func (c *component) refusal(status int, admitted *Admitted, router *Router, head bool) Answer {
	return errorAnswer(status, c.defaults.Of(admitted, router, c.nonces.Next()), nil, head)
}

type served struct {
	answer Answer
	scope  <-chan struct{}
}

// serveSteps are the steps of `serve`: closing, the registries, admission, the route, origins, the principal, then the scope.
func (c *component) serveSteps(request Incoming, connection Connection, controller context.Context, finished <-chan struct{}, current *running) served {
	head := request.Method() == "HEAD"
	if c.isClosing() {
		return served{answer: c.refusal(503, nil, nil, head)}
	}
	registrySet, err := c.registriesNow()
	if err != nil || c.isClosing() {
		return served{answer: c.refusal(503, nil, nil, head)}
	}
	router := registrySet.router.Value().(*Router)
	admission := admit(request, connection, router.Hosts, router.Relays)
	if refused, ok := isRefused(admission); ok {
		if refused.Failed != nil {
			c.hookFailed(HookRelay, refused.Failed.Relay, refused.Failed.Error)
		}
		return served{answer: c.refusal(403, nil, router, head)}
	}
	admitted := admission.(*Admitted)
	// A fresh script nonce per response (decision "A CSP nonce per response").
	webRequest := newWebRequest(request, admitted, c.nonces.Next())
	match, status := router.Route(request.Method(), webRequest.Path())
	if status != Found {
		code := 404
		if status == BadPath {
			code = 400
		}
		return served{answer: c.refusal(code, admitted, router, head)}
	}
	webRequest.params = match.Params
	foreign := isForeign(request)
	if !match.CrossSite && !originAccepted(request.Method(), request, admitted.Origin) {
		return served{answer: c.refusal(403, admitted, router, head)}
	}
	var principal data.Principal
	// Foreign requests are never authenticated but a top-level navigation to a page; a cross-site route never is.
	authenticators := registrySet.authenticators.Value().([]AuthenticatorEntry)
	if len(authenticators) > 0 && !match.CrossSite && (!foreign || (match.Page && isNavigation(request.Method(), request))) {
		principal = c.principal(authenticators, webRequest, controller)
		if c.isClosing() || controller.Err() != nil {
			return served{answer: c.refusal(503, admitted, router, head)}
		}
	}
	answered := make(chan Answer, 1)
	scope := make(chan struct{})
	rootCtx := chordctx.WithAbortSignal(c.base, controller)
	go func() {
		defer close(scope)
		_, err := c.host.Root(func(ctx host.Ctx) (any, error) {
			current.mu.Lock()
			current.scope = ctx
			current.mu.Unlock()
			answered <- c.run(request, webRequest, match, admitted, router, foreign, ctx)
			// The scope lasts until the body is written or abandoned (a `bytes` body reads in it).
			<-finished
			return nil, nil
		}, rootCtx, host.RootOptions{Principal: principal, Anonymous: principal == nil})
		if err != nil {
			// The scope was sealed (a disconnect, `close`) before it answered: the request is over.
			select {
			case answered <- c.refusal(503, admitted, router, head):
			default:
			}
		}
	}()
	return served{answer: <-answered, scope: scope}
}

// run runs the wraps and the route in the request's scope ctx, and makes its answer (a failure: a reported 500).
func (c *component) run(incoming Incoming, request *webRequest, match *RouteMatch, admitted *Admitted, router *Router, foreign bool, ctx host.Ctx) Answer {
	response, err := chainOf(match, ctx)(request)
	if err != nil {
		response = c.failure(err, match)
	}
	// Downloads are never another site's to take (rule 6).
	if response.Kind == KindBytes && foreign {
		response = HttpResponse{Kind: KindError, Status: 403}
	}
	failed := func(err error) {
		c.onError(HttpError{Kind: ErrResponseFailed, Pattern: match.Pattern, Error: err})
	}
	answer, err := answerOf(response, incoming, c.defaults.Of(admitted, router, request.Nonce()), ctx, failed)
	if err != nil {
		failed(err)
		return c.refusal(500, admitted, router, request.Method() == "HEAD")
	}
	return answer
}

// failure is what an error escaping the chain answers: its HttpFailure's status, else a reported 500 without its message.
func (c *component) failure(err error, match *RouteMatch) HttpResponse {
	var by *thrown
	if !errors.As(err, &by) {
		by = &thrown{route: true, plugin: match.Plugin, cause: err}
	}
	var failure *HttpFailure
	if errors.As(by.cause, &failure) {
		return HttpResponse{Kind: KindError, Status: failure.Status}
	}
	// Chord refused the caller (an anonymous scope's remote object, a route's `authorize`): no failure of the route.
	if _, ok := data.IsChordErrorCode(by.cause, data.CodeForbidden); ok {
		return HttpResponse{Kind: KindError, Status: 403}
	}
	if by.route {
		c.onError(HttpError{Kind: ErrRouteFailed, Pattern: match.Pattern, Plugin: by.plugin, Error: by.cause})
	} else {
		c.onError(HttpError{Kind: ErrWrapFailed, Name: by.name, Plugin: by.plugin, Error: by.cause})
	}
	return HttpResponse{Kind: KindError, Status: 500}
}

// principal is the principal the authenticators give request; one that throws is reported and skipped. Raced against signal
// (a disconnect, `close`'s seal): an authenticator ignoring it no longer holds the request; the caller sees the abort and
// answers 503.
func (c *component) principal(authenticators []AuthenticatorEntry, request HttpRequest, signal context.Context) data.Principal {
	failed := func(plugin string, err error) { c.hookFailed(HookAuthenticate, plugin, err) }
	ctx := chordctx.WithAbortSignal(c.base, signal)
	result := make(chan data.Principal, 1)
	go func() { result <- authenticate(authenticators, request, failed, ctx) }()
	select {
	case principal := <-result:
		return principal
	case <-ctx.Done():
		return nil
	}
}

func (c *component) hookFailed(hook HookName, plugin string, err error) {
	c.onError(HttpError{Kind: ErrHookFailed, Hook: hook, Plugin: plugin, Error: err})
}

// registriesNow is the registries: resolved once, waiting for the publication; a failed try is tried again later.
func (c *component) registriesNow() (*registrySet, error) {
	c.mu.Lock()
	if c.registries != nil {
		set := c.registries
		c.mu.Unlock()
		return set, nil
	}
	current := c.resolving
	if current == nil {
		current = &resolution{done: make(chan struct{})}
		c.resolving = current
		go c.resolve(current)
	}
	c.mu.Unlock()
	<-current.done
	return current.set, current.err
}

func (c *component) resolve(current *resolution) {
	router, err := c.host.Registry(registries.Registry[any, any](Http), c.ctx, host.GetOptions{})
	var authenticators *registries.RegistryValueSource
	if err == nil {
		authenticators, err = c.host.Registry(registries.Registry[any, any](Authenticators), c.ctx, host.GetOptions{})
	}
	c.mu.Lock()
	if err != nil {
		c.resolving = nil
		current.err = err
	} else {
		current.set = &registrySet{router: router, authenticators: authenticators}
		c.registries = current.set
	}
	c.mu.Unlock()
	close(current.done)
}

// follow is the router followed from the facet's follower object: connections its changes no longer admit are terminated. The
// termination runs off the host's sections and only for the newest router (the connections the older ones admitted that the
// newest does not are terminated by it).
func (c *component) follow(router *Router) {
	c.mu.Lock()
	c.latest = router
	if c.revoking {
		c.mu.Unlock()
		return
	}
	c.revoking = true
	c.mu.Unlock()
	go func() {
		for {
			c.mu.Lock()
			next := c.latest
			c.latest = nil
			if next == nil {
				c.revoking = false
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
			c.upgrades.revoke(next)
		}
	}()
}

// Upgrade is one upgrade, tracked from its arrival: refused while closing, else admitted and checked, then handed out.
func (c *component) Upgrade(w nethttp.ResponseWriter, request *nethttp.Request) {
	controller, cancel := context.WithCancelCause(context.Background())
	// A client gone while its upgrade authenticates aborts that work (a clean disconnect, a reset).
	cancelledBy(request.Context(), func(error) { cancel(errors.New("http: the client disconnected")) })
	done := make(chan struct{})
	current := &running{cancel: cancel, done: done}
	c.mu.Lock()
	c.running[current] = struct{}{}
	c.mu.Unlock()
	defer func() {
		close(done)
		c.mu.Lock()
		delete(c.running, current)
		c.mu.Unlock()
		cancel(errors.New("http: the upgrade finished"))
	}()
	c.upgradeSteps(w, request, controller)
}

// upgradeSteps are the steps of an upgrade (the same rules as a request's; a socket needs a principal), checked again at
// hand-out.
func (c *component) upgradeSteps(w nethttp.ResponseWriter, r *nethttp.Request, signal context.Context) {
	if c.isClosing() {
		refuse(w, 503)
		return
	}
	request, refusal := nodeIncomingOf(r)
	if refusal != nil {
		refuse(w, refusal.Status)
		return
	}
	registrySet, err := c.registriesNow()
	if err != nil {
		refuse(w, 503)
		return
	}
	if c.isClosing() || signal.Err() != nil {
		refuse(w, 503)
		return
	}
	before := registrySet.router.Value().(*Router)
	admission := admit(request, connectionOf(r), before.Hosts, before.Relays)
	if refused, ok := isRefused(admission); ok {
		if refused.Failed != nil {
			c.hookFailed(HookRelay, refused.Failed.Relay, refused.Failed.Error)
		}
		refuse(w, 403)
		return
	}
	admitted := admission.(*Admitted)
	sent, hasOrigin := request.Header("origin")
	if isForeign(request) || (hasOrigin && sent != admitted.Origin) {
		refuse(w, 403)
		return
	}
	webRequest := newWebRequest(request, admitted, c.nonces.Next())
	principal := c.principal(registrySet.authenticators.Value().([]AuthenticatorEntry), webRequest, signal)
	if c.isClosing() || signal.Err() != nil {
		refuse(w, 503)
		return
	}
	if principal == nil {
		refuse(w, 403)
		return
	}
	// The router now: a host, a relay or a socket removed while authenticating refuses the upgrade.
	router := registrySet.router.Value().(*Router)
	if (admitted.Allowed != "" && !router.Hosts.Has(admitted.Allowed)) || (admitted.Relay != nil && !containsRelay(router.Relays, admitted.Relay)) {
		refuse(w, 403)
		return
	}
	admission2 := UpgradeAdmission{Allowed: admitted.Allowed, Relay: admitted.Relay, Principal: principal}
	if links := c.options.Links; links != nil && webRequest.Path() == links.Path {
		options := links.Options
		if links.OptionsFunc != nil {
			var err error
			options, err = tryLinkOptions(links.OptionsFunc, webRequest, principal)
			if err != nil {
				c.hookFailed(HookLinksOptions, "", err)
				refuse(w, 403)
				return
			}
		}
		c.upgrades.Link(w, r, admission2, options, c.host, c.ctx)
		return
	}
	match, status := router.Socket(webRequest.Path())
	switch status {
	case BadPath:
		refuse(w, 400)
		return
	case NoMatch:
		refuse(w, 404)
		return
	}
	webRequest.params = match.Params
	failed := func(failure SocketFailure) {
		c.onError(HttpError{Kind: ErrSocketFailed, Pattern: failure.Pattern, Plugin: failure.Plugin, Error: failure.Error})
	}
	c.upgrades.Socket(w, r, admission2, match, webRequest, c.host, c.ctx, failed)
}

func containsRelay(relays []*HttpRelay, relay *HttpRelay) bool {
	for _, each := range relays {
		if each == relay {
			return true
		}
	}
	return false
}

func tryLinkOptions(options func(HttpRequest, data.Principal) (LinkOptions, error), request HttpRequest, principal data.Principal) (result LinkOptions, err error) {
	defer recoverAs(&err)
	return options(request, principal)
}

// listen is the listener's open: Chord's own server listening at listen.
func (c *component) listen(listen ListenAddress, ctx context.Context) (*NodeListener, error) {
	listener, err := ListenNode(c, listen, ctx)
	if err != nil {
		return nil, err
	}
	c.listening.resolve(listener.Address)
	return listener, nil
}

// runClose is `close`: refuses from now on, then waits for sockets and running requests until the deadline, then seals.
func (c *component) runClose(ctx context.Context) {
	c.closingCancel()
	c.listening.reject(errors.New("http: closed"))
	timeout := c.options.CloseTimeout
	if timeout == 0 {
		timeout = defaultCloseTimeout
	}
	deadline := make(chan struct{})
	var deadlineOnce sync.Once
	reach := func() { deadlineOnce.Do(func() { close(deadline) }) }
	finished := make(chan struct{})
	defer close(finished)
	if ctx.Err() != nil {
		// Already aborted: no wait at all.
		reach()
	} else {
		timer := time.AfterFunc(timeout, reach)
		defer timer.Stop()
		go func() {
			select {
			case <-ctx.Done():
				reach()
			case <-finished:
			}
		}()
	}
	c.mu.Lock()
	var dones []<-chan struct{}
	for each := range c.running {
		dones = append(dones, each.doneChan())
	}
	c.mu.Unlock()
	allRunning := make(chan struct{})
	go func() {
		for _, done := range dones {
			<-done
		}
		close(allRunning)
	}()
	upgraded := make(chan struct{})
	go func() {
		c.upgrades.Close(deadline)
		close(upgraded)
	}()
	both := joined(upgraded, allRunning)
	select {
	case <-both:
	case <-deadline:
	}
	c.mu.Lock()
	sealing := make([]*running, 0, len(c.running))
	for each := range c.running {
		each.cancel(errors.New("http: closed"))
		sealing = append(sealing, each)
	}
	c.mu.Unlock()
	// Sealed, as `abort()` is for the routes' signals once it returns.
	for _, each := range sealing {
		each.mu.Lock()
		scope := each.scope
		each.mu.Unlock()
		if scope != nil {
			<-scope.Done()
		}
	}
}

// refuse refuses an upgrade with status and closes its connection.
func refuse(w nethttp.ResponseWriter, status int) {
	header := w.Header()
	header.Set("Connection", "close")
	header.Set("Content-Length", "0")
	w.WriteHeader(status)
}

// thrown is who threw out of a request's chain: the route, or a wrap (by name), with its plugin.
type thrown struct {
	route  bool
	name   string
	plugin string
	cause  error
}

func (t *thrown) Error() string { return fmt.Sprint("thrown: ", t.cause) }
func (t *thrown) Unwrap() error { return t.cause }

// chainOf is the route of match inside its wraps (outermost first), each throw tagged with who threw it.
func chainOf(match *RouteMatch, ctx host.Ctx) func(HttpRequest) (HttpResponse, error) {
	next := tagged(match.Handler, match.Plugin, ctx)
	for index := len(match.Wraps) - 1; index >= 0; index-- {
		wrap, plugin := match.Wraps[index].Wrap, match.Wraps[index].Plugin
		inner := next
		next = func(request HttpRequest) (response HttpResponse, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = &thrown{name: wrap.Name, plugin: plugin, cause: panicError(recovered)}
				}
			}()
			response, err = wrap.Wrap(request, inner, ctx)
			if err != nil {
				var already *thrown
				if !errors.As(err, &already) {
					err = &thrown{name: wrap.Name, plugin: plugin, cause: err}
				}
			}
			return response, err
		}
	}
	return next
}

// tagged is route with its throws tagged as the route's.
func tagged(route HttpRoute, plugin string, ctx host.Ctx) func(HttpRequest) (HttpResponse, error) {
	return func(request HttpRequest) (response HttpResponse, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = &thrown{route: true, plugin: plugin, cause: panicError(recovered)}
			}
		}()
		response, err = route(request, ctx)
		if err != nil {
			err = &thrown{route: true, plugin: plugin, cause: err}
		}
		return response, err
	}
}

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}
