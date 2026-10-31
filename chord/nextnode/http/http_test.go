package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Tests of the HTTP component (slice 10 §2; decisions "The HTTP core is a fetch handler", "The HTTP server is a native
// object", "The HTTP component's security baseline"): `fetch` waiting for the publication, the registries resolved
// once, the listener opening after the publication (none without `listen`, a later change reported), allowed hosts from
// contributions, authenticators, foreign requests and `Origin`, pages and cross-site routes, the default headers on the
// component's own answers, throwing routes and wraps, `close` (also while authenticating), a mounted handler, and
// requests' scopes collected. Ports packages/chord/test/next-node/http/http.test.ts.
//
// Not ported: "a reload swapping a route leaves a running request on the old router; the next uses the new one" needs
// host.Reload, which the Go host does not have yet.
//
// Forced differences. A route that never answers blocks on its scope's Done and returns a 503 description. The GC
// tests take a `weak.Pointer` of the scope and of the request through reflect (`host.Ctx` and `HttpRequest` are
// interfaces over pointers the tests cannot name), and run `runtime.GC` in a bounded loop. An `AbortSignal` of a request
// is the request's context.

// httpWhoami is a route answering who it runs as.
var httpWhoami = contributing(HttpContribution{Routes: []HttpRouteEntry{
	RouteFunc("GET /whoami", httpWhoamiRoute),
}})

func httpWhoamiRoute(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
	if principal := ctx.Principal(); principal != nil {
		return jsonOf(principal), nil
	}
	return jsonOf(nil), nil
}

// httpKindOf is the kind of a principal, "anonymous" for none.
func httpKindOf(principal data.Principal) string {
	if principal == nil {
		return "anonymous"
	}
	kind, _ := principal.Value("kind").(string)
	return kind
}

// httpAsync runs f on a goroutine of its own; the result comes on the channel.
func httpAsync[T any](f func() T) <-chan T {
	result := make(chan T, 1)
	go func() { result <- f() }()
	return result
}

// httpMerge is the headers of all maps, the later winning.
func httpMerge(maps ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, each := range maps {
		for name, value := range each {
			merged[name] = value
		}
	}
	return merged
}

// httpWithContext is a loopback mutation making the request run in ctx (`signal`).
func httpWithContext(ctx context.Context) func(*nethttp.Request) {
	return func(request *nethttp.Request) { *request = *request.WithContext(ctx) }
}

// httpAbortable is a context with its abort (`new AbortController()`).
func httpAbortable() (context.Context, context.CancelCauseFunc) {
	return context.WithCancelCause(context.Background())
}

// httpAborted is `AbortSignal.abort()`.
func httpAborted() context.Context {
	ctx, abort := httpAbortable()
	abort(errors.New("aborted"))
	return ctx
}

// httpFetch is fetch of a loopback request, as a mounted server makes it.
func httpFetch(server *testServer, path string, mutate ...func(*nethttp.Request)) *nethttp.Response {
	return server.http.Fetch(loopback(path, mutate...), loopbackConnection)
}

// httpFailingBody is a body of "{}" that then errors.
type httpFailingBody struct{ pulls int }

func (b *httpFailingBody) Read(p []byte) (int, error) {
	b.pulls++
	if b.pulls == 1 {
		return copy(p, "{}"), nil
	}
	return 0, errors.New("incoming body checksum failed")
}

// httpWeak is a weak pointer to the object a pointer in value points to.
func httpWeak(value any) weak.Pointer[byte] {
	return weak.Make((*byte)(reflect.ValueOf(value).UnsafePointer()))
}

// httpExpectCollected waits (up to 100 collections) until every object of refs is collected.
func httpExpectCollected(t *testing.T, refs []weak.Pointer[byte]) {
	t.Helper()
	remaining := len(refs)
	for range 100 {
		runtime.GC()
		remaining = 0
		for _, ref := range refs {
			if ref.Value() != nil {
				remaining++
			}
		}
		if remaining == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d of %d objects were not collected", remaining, len(refs))
}

func TestStartupFetchWaitsForThePublicationThenServesTheRegistriesAreResolvedOnce(t *testing.T) {
	server := serving(t, []plugin{{"@app/who", httpWhoami}}, servedOptions{noLoad: true})
	early := httpAsync(func() *nethttp.Response { return httpFetch(server, "/whoami") })
	time.Sleep(50 * time.Millisecond)
	server.load()
	if got := fetchText(t, receive(t, early, "the early answer")); got != "null" {
		t.Fatalf("early answer = %q, want null", got)
	}
	calls := server.registryCalls.count()
	for range 3 {
		httpFetch(server, "/whoami").Body.Close()
	}
	if got := server.registryCalls.count(); got != calls {
		t.Fatalf("registry calls = %d, want %d", got, calls)
	}
	server.close()
}

func TestStartupAnswers503ToARequestWaitingPastTheHostsReadyTimeout(t *testing.T) {
	server := serving(t, nil, servedOptions{noLoad: true, readyTimeout: 50 * time.Millisecond})
	if got := httpFetch(server, "/x").StatusCode; got != 503 {
		t.Fatalf("status = %d, want 503", got)
	}
	server.load()
	if got := httpFetch(server, "/x").StatusCode; got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
	server.close()
}

func TestStartupListensOncePublishedWhereItsListenSettingSaysWithoutItNoListener(t *testing.T) {
	server := serving(t, []plugin{{"@app/who", httpWhoami}}, servedOptions{})
	if server.port <= 0 {
		t.Fatalf("port = %d", server.port)
	}
	if got := mustSend(t, server.port, "GET", "/whoami", sendOptions{}).status; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	server.close()
	mounted := serving(t, []plugin{{"@app/who", httpWhoami}}, servedOptions{noListen: true})
	listening := httpAsync(func() string {
		if _, err := mounted.http.Listening(background); err != nil {
			return "closed"
		}
		return "listening"
	})
	if got := httpFetch(mounted, "/whoami").StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	mounted.close()
	if got := receive(t, listening, "listening"); got != "closed" {
		t.Fatalf("listening = %q, want closed", got)
	}
}

func TestHostAndPrincipalsRefusesAHostNeitherLoopbackNorContributedAdmitsAContributedOne(t *testing.T) {
	server := serving(t, []plugin{
		{"@app/who", httpWhoami},
		{"@app/hosts", contributing(HttpContribution{Hosts: []string{"Notes.Example.com"}})},
	}, servedOptions{})
	refused := mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: map[string]string{"host": "evil.com"}})
	if refused.status != 403 || refused.header("x-content-type-options") != "nosniff" || refused.header("cache-control") != "no-store" {
		t.Fatalf("refused = %d %q %q", refused.status, refused.header("x-content-type-options"), refused.header("cache-control"))
	}
	through := mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: map[string]string{"host": "notes.example.com"}})
	if through.status != 200 {
		t.Fatalf("status = %d, want 200", through.status)
	}
	server.close()
}

