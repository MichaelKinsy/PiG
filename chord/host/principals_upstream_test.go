package host

// Ports packages/chord/test/principals.test.ts (Pi aa9cf15b, protocol 3). The first case's TypeScript half (a typed
// host, facet API and context usable where the untyped ones are expected; `@ts-expect-error` on a stranger principal and
// on a pattern naming fields the principals lack) has no Go form: Principal is `any`. Its runtime half is below.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// principalsKey is principals.test.ts principalKey: a guest is `guest:<id>`, anything else its kind.
func principalsKey(principal Principal) string {
	kind, _ := inputField(principal, "kind").(string)
	if kind == "guest" {
		return fmt.Sprintf("guest:%v", inputField(principal, "id"))
	}
	return kind
}

// principals.test.ts "factories, methods and contexts see the app's principal type"
func TestPrincipalsFactoriesMethodsAndContextsSeeTheAppsPrincipal(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: map[string]any{"kind": "host"}, PrincipalKey: principalsKey})
	who := Local("t.who", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"who", Method(nil, tString())}}})
	taskKind := make(chan string, 1)
	facet := Facet{Name: "who", FacetDefinition: DefineFacet(func(api FacetApi) error {
		return api.Serve(who, func(_ any, principal Principal, self Self) (Implementation, error) {
			created := inputField(principal, "kind").(string)
			if created == "guest" {
				created = "guest " + inputField(principal, "id").(string)
			}
			if err := self.Task(func(ctx Ctx) error {
				taskKind <- inputField(ctx.Principal(), "kind").(string)
				return nil
			}); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf("who", methodFunc(func(_ any, ctx Ctx) (any, error) {
				return created + ", called by " + inputField(ctx.Principal(), "kind").(string), nil
			})), nil
		}, ServeOptions{})
	}, DefineFacetOptions{})}
	if err := host.Install(facet, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	guest := map[string]any{"kind": "guest", "id": "g1"}
	ref := requestsGet(t, host, who, map[string]any{}, &GetOptions{Principal: guest, HasPrincipal: true})
	got, err := host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "who", nil, ctx) }, RootOptions{Principal: guest})
	if err != nil || got != "guest g1, called by host" {
		t.Fatalf("who = %v, %v", got, err)
	}
	waitFor(t, "the task", func() bool { return len(taskKind) == 1 })
	if kind := <-taskKind; kind != "host" {
		t.Fatalf("a task's ctx principal kind = %q, want host", kind)
	}
}

// principals.test.ts "the host's own principal is recognized by key: an equal object reaches the same objects"
func TestPrincipalsTheHostsOwnPrincipalIsRecognizedByKeyAnEqualObjectReachesTheSameObjects(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: map[string]any{"kind": "host"}, PrincipalKey: principalsKey})
	who := Local("t.who", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"who", Method(nil, tString())}}})
	var created principalLog
	mustServe(t, host, who, func(any, Principal, Self) (Implementation, error) {
		created.add(nil)
		return chordjson.ObjectOf("who", methodFunc(func(any, Ctx) (any, error) { return "one", nil })), nil
	}, ServeOptions{})
	own := requestsGet(t, host, who, map[string]any{}, nil)
	equal := requestsGet(t, host, who, map[string]any{}, &GetOptions{Principal: map[string]any{"kind": "host"}, HasPrincipal: true})
	if err := errors.Join(readyErr(t, own), readyErr(t, equal)); err != nil {
		t.Fatal(err)
	}
	if n := len(created.items()); n != 1 {
		t.Fatalf("created %d objects, want 1", n)
	}
	// Never disposed as a matching principal: it is the host's own.
	if err := host.InvalidatePrincipal(PrincipalMatch{Pattern: chordjson.ObjectOf("kind", "host")}); err != nil {
		t.Fatal(err)
	}
	if own.Status() != RefLive {
		t.Fatalf("status = %s, want live", own.Status())
	}
}

// principals.test.ts "a child's parent acts as the principal the child gave `connect`; relayed callers keep theirs"
func TestPrincipalsAChildsParentActsAsThePrincipalTheChildGaveConnectRelayedCallersKeepTheirs(t *testing.T) {
	echo := Remote("t.echo", RemoteDef{
		Version: 1,
		Input:   tObject(),
		Host:    NewHostNamed("worker"),
		Variants: Fields[VariantDef]{{"main", VariantDef{
			Methods: Fields[MethodSpec]{{"who", Method(nil, tString())}},
		}}},
	})
	self := func() Principal { return map[string]any{"kind": "host"} }
	server := requestsHost(t, HostOptions{Name: "server", Self: self(), PrincipalKey: principalsKey})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Kind: KindHost, Name: new("worker"), Principal: self()}); err != nil {
		t.Fatal(err)
	}
	worker := requestsHost(t, HostOptions{
		Name: "worker", Self: self(), PrincipalKey: principalsKey,
		Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: map[string]any{"kind": "server"}}},
	})
	mustServe(t, worker, echo, func(_ any, principal Principal, _ Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "who", methodFunc(func(any, Ctx) (any, error) { return principalsKey(principal), nil })), nil
	}, ServeOptions{})
	browserDown, browserUp := MemoryLinkPair()
	if err := server.Accept(browserDown, AcceptOptions{Principal: map[string]any{"kind": "guest", "id": "g1"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{
		Name: "browser", Kind: KindClient, Self: self(),
		Upstream: &UpstreamOptions{Link: browserUp, ConnectOptions: ConnectOptions{Principal: map[string]any{"kind": "server"}}},
	})
	waitLinked(t, server, worker)
	if got := mustCall(t, requestsGet(t, server, echo, map[string]any{}, nil), "who", nil, nil); got != "server" {
		t.Fatalf("the parent's own request acted as %v, want server", got)
	}
	server.Route("worker", Route{
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve:   func(string) (Link, error) { return down, nil },
		Principal: func(string) Principal { return self() },
	})
	if got := mustCall(t, requestsGet(t, browser, echo, map[string]any{}, nil), "who", nil, nil); got != "guest:g1" {
		t.Fatalf("the relayed caller acted as %v, want guest:g1", got)
	}
}

// principals.test.ts "refuses a host, a parent link or a route without its principal" (host.ts constructor, connect and route).
func TestPrincipalsRefusesAHostAParentLinkOrARouteWithoutItsPrincipal(t *testing.T) {
	thrown := func(fn func()) (message string) {
		defer func() {
			if recovered := recover(); recovered != nil {
				message = recovered.(*TypeError).Message
			}
		}()
		fn()
		return ""
	}
	if got := thrown(func() { CreateHost(HostOptions{Name: "x"}) }); got != "createHost needs `self`: the principal of the host's own code" {
		t.Fatalf("createHost: %q", got)
	}
	host := CreateHost(HostOptions{Self: kindHost()})
	if got := thrown(func() { host.Connect(&fakeLink{}, ConnectOptions{}) }); got != "connect needs `principal`: what the parent's own requests act as" {
		t.Fatalf("connect: %q", got)
	}
	if got := thrown(func() { host.Route("w:", Route{}) }); got != "route w: needs `principal`: what the routed hosts act as when they call back" {
		t.Fatalf("route: %q", got)
	}
	// An explicit null is a principal.
	CreateHost(HostOptions{HasSelf: true}).Connect(&fakeLink{}, ConnectOptions{HasPrincipal: true})
}