func TestHostAndPrincipalsAdmitsHostsAWritableContributionAddsAndRemovesAtRuntime(t *testing.T) {
	tunnel, err := contracts.Local("app.tunnel", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("name", contracts.NewMethod(contracts.Arr(contracts.Str()), contracts.Nul()))},
	})
	if err != nil {
		t.Fatal(err)
	}
	facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(tunnel, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			contribution := self.Writable(HttpContribution{Hosts: []string{}})
			self.(host.StoreSelf).Contribute(registries.Registry[any, any](Http), contribution)
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"name": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					hosts := []string{}
					for _, each := range args.([]any) {
						hosts = append(hosts, each.(string))
					}
					return nil, contribution.Set(HttpContribution{Hosts: hosts})
				},
			}}}, nil
		}, host.ServeOptions{})
	})
	server := serving(t, []plugin{{"@app/tunnel", facet}, {"@app/who", httpWhoami}}, servedOptions{})
	status := func() int {
		return mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: map[string]string{"host": "t.example.com"}}).status
	}
	if got := status(); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	handle, err := server.host.Get(tunnel, delta.JsonObjectOf(), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Call("name", []any{"t.example.com"}, background); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if _, err := handle.Call("name", []any{}, background); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	server.close()
}

func TestHostAndPrincipalsRunsARequestWithoutAPrincipalAnonymousAsksTheAuthenticatorsInOrderSkippingAThrowingOne(t *testing.T) {
	server := serving(t, []plugin{
		{"@app/who", httpWhoami},
		{"@app/a-broken", authenticating(func(HttpRequest, context.Context) (data.Principal, error) {
			return nil, errors.New("broken")
		})},
	}, servedOptions{})
	if got := mustSend(t, server.port, "GET", "/whoami", sendOptions{}).text(); got != "null" {
		t.Fatalf("anonymous = %q, want null", got)
	}
	owner := mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: map[string]string{"x-principal": "owner"}})
	if got := owner.text(); got != `{"kind":"owner"}` {
		t.Fatalf("owner = %q", got)
	}
	errs := server.errors.all()
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want 2", errs)
	}
	for _, each := range errs {
		if each.Kind != ErrHookFailed || each.Hook != HookAuthenticate || each.Plugin != "@app/a-broken" {
			t.Fatalf("error = %+v", each)
		}
	}
	server.close()
}

// Opus's security audit H1: a cross-site form posted with the owner's credentials.
func TestHostAndPrincipalsNeverAuthenticatesAForeignRequestRefusesAForeignOriginAndAnUnsafeBrowserRequestWithoutOne(t *testing.T) {
	var deleted atomic.Int32
	server := serving(t, []plugin{{"@app/notes", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("POST /delete", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			if httpKindOf(ctx.Principal()) == "owner" {
				deleted.Add(1)
			}
			return jsonOf(float64(deleted.Load())), nil
		}),
		RouteFunc("GET /whoami", httpWhoamiRoute),
	}})}}, servedOptions{})
	owner := map[string]string{"x-principal": "owner"}
	own := "http://127.0.0.1:" + strconv.Itoa(server.port)
	crossSite := httpMerge(owner, map[string]string{"sec-fetch-site": "cross-site"})
	if got := mustSend(t, server.port, "POST", "/delete", sendOptions{headers: httpMerge(crossSite, map[string]string{"origin": "https://evil.example"})}).status; got != 403 {
		t.Fatalf("foreign origin: %d, want 403", got)
	}
	if got := mustSend(t, server.port, "POST", "/delete", sendOptions{headers: httpMerge(owner, map[string]string{"sec-fetch-site": "same-origin"})}).status; got != 403 {
		t.Fatalf("same-origin without Origin: %d, want 403", got)
	}
	if got := mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: crossSite}).text(); got != "null" {
		t.Fatalf("cross-site whoami = %q, want null", got)
	}
	same := mustSend(t, server.port, "POST", "/delete", sendOptions{headers: httpMerge(owner, map[string]string{"origin": own, "sec-fetch-site": "same-origin"})})
	if same.status != 200 || same.text() != "1" {
		t.Fatalf("same = %d %q, want 200 1", same.status, same.text())
	}
	// Without Fetch Metadata (a script, the TUI): not a browser's request.
	if got := mustSend(t, server.port, "POST", "/delete", sendOptions{headers: owner}).text(); got != "2" {
		t.Fatalf("without Fetch Metadata = %q, want 2", got)
	}
	server.close()
}

func TestHostAndPrincipalsAuthenticatesATopLevelNavigationToAPageFromAnotherSiteACrossSiteRouteRunsAnonymous(t *testing.T) {
	seen := &collector[string]{}
	server := serving(t, []plugin{{"@app/pages", contributing(HttpContribution{Routes: []HttpRouteEntry{
		{Key: "GET /app", Route: HttpRouteOptions{Page: true, Handler: func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			seen.add(httpKindOf(ctx.Principal()))
			return htmlOf("app"), nil
		}}},
		{Key: "POST /hook", Route: HttpRouteOptions{CrossSite: true, Handler: func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			seen.add(httpKindOf(ctx.Principal()))
			return jsonOf(float64(1)), nil
		}}},
	}})}}, servedOptions{})
	navigate := map[string]string{
		"x-principal":    "owner",
		"sec-fetch-site": "cross-site",
		"sec-fetch-mode": "navigate",
		"sec-fetch-dest": "document",
	}
	mustSend(t, server.port, "GET", "/app", sendOptions{headers: navigate})
	mustSend(t, server.port, "GET", "/app", sendOptions{headers: httpMerge(navigate, map[string]string{"sec-fetch-dest": "iframe"})})
	hook := mustSend(t, server.port, "POST", "/hook", sendOptions{headers: map[string]string{
		"x-principal": "owner", "origin": "https://github.com", "sec-fetch-site": "cross-site",
	}})
	if got := seen.all(); hook.status != 200 || !reflect.DeepEqual(got, []string{"owner", "anonymous", "anonymous"}) {
		t.Fatalf("hook = %d, seen = %v", hook.status, got)
	}
	server.close()
}

func TestDefaultHeadersSendsTheBaselineOnEveryAnswerACSPWithTheResponsesOwnNonceAndTheContributedFramersAndSources(t *testing.T) {
	nonces := &collector[string]{}
	lastNonce := func() string {
		all := nonces.all()
		return all[len(all)-1]
	}
	server := serving(t, []plugin{{"@app/page", contributing(HttpContribution{
		Routes: []HttpRouteEntry{RouteFunc("GET /page", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			nonces.add(request.Nonce())
			return htmlOf(`<script nonce="` + request.Nonce() + `"></script>`), nil
		})},
		Framers: []string{"https://shell.example.com"},
		Csp:     []CspEntry{{Directive: "img-src", Sources: []string{"https://cdn.example.com"}}},
	})}}, servedOptions{})
	page := mustSend(t, server.port, "GET", "/page", sendOptions{})
	for name, want := range map[string]string{"cache-control": "no-store", "x-content-type-options": "nosniff", "referrer-policy": "no-referrer"} {
		if got := page.header(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := page.headers.Values("x-frame-options"); len(got) != 0 {
		t.Fatalf("x-frame-options = %v, want none", got)
	}
	nonce := lastNonce()
	want := "default-src 'none'; script-src 'self' 'nonce-" + nonce + "'; connect-src 'self' ws://127.0.0.1:" + strconv.Itoa(server.port) +
		"; base-uri 'none'; form-action 'self'; frame-ancestors https://shell.example.com; img-src https://cdn.example.com"
	if got := page.header("content-security-policy"); got != want {
		t.Fatalf("content-security-policy = %q, want %q", got, want)
	}
	// Fable's recheck: a nonce read from one page must not run a script injected into another.
	first := nonce
	mustSend(t, server.port, "GET", "/page", sendOptions{})
	if lastNonce() == first {
		t.Fatalf("the nonce %q repeated", first)
	}
	missing := mustSend(t, server.port, "GET", "/nope", sendOptions{})
	if missing.status != 404 || missing.header("x-content-type-options") != "nosniff" {
		t.Fatalf("missing = %d %q", missing.status, missing.header("x-content-type-options"))
	}
	if csp := missing.header("content-security-policy"); !regexp.MustCompile(`'nonce-[A-Za-z0-9+/]{22}=='`).MatchString(csp) {
		t.Fatalf("the missing answer's CSP has no nonce: %q", csp)
	} else if regexp.MustCompile(regexp.QuoteMeta(first)).MatchString(csp) {
		t.Fatalf("the missing answer's CSP %q contains the page's nonce", csp)
	}
	server.close()
	plain := serving(t, nil, servedOptions{})
	if got := mustSend(t, plain.port, "GET", "/", sendOptions{}).header("x-frame-options"); got != "DENY" {
		t.Fatalf("x-frame-options = %q, want DENY", got)
	}
	plain.close()
}

func TestDefaultHeadersLetsARelaysVerifiedRequestThroughWithItsFramer(t *testing.T) {
	framer := "https://relay.example"
	server := serving(t, []plugin{
		{"@app/who", httpWhoami},
		{"@app/relay", contributing(HttpContribution{Relays: []*HttpRelay{{
			Name: "radius",
			Verify: func(request *nethttp.Request) (*RelayVerified, error) {
				if request.Header.Get("x-relay") == "secret" {
					return &RelayVerified{Origin: "https://me.relay.example", Framer: &framer}, nil
				}
				return nil, nil
			},
		}}})},
	}, servedOptions{})
	forwarded := map[string]string{"x-forwarded-for": "203.0.113.9"}
	if got := mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: forwarded}).status; got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	relayed := mustSend(t, server.port, "GET", "/whoami", sendOptions{headers: httpMerge(forwarded, map[string]string{"x-relay": "secret"})})
	if relayed.status != 200 {
		t.Fatalf("status = %d, want 200", relayed.status)
	}
	if csp := relayed.header("content-security-policy"); !regexp.MustCompile(regexp.QuoteMeta("frame-ancestors https://relay.example")).MatchString(csp) {
		t.Fatalf("csp = %q", csp)
	}
	server.close()
}

func TestRoutesAndWrapsFailingAnswersAThrowingRoute500WithoutItsMessageAThrowingWrap500NamingItsPluginAnHttpFailureItsStatus(t *testing.T) {
	server := serving(t, []plugin{
		{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
			RouteFunc("GET /throws", func(HttpRequest, host.Ctx) (HttpResponse, error) { return HttpResponse{}, errors.New("secret") }),
			RouteFunc("GET /failure", func(HttpRequest, host.Ctx) (HttpResponse, error) {
				return HttpResponse{}, NewHttpFailure(422, "no")
			}),
		}})},
		{"@app/b", contributing(HttpContribution{
			Routes: []HttpRouteEntry{RouteFunc("GET /wrapped", func(HttpRequest, host.Ctx) (HttpResponse, error) { return jsonOf(float64(1)), nil })},
			Wraps: []HttpWrap{{
				Name:  "broken",
				Phase: PhaseHeaders,
				Wrap: func(HttpRequest, func(HttpRequest) (HttpResponse, error), host.Ctx) (HttpResponse, error) {
					return HttpResponse{}, errors.New("wrap")
				},
			}},
		})},
	}, servedOptions{})
	thrown := mustSend(t, server.port, "GET", "/throws", sendOptions{})
	if thrown.status != 500 || thrown.text() != "Internal Server Error" {
		t.Fatalf("thrown = %d %q", thrown.status, thrown.text())
	}
	if got := mustSend(t, server.port, "GET", "/failure", sendOptions{}).status; got != 422 {
		t.Fatalf("failure = %d, want 422", got)
	}
	if got := mustSend(t, server.port, "GET", "/wrapped", sendOptions{}).status; got != 500 {
		t.Fatalf("wrapped = %d, want 500", got)
	}
	errs := server.errors.all()
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want 2", errs)
	}
	if e := errs[0]; e.Kind != ErrRouteFailed || e.Pattern != "GET /throws" || e.Plugin != "@app/a" || e.Error == nil || e.Error.Error() != "secret" {
		t.Fatalf("errors[0] = %+v", e)
	}
	if e := errs[1]; e.Kind != ErrWrapFailed || e.Name != "broken" || e.Plugin != "@app/b" || e.Error == nil || e.Error.Error() != "wrap" {
		t.Fatalf("errors[1] = %+v", e)
	}
	server.close()
}

func TestRoutesAndWrapsFailingRunsWrapsOutermostFirstAroundTheRouteInItsRequestsScope(t *testing.T) {
	order := &collector[string]{}
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{
		Routes: []HttpRouteEntry{RouteFunc("GET /r", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			order.add("route as " + httpKindOf(ctx.Principal()))
			return jsonOf(float64(1)), nil
		})},
		Wraps: []HttpWrap{
			{
				Name:  "inner",
				Phase: PhaseTransform,
				Wrap: func(request HttpRequest, next func(HttpRequest) (HttpResponse, error), _ host.Ctx) (HttpResponse, error) {
					order.add("transform")
					return next(request)
				},
			},
			{
				Name:  "outer",
				Phase: PhaseObserve,
				Wrap: func(request HttpRequest, next func(HttpRequest) (HttpResponse, error), _ host.Ctx) (HttpResponse, error) {
					order.add("observe")
					response, err := next(request)
					if err != nil {
						return response, err
					}
					response.Headers = []ResponseHeader{HeaderOf("X-Observed", "1")}
					return response, nil
				},
			},
		},
	})}}, servedOptions{})
	got := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"x-principal": "owner"}})
	if want := []string{"observe", "transform", "route as owner"}; !reflect.DeepEqual(order.all(), want) || got.header("x-observed") != "1" {
		t.Fatalf("order = %v, x-observed = %q", order.all(), got.header("x-observed"))
	}
	server.close()
}

func TestReloadsAndCloseRefusesNewRequests503WaitsForRunningOnesUpToCloseTimeoutThenSealsThem503(t *testing.T) {
	sealed := &collector[host.Ctx]{}
	server := serving(t, []plugin{{"@app/slow", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /quick", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			time.Sleep(30 * time.Millisecond)
			return htmlOf("done"), nil
		}),
		RouteFunc("GET /stuck", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			sealed.add(ctx)
			<-ctx.Done()
			return HttpResponse{Kind: KindError, Status: 503}, nil
		}),
	}})}}, servedOptions{closeTimeout: 100 * time.Millisecond})
	type sent struct {
		answer answer
		err    error
	}
	request := func(path string) <-chan sent {
		return httpAsync(func() sent {
			got, err := send(t, server.port, "GET", path, sendOptions{})
			return sent{got, err}
		})
	}
	quick := request("/quick")
	stuck := request("/stuck")
	time.Sleep(10 * time.Millisecond)
	closing := httpAsync(func() struct{} {
		server.http.Close(background)
		return struct{}{}
	})
	// Pi's `close` refuses synchronously before it returns its promise; Close blocks, so the test waits for it to have started.
	eventually(t, "close refuses", server.component.isClosing)
	late := httpFetch(server, "/quick")
	if late.StatusCode != 503 {
		t.Fatalf("a request while closing: %d, want 503", late.StatusCode)
	}
	late.Body.Close()
	if got := receive(t, quick, "the quick answer"); got.err != nil || got.answer.text() != "done" {
		t.Fatalf("quick = %q, %v", got.answer.text(), got.err)
	}
	receive(t, closing, "close")
	if contexts := sealed.all(); len(contexts) != 1 || contexts[0].Err() == nil {
		t.Fatalf("the stuck request's scope was not sealed: %v", contexts)
	}
	if err := server.host.Close(background); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, stuck, "the stuck answer"); got.err != nil || got.answer.status != 503 {
		t.Fatalf("stuck = %d, %v, want 503", got.answer.status, got.err)
	}
	if _, err := send(t, server.port, "GET", "/quick", sendOptions{}); err == nil {
		t.Fatal("a request after close was answered")
	}
}

// Astra's build review: a request authenticating when close began ran its route afterwards.
func TestReloadsAndCloseNeverRunsARouteForARequestStillAuthenticatingWhenCloseSealedIt(t *testing.T) {
	entered := newGate()
	release := newGate()
	var calls atomic.Int32
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			calls.Add(1)
			return jsonOf(float64(1)), nil
		}),
	}})}}, servedOptions{
		closeTimeout: 20 * time.Millisecond,
		authenticate: func(HttpRequest, context.Context) (data.Principal, error) {
			entered.resolve()
			<-release.done()
			return principalOwner(), nil
		},
	})
	answered := httpAsync(func() *nethttp.Response { return httpFetch(server, "/") })
	receive(t, entered.done(), "the authenticator")
	server.http.Close(background)
	release.resolve()
	response := receive(t, answered, "the answer")
	if response.StatusCode != 503 || calls.Load() != 0 {
		t.Fatalf("status = %d, calls = %d, want 503, 0", response.StatusCode, calls.Load())
	}
	server.close()
}

func TestMountingWithoutAConnectionARequestIsNeverLocal(t *testing.T) {
	server := serving(t, []plugin{{"@app/who", httpWhoami}}, servedOptions{noListen: true})
	if got := server.http.Fetch(loopback("/whoami"), Connection{}).StatusCode; got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	server.close()
}

// Sol's second recheck C: after cancellation the chain still started the next authenticator.
func TestCancellationStartsNoFurtherAuthenticatorOnceCloseSealedTheRequest(t *testing.T) {
	entered := newGate()
	release := newGate()
	var later atomic.Int32
	server := serving(t, []plugin{
		{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
			RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) { return htmlOf("ok"), nil }),
		}})},
		{"@app/z-later", authenticating(func(HttpRequest, context.Context) (data.Principal, error) {
			later.Add(1)
			return principalOwner(), nil
		})},
	}, servedOptions{
		noListen:     true,
		closeTimeout: 10 * time.Millisecond,
		authenticate: func(HttpRequest, context.Context) (data.Principal, error) {
			entered.resolve()
			<-release.done()
			return nil, nil
		},
	})
	answered := httpAsync(func() *nethttp.Response { return httpFetch(server, "/") })
	receive(t, entered.done(), "the authenticator")
	server.http.Close(background)
	if got := receive(t, answered, "the answer").StatusCode; got != 503 {
		t.Fatalf("status = %d, want 503", got)
	}
	release.resolve()
	time.Sleep(20 * time.Millisecond)
	if got := later.Load(); got != 0 {
		t.Fatalf("later = %d, want 0", got)
	}
	server.close()
}

func TestCancellationAsksNoAuthenticatorForARequestAbortedBeforeItArrived(t *testing.T) {
	var calls atomic.Int32
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) { return htmlOf("ok"), nil }),
	}})}}, servedOptions{
		noListen: true,
		authenticate: func(HttpRequest, context.Context) (data.Principal, error) {
			calls.Add(1)
			return nil, nil
		},
	})
	httpFetch(server, "/", httpWithContext(httpAborted())).Body.Close()
	if got := calls.Load(); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
	server.close()
}

// Sol's third recheck: a request aborted while its route returned the `bytes` description got a stream born sealed,
// which never errored; the request never finished.
func TestCancellationEndsARequestWhoseBytesResponseWasBornSealed(t *testing.T) {
	for _, when := range []string{"inside the route", "after the route returned"} {
		t.Run("aborted "+when, func(t *testing.T) {
			ctx, abort := httpAbortable()
			entered := newGate()
			var reads atomic.Int32
			var bornSealed atomic.Bool
			server := serving(t, []plugin{{"@app/b", contributing(HttpContribution{Routes: []HttpRouteEntry{
				RouteFunc("GET /", func(_ HttpRequest, scope host.Ctx) (HttpResponse, error) {
					entered.resolve()
					if when == "inside the route" {
						abort(errors.New("client gone"))
						// An AbortSignal's abort reaches the scope's signal at once; Go links contexts on a goroutine of its own,
						// so the route waits for the link before it reads the scope.
						select {
						case <-scope.Done():
						case <-time.After(within):
						}
					}
					bornSealed.Store(scope.Err() != nil)
					return HttpResponse{Kind: KindBytes, Mime: "application/octet-stream", Read: func(int64, int, host.Ctx) ([]byte, error) {
						reads.Add(1)
						return []byte{1}, nil
					}}, nil
				}),
			}})}}, servedOptions{noListen: true, closeTimeout: time.Second})
			answered := httpAsync(func() *nethttp.Response { return httpFetch(server, "/", httpWithContext(ctx)) })
			receive(t, entered.done(), "the route")
			if when == "after the route returned" {
				abort(errors.New("client gone"))
			}
			receive(t, answered, "the answer")
			time.Sleep(10 * time.Millisecond)
			started := time.Now()
			server.http.Close(background)
			if elapsed := time.Since(started); elapsed >= 500*time.Millisecond || reads.Load() != 0 || bornSealed.Load() != (when == "inside the route") {
				t.Fatalf("close took %v, reads = %d, bornSealed = %v", elapsed, reads.Load(), bornSealed.Load())
			}
			server.close()
		})
	}
}

// Sol's second recheck B: an aborted `Response` nobody touched again kept its request: close waited its bound.
func TestCancellationEndsARequestWhoseBytesResponseWasAbortedAndDroppedWithoutAnotherRead(t *testing.T) {
	server := serving(t, []plugin{{"@app/b", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return HttpResponse{Kind: KindBytes, Mime: "application/octet-stream", Read: func(int64, int, host.Ctx) ([]byte, error) {
				return []byte{1}, nil
			}}, nil
		}),
	}})}}, servedOptions{noListen: true, closeTimeout: time.Second})
	ctx, abort := httpAbortable()
	httpFetch(server, "/", httpWithContext(ctx))
	abort(errors.New("client gone"))
	time.Sleep(10 * time.Millisecond)
	started := time.Now()
	server.http.Close(background)
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("close took %v, want under 500ms", elapsed)
	}
	server.close()
}

// Astra's recheck R4, Sol's N1: a close before the first resolution started a stopped watch; its throw ended the process.
func TestCancellationACloseBeforeThePublicationLeavesNothingRunningTheWaitingRequestAnswers503(t *testing.T) {
	server := serving(t, nil, servedOptions{noLoad: true, noListen: true})
	waiting := httpAsync(func() *nethttp.Response { return httpFetch(server, "/x") })
	server.http.Close(httpAborted())
	server.load()
	if got := receive(t, waiting, "the waiting request").StatusCode; got != 503 {
		t.Fatalf("status = %d, want 503", got)
	}
	time.Sleep(50 * time.Millisecond)
	server.close()
}

func TestCancellationACloseWhileTheFirstRequestResolvesTheRegistriesStartsNoFollow(t *testing.T) {
	server := serving(t, []plugin{{"@app/who", httpWhoami}}, servedOptions{noListen: true, closeTimeout: 10 * time.Millisecond})
	pending := httpAsync(func() *nethttp.Response { return httpFetch(server, "/whoami") })
	timeout, cancel := context.WithTimeout(background, 20*time.Millisecond)
	defer cancel()
	server.http.Close(timeout)
	if got := receive(t, pending, "the pending request").StatusCode; got != 200 && got != 503 {
		t.Fatalf("status = %d, want 200 or 503", got)
	}
	time.Sleep(50 * time.Millisecond)
	server.close()
}

// Sol's recheck N9.
func TestCancellationACloseWhoseContextIsAbortedAlreadyDoesNotWait(t *testing.T) {
	entered := newGate()
	release := newGate()
	server := serving(t, []plugin{{"@app/wait", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /wait", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			entered.resolve()
			_ = release.wait(ctx)
			return htmlOf("done"), nil
		}),
	}})}}, servedOptions{noListen: true, closeTimeout: time.Second})
	answered := httpAsync(func() *nethttp.Response { return httpFetch(server, "/wait") })
	receive(t, entered.done(), "the route")
	started := time.Now()
	server.http.Close(httpAborted())
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("close took %v, want under 500ms", elapsed)
	}
	release.resolve()
	receive(t, answered, "the answer")
	server.close()
}

// Sol's recheck N5: an authenticator ignoring its signal kept fetch pending after close returned.
func TestCancellationCloseSettlesARequestWhoseAuthenticatorIgnoresItsSignal503(t *testing.T) {
	entered := newGate()
	release := newGate()
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) { return htmlOf("ok"), nil }),
	}})}}, servedOptions{
		noListen:     true,
		closeTimeout: 10 * time.Millisecond,
		authenticate: func(HttpRequest, context.Context) (data.Principal, error) {
			entered.resolve()
			<-release.done()
			return principalOwner(), nil
		},
	})
	answered := httpAsync(func() *nethttp.Response { return httpFetch(server, "/") })
	receive(t, entered.done(), "the authenticator")
	server.http.Close(background)
	settled := -1
	select {
	case response := <-answered:
		settled = response.StatusCode
	case <-time.After(30 * time.Millisecond):
	}
	if settled != 503 {
		t.Fatalf("settled = %d, want 503 (-1: pending)", settled)
	}
	release.resolve()
	server.close()
}

// Astra's recheck R2, Sol's N3: a held `Response` went on calling `read` after its scope was sealed.
func TestCancellationReadsNoBytesAfter(t *testing.T) {
	for _, kind := range []string{"the request aborted", "the component closed"} {
		t.Run(kind, func(t *testing.T) {
			var reads atomic.Int32
			var readAfterSeal atomic.Bool
			ctx, abort := httpAbortable()
			server := serving(t, []plugin{{"@app/bytes", contributing(HttpContribution{Routes: []HttpRouteEntry{
				RouteFunc("GET /stream", func(HttpRequest, host.Ctx) (HttpResponse, error) {
					return HttpResponse{Kind: KindBytes, Mime: "application/octet-stream", Read: func(_ int64, _ int, scope host.Ctx) ([]byte, error) {
						n := reads.Add(1)
						if scope.Err() != nil {
							readAfterSeal.Store(true)
						}
						return []byte{byte(n)}, nil
					}}, nil
				}),
			}})}}, servedOptions{noListen: true, closeTimeout: 10 * time.Millisecond})
			response := httpFetch(server, "/stream", httpWithContext(ctx))
			buffer := make([]byte, 64)
			if _, err := response.Body.Read(buffer); err != nil {
				t.Fatal(err)
			}
			if kind == "the request aborted" {
				abort(errors.New("client gone"))
			} else {
				server.http.Close(background)
			}
			time.Sleep(10 * time.Millisecond)
			if _, err := response.Body.Read(buffer); err == nil {
				t.Fatal("a read after the seal succeeded")
			}
			if reads.Load() != 1 || readAfterSeal.Load() {
				t.Fatalf("reads = %d, readAfterSeal = %v, want 1, false", reads.Load(), readAfterSeal.Load())
			}
			server.close()
		})
	}
}

// Sol's recheck N4.
func TestCancellationABodyThatErrorsAfterValidJSONFailsTheRequest400TheRoutesSideEffectNeverRuns(t *testing.T) {
	var writes atomic.Int32
	server := serving(t, []plugin{{"@app/body", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("POST /body", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			value, err := request.JSON()
			if err != nil {
				return HttpResponse{}, err
			}
			writes.Add(1)
			return jsonOf(value), nil
		}),
	}})}}, servedOptions{noListen: true})
	response := httpFetch(server, "/body", func(request *nethttp.Request) {
		request.Method = "POST"
		request.Body = io.NopCloser(&httpFailingBody{})
		request.ContentLength = -1
	})
	if response.StatusCode != 400 || writes.Load() != 0 {
		t.Fatalf("status = %d, writes = %d, want 400, 0", response.StatusCode, writes.Load())
	}
	response.Body.Close()
	server.close()
}

// Sol's recheck N8: `Response` threw on a body for these statuses.
func TestAnswersAnswersWithoutABody(t *testing.T) {
	for _, status := range []int{204, 205, 304} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := serving(t, []plugin{{"@app/s", contributing(HttpContribution{Routes: []HttpRouteEntry{
				RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) {
					return HttpResponse{Kind: KindJSON, Value: nil, Status: status}, nil
				}),
			}})}}, servedOptions{noListen: true})
			response := httpFetch(server, "/")
			if _, has := response.Header["Content-Length"]; response.StatusCode != status || response.Body != nethttp.NoBody || has {
				t.Fatalf("status = %d, body = %v, has content-length = %v", response.StatusCode, response.Body, has)
			}
			server.close()
		})
	}
}

// Fable's recheck N3: the kernel's `forbidden` reached the client as a reported 500.
func TestAnswersAForbiddenEscapingARoute403Unreported(t *testing.T) {
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return HttpResponse{}, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: "no"}, nil)
		}),
	}})}}, servedOptions{noListen: true})
	if got := httpFetch(server, "/").StatusCode; got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	if errs := server.errors.all(); len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	server.close()
}

// Opus's recheck R2: one value per header name allowed one cookie per response.
func TestAnswersSendsSeveralSetCookieHeaders(t *testing.T) {
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return HttpResponse{Kind: KindJSON, Value: float64(1), Headers: []ResponseHeader{HeaderOf("Set-Cookie", "a=1", "b=2")}}, nil
		}),
	}})}}, servedOptions{})
	want := []string{"a=1", "b=2"}
	if got := mustSend(t, server.port, "GET", "/", sendOptions{}).headers.Values("Set-Cookie"); !reflect.DeepEqual(got, want) {
		t.Fatalf("set-cookie = %v, want %v", got, want)
	}
	request, err := nethttp.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(server.port)+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Host", fmt.Sprintf("127.0.0.1:%d", server.port))
	fetched := server.http.Fetch(request, Connection{LocalAddress: "127.0.0.1", LocalPort: server.port})
	if got := fetched.Header.Values("Set-Cookie"); !reflect.DeepEqual(got, want) {
		t.Fatalf("fetched set-cookie = %v, want %v", got, want)
	}
	fetched.Body.Close()
	server.close()
}

// Sol's second recheck B, her GC probe: 8 of 8 such requests kept their scopes.
func TestGCCollectsTheScopesOfAbortedBytesResponsesDroppedWithoutAnotherRead(t *testing.T) {
	refs := &collector[weak.Pointer[byte]]{}
	server := serving(t, []plugin{{"@app/b", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			refs.add(httpWeak(ctx))
			return HttpResponse{Kind: KindBytes, Mime: "application/octet-stream", Read: func(int64, int, host.Ctx) ([]byte, error) {
				return []byte{1}, nil
			}}, nil
		}),
	}})}}, servedOptions{noListen: true})
	// In a function of its own: the test's frame keeps no last response.
	fetchAborted := func() {
		ctx, abort := httpAbortable()
		httpFetch(server, "/", httpWithContext(ctx))
		abort(errors.New("client gone"))
	}
	for range 8 {
		fetchAborted()
	}
	httpExpectCollected(t, refs.all())
	server.close()
}

func TestGCCollectsTheScopesOfBytesResponsesBornSealedAbortedWhileTheRouteReturned(t *testing.T) {
	refs := &collector[weak.Pointer[byte]]{}
	var mu sync.Mutex
	var aborts []context.CancelCauseFunc
	server := serving(t, []plugin{{"@app/b", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			refs.add(httpWeak(ctx))
			mu.Lock()
			if len(aborts) > 0 {
				aborts[len(aborts)-1](errors.New("client gone"))
			}
			mu.Unlock()
			return HttpResponse{Kind: KindBytes, Mime: "application/octet-stream", Read: func(int64, int, host.Ctx) ([]byte, error) {
				return []byte{1}, nil
			}}, nil
		}),
	}})}}, servedOptions{noListen: true})
	// In a function of its own: the test's frame keeps no last request.
	fetchAborted := func() {
		ctx, abort := httpAbortable()
		mu.Lock()
		aborts = append(aborts, abort)
		mu.Unlock()
		httpFetch(server, "/", httpWithContext(ctx))
	}
	for range 8 {
		fetchAborted()
	}
	mu.Lock()
	aborts = nil
	mu.Unlock()
	httpExpectCollected(t, refs.all())
	server.close()
}

func TestGCCollectsRequestsScopesAndRequestsOnceAnswered(t *testing.T) {
	refs := &collector[weak.Pointer[byte]]{}
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /r", func(request HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			refs.add(httpWeak(ctx))
			refs.add(httpWeak(request))
			return jsonOf(float64(1)), nil
		}),
	}})}}, servedOptions{})
	for range 5 {
		mustSend(t, server.port, "GET", "/r", sendOptions{})
	}
	httpExpectCollected(t, refs.all())
	server.close()
}
