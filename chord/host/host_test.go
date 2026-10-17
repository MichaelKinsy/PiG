package host

// Tests of the implementation packages/chord/src/host.ts and cluster.ts (Pi v1.2.0) decide themselves: defaults, facet
// installation, roots, links, principal invalidation, routing, registry views, addresses and the cluster vote
// protocol. Each test cites the Pi lines whose contract it asserts.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

type fakeLink struct {
	mu      sync.Mutex
	frames  []Frame
	closed  bool
	reasons []string
	onClose []func(string)
}

func (link *fakeLink) Closed() bool {
	link.mu.Lock()
	defer link.mu.Unlock()
	return link.closed
}

func (link *fakeLink) Send(frame Frame) (int, error) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.frames = append(link.frames, frame)
	return 0, nil
}

func (link *fakeLink) Receive(func(Frame)) {}

func (link *fakeLink) OnClose(listener func(string)) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.onClose = append(link.onClose, listener)
}

func (link *fakeLink) Close(reason ...string) {
	link.mu.Lock()
	if link.closed {
		link.mu.Unlock()
		return
	}
	link.closed = true
	text := ""
	if len(reason) > 0 {
		text = reason[0]
	}
	link.reasons = append(link.reasons, text)
	listeners := slices.Clone(link.onClose)
	link.mu.Unlock()
	for _, listener := range listeners {
		listener(text)
	}
}

func (link *fakeLink) sent() []Frame {
	link.mu.Lock()
	defer link.mu.Unlock()
	return slices.Clone(link.frames)
}

func framesOf[F Frame](link *fakeLink) []F {
	var out []F
	for _, frame := range link.sent() {
		if typed, ok := frame.(F); ok {
			out = append(out, typed)
		}
	}
	return out
}

func testContract(name string) *Contract { return testContractOn(name, "host") }

// testContractOn is testContract placed on the host named placedOn.
func testContractOn(name, placedOn string) *Contract {
	return Remote(name, RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed(placedOn), Variants: Fields[VariantDef]{{"", VariantDef{Methods: Fields[MethodSpec]{{"ping", Method(tObject(), tObject())}}}}}})
}

func noFactory(any, Principal, Self) (Implementation, error) { return chordjson.ObjectOf(), nil }

func childConnection(t *testing.T, host *HostImpl, name string) (*Connection, *fakeLink) {
	t.Helper()
	link := &fakeLink{}
	if err := host.Accept(link, AcceptOptions{Principal: chordjson.ObjectOf("host", name), Kind: KindHost, Name: &name}); err != nil {
		t.Fatal(err)
	}
	var connection *Connection
	host.run(func() {
		for _, candidate := range host.connections.Keys() {
			if candidate.link == link {
				connection = candidate
			}
		}
	})
	return connection, link
}

func messageOf(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// host.ts:233-251: name defaults to "host", the first code version is 1 and there is no previous code version.
func TestCreateHostDefaults(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	if host.Name() != "host" || host.CodeVersion() != 1 {
		t.Fatalf("name %q code version %d", host.Name(), host.CodeVersion())
	}
	if _, ok := host.PreviousCodeVersion(); ok {
		t.Fatal("a new host has a previous code version")
	}
	if named := CreateHost(HostOptions{Self: kindHost(), Name: "server"}); named.Name() != "server" {
		t.Fatalf("name %q", named.Name())
	}
}

// host.ts isSelf (aa9cf15b): the host's own principal is the one given as self and is recognized by principalKey, so an
// equal object is the host's own and another principal is not.
func TestHostRecognizesItsOwnPrincipalByKey(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	if !host.isSelf(host.principal) || !host.isSelf(kindHost()) || !host.isSelf(chordjson.ObjectOf("kind", "host")) {
		t.Fatal("an equal principal is the host's own")
	}
	if host.isSelf(kindServer()) || host.isSelf(nil) {
		t.Fatal("another principal is not the host's own")
	}
	if host.principal == nil || CanonicalJSON(host.principal) != `{"kind":"host"}` {
		t.Fatalf("self %s", CanonicalJSON(host.principal))
	}
}

// host.ts:85-88: defineFacet returns { setup, uses }.
func TestDefineFacetKeepsSetupAndUses(t *testing.T) {
	uses := []*Contract{testContract("t.used")}
	called := false
	definition := DefineFacet(func(FacetApi) error {
		called = true
		return nil
	}, DefineFacetOptions{Uses: uses})
	if len(definition.Uses) != 1 || definition.Uses[0] != uses[0] {
		t.Fatalf("uses %v", definition.Uses)
	}
	if err := definition.Setup(FacetApi{}); err != nil || !called {
		t.Fatal("setup is not the given function")
	}
}

// host.ts:254-259 and 927-930: nextCodeVersionId adopts a newer given code version, refuses one not newer than the current, and
// otherwise allocates the next; peekNextCodeVersion is the one after the last allocated.
func TestNextCodeVersionId(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	host.run(func() {
		one := 1
		if _, err := host.nextCodeVersionId(&one); messageOf(err) != "code version 1 is not newer than 1" {
			t.Errorf("given 1: %v", err)
		}
		if next, err := host.nextCodeVersionId(nil); err != nil || next != 2 {
			t.Errorf("next %d %v", next, err)
		}
		five := 5
		if next, err := host.nextCodeVersionId(&five); err != nil || next != 5 {
			t.Errorf("given 5: %d %v", next, err)
		}
		if host.peekNextCodeVersion() != 6 {
			t.Errorf("peek %d", host.peekNextCodeVersion())
		}
	})
}

// host.ts:307-316: modules need a loader; the loader gets only the modules, in order, and facets keep their places.
func TestResolvePluginRefs(t *testing.T) {
	_, err := CreateHost(HostOptions{Self: kindHost()}).resolve([]PluginRef{ModuleRef("./a.ts")})
	if messageOf(err) != "loading modules needs createHost({ loader }), e.g. nodeLoader from @earendil-works/chord/node" {
		t.Fatalf("without loader: %v", err)
	}
	var asked []string
	host := CreateHost(HostOptions{Self: kindHost(), Loader: func(modules []string) ([]Facet, error) {
		asked = modules
		facets := make([]Facet, len(modules))
		for index, module := range modules {
			facets[index] = Facet{Name: module}
		}
		return facets, nil
	}})
	facets, err := host.resolve([]PluginRef{ModuleRef("a"), &Facet{Name: "inline"}, ModuleRef("b")})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{facets[0].Name, facets[1].Name, facets[2].Name}
	if !slices.Equal(asked, []string{"a", "b"}) || !slices.Equal(names, []string{"a", "inline", "b"}) {
		t.Fatalf("asked %v, facets %v", asked, names)
	}
	loaderFailure := errors.New("import failed")
	failing := CreateHost(HostOptions{Self: kindHost(), Loader: func([]string) ([]Facet, error) { return nil, loaderFailure }})
	if _, err := failing.resolve([]PluginRef{ModuleRef("a")}); !errors.Is(err, loaderFailure) {
		t.Fatalf("loader failure: %v", err)
	}
}

// host.ts:267-287 and 318-330: a facet serves through its api into the current code version, the api names the host, a facet
// name and a type are installed once, lazy types create no object, and serve/install refuse to run during a reload.
func TestInstallServesThroughFacetApi(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Name: "server"})
	a := testContractOn("t.a", "server")
	var apiName string
	facet := Facet{Name: "f", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		apiName = api.Name
		return api.Serve(a, noFactory, ServeOptions{})
	}}}
	if err := host.Install(facet, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	host.run(func() {
		if service, _ := host.current.services.Get("t.a"); service == nil || service.facet != "f" {
			t.Errorf("service %+v", service)
		}
		if !host.facets.Has("f") || host.objects.Len() != 0 {
			t.Errorf("facets %v objects %d", host.facets.Keys(), host.objects.Len())
		}
	})
	if apiName != "server" {
		t.Fatalf("api name %q", apiName)
	}
	if err := host.Install(facet, LoadOptions{}); messageOf(err) != "facet f is already installed; use reload()" {
		t.Fatalf("second install: %v", err)
	}
	other := Facet{Name: "g", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(a, noFactory, ServeOptions{})
	}}}
	if err := host.Install(other, LoadOptions{}); messageOf(err) != "t.a is already served" {
		t.Fatalf("duplicate type: %v", err)
	}
	host.run(func() {
		if host.facets.Has("g") {
			t.Error("a facet whose setup failed is installed")
		}
	})
	if err := host.Serve(a, noFactory, ServeOptions{}); messageOf(err) != "t.a is already served" {
		t.Fatalf("host serve duplicate: %v", err)
	}
	host.run(func() { host.reloading = true })
	if err := host.Serve(testContract("t.b"), noFactory, ServeOptions{}); messageOf(err) != "host.serve during a reload; serve from a facet passed to reload()" {
		t.Fatalf("serve during reload: %v", err)
	}
	if err := host.Install(Facet{Name: "h"}, LoadOptions{}); messageOf(err) != "host.install during a reload" {
		t.Fatalf("install during reload: %v", err)
	}
}

// host.ts:289-293: load installs every facet (no await between installs) and fails with the first install error.
func TestLoadInstallsInOrder(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	var order []string
	setup := func(name string) Facet {
		return Facet{Name: name, FacetDefinition: FacetDefinition{Setup: func(FacetApi) error {
			order = append(order, name)
			return nil
		}}}
	}
	a, b := setup("a"), setup("b")
	if err := host.Load([]PluginRef{&a, &b}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"a", "b"}) {
		t.Fatalf("order %v", order)
	}
	c := setup("c")
	if err := host.Load([]PluginRef{&c, &a}, LoadOptions{}); messageOf(err) != "facet a is already installed; use reload()" {
		t.Fatalf("reinstall: %v", err)
	}
	host.run(func() {
		if !slices.Equal(host.facets.Keys(), []string{"a", "b", "c"}) {
			t.Errorf("facets %v", host.facets.Keys())
		}
	})
}

// host.ts:438-489: a root runs fn with a ctx of a fresh root id (name#incarnation:seq) in the current code version as the host
// principal (or the given one), returns fn's result, and unregisters when it settles; an unretained code version is
// superseded.
func TestRootRunsInCodeVersion(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Name: "s"})
	var ids []string
	for range 2 {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			ids = append(ids, ctx.RootID())
			if ctx.CodeVersion() != 1 || !host.isSelf(ctx.Principal()) || ctx.Signal() == nil {
				t.Errorf("ctx code version %d principal %v", ctx.CodeVersion(), ctx.Principal())
			}
			host.run(func() {
				if host.current.roots[ctx.RootID()] != 1 {
					t.Errorf("root not running: %v", host.current.roots)
				}
			})
			return 42, nil
		}, RootOptions{})
		if err != nil || value != 42 {
			t.Fatalf("value %v err %v", value, err)
		}
	}
	prefix, _, _ := strings.Cut(ids[0], ":")
	if !strings.HasPrefix(ids[0], "s#") || len(prefix) != len("s#")+6 || ids[0] != prefix+":1" || ids[1] != prefix+":2" {
		t.Fatalf("root ids %v", ids)
	}
	host.run(func() {
		if len(host.current.roots) != 0 || len(host.local) != 0 {
			t.Errorf("roots %v local %v", host.current.roots, host.local)
		}
	})
	user := chordjson.ObjectOf("user", "u1")
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		if ctx.Principal() != user {
			t.Errorf("principal %v", ctx.Principal())
		}
		return nil, nil
	}, RootOptions{Principal: user}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("root failed")
	if _, err := host.Root(func(Ctx) (any, error) { return nil, failure }, RootOptions{}); !errors.Is(err, failure) {
		t.Fatalf("fn error: %v", err)
	}
	seven := 7
	_, err := host.Root(func(Ctx) (any, error) {
		t.Error("fn ran in an unretained code version")
		return nil, nil
	}, RootOptions{CodeVersion: &seven})
	if !IsChordError(err, CodeSuperseded) || messageOf(err) != "superseded: code version 7 is not retained by s" {
		t.Fatalf("unretained code version: %v", err)
	}
}

// host.ts:491-509: a root's end is announced (root_end) on every link its calls went out on.
func TestRootEndAnnouncedOnLinksItUsed(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	connection, link := childConnection(t, host, "w")
	var rootID string
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		rootID = ctx.RootID()
		host.run(func() { host.rootSent(rootID, connection) })
		return nil, nil
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	ends := framesOf[RootEndFrame](link)
	if len(ends) != 1 || ends[0].RootID != rootID {
		t.Fatalf("root_end frames %v", ends)
	}
}

// host.ts:727-736: a host link needs its name.
func TestAcceptHostNeedsName(t *testing.T) {
	err := CreateHost(HostOptions{Self: kindHost()}).Accept(&fakeLink{}, AcceptOptions{Kind: KindHost})
	var typeError *TypeError
	if !errors.As(err, &typeError) || typeError.Message != "accepting a host link needs its name" {
		t.Fatalf("accept: %v", err)
	}
}

// host.ts:738-749: connect replaces an open upstream link, closing it as "replaced".
func TestConnectReplacesUpstream(t *testing.T) {
	first, second := &fakeLink{}, &fakeLink{}
	host := CreateHost(HostOptions{Self: kindHost(), Upstream: &UpstreamOptions{Link: first, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	host.Connect(second, ConnectOptions{Principal: kindServer()})
	if !first.Closed() || !slices.Equal(first.reasons, []string{"replaced"}) || second.Closed() {
		t.Fatalf("first closed %v %v, second closed %v", first.Closed(), first.reasons, second.Closed())
	}
	host.run(func() {
		if host.upstream == nil || host.upstream.link != second {
			t.Error("upstream is not the new link")
		}
	})
}

// host.ts:892-898: a host with a parent does not coordinate reloads.
func TestReloadRefusedUnderParent(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Upstream: &UpstreamOptions{Link: &fakeLink{}, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	_, err := host.Reload(nil, ReloadOptions{})
	var chordError *ChordError
	if !errors.As(err, &chordError) || chordError.Code != CodeUnavailable || chordError.Reason != "not_coordinator" ||
		chordError.Message != "unavailable/not_coordinator: reloads are coordinated by the server" {
		t.Fatalf("reload: %v", err)
	}
}

// host.ts:702-718: a pattern propagates to child hosts as an invalidate frame; a predicate stays on this host; clients
// never get it.
func TestInvalidatePrincipalPropagatesPatterns(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	_, child := childConnection(t, host, "w")
	client := &fakeLink{}
	if err := host.Accept(client, AcceptOptions{Principal: chordjson.ObjectOf("user", "u2")}); err != nil {
		t.Fatal(err)
	}
	if err := host.InvalidatePrincipal(PrincipalMatch{Predicate: func(Principal) bool { return false }}); err != nil {
		t.Fatal(err)
	}
	if frames := framesOf[InvalidateFrame](child); len(frames) != 0 {
		t.Fatalf("predicate propagated: %v", frames)
	}
	pattern := chordjson.ObjectOf("user", "u1")
	if err := host.InvalidatePrincipal(PrincipalMatch{Pattern: pattern}); err != nil {
		t.Fatal(err)
	}
	frames := framesOf[InvalidateFrame](child)
	if len(frames) != 1 || frames[0].Pattern != pattern {
		t.Fatalf("child frames %v", frames)
	}
	if frames := framesOf[InvalidateFrame](client); len(frames) != 0 {
		t.Fatalf("client got %v", frames)
	}
}

// host.ts:983-986: a pattern matches an object principal whose fields deep-equal the pattern's.
func TestMatchesPattern(t *testing.T) {
	pattern := chordjson.ObjectOf("user", "u1", "grant", map[string]any{"rev": 2.0})
	cases := []struct {
		principal Principal
		want      bool
	}{
		{chordjson.ObjectOf("user", "u1", "grant", chordjson.ObjectOf("rev", 2.0), "conn", "c1"), true},
		{map[string]any{"user": "u1", "grant": map[string]any{"rev": 2.0}}, true},
		{chordjson.ObjectOf("user", "u1", "grant", map[string]any{"rev": 3.0}), false},
		{chordjson.ObjectOf("user", "u1"), false},
		{nil, false},
		{"u1", false},
		{[]any{"u1"}, false},
	}
	for _, test := range cases {
		if got := matchesPattern(pattern, test.principal); got != test.want {
			t.Errorf("%v: got %v", test.principal, got)
		}
	}
	for _, principal := range []Principal{nil, "u1", 1.0, true} {
		if matchesPattern(chordjson.ObjectOf(), principal) {
			t.Errorf("the empty pattern matched the non-object %v", principal)
		}
	}
	if !matchesPattern(chordjson.ObjectOf("0", "x"), []any{"x"}) {
		t.Error("an array principal is an object with index fields")
	}
	if !matchesPattern(chordjson.ObjectOf("missing", nil), chordjson.ObjectOf()) {
		t.Error("an absent field deep-equals null (canonicalJson(undefined) is null)")
	}
}

// host.ts remoteKey (b45e0ebc): a remote binding's key is @type, the canonical input identity (fields equal to their
// defaults omitted), the principal part ("host" for the host's own principal) and the consumer's contract fingerprint: no
// host name, since the type and its input decide where the object lives.
func TestRemoteKey(t *testing.T) {
	typ := testContract("t.a")
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	input := map[string]any{"b": 1.0, "a": "x"}
	if got, want := host.remoteKey(typ, input, host.principal), "@t.a\x00"+CanonicalIdentity(typ.Input, input)+"\x00host\x00"+typ.Fingerprint; got != want {
		t.Fatalf("host principal: %q, want %q", got, want)
	}
	user := chordjson.ObjectOf("user", "u1")
	if got, want := host.remoteKey(typ, input, user), "@t.a\x00"+CanonicalIdentity(typ.Input, input)+"\x00p:"+CanonicalJSON(user)+"\x00"+typ.Fingerprint; got != want {
		t.Fatalf("user principal: %q, want %q", got, want)
	}
}

// host.ts:807-809: an unserved type is unavailable/unknown_type. A local contract has no placement (aa9cf15b: get takes
// no host), so it is looked up here.
func TestGetErrors(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	_, err := host.Hold(testContract("t.a"), map[string]any{}, nil)
	if !IsChordError(err, CodeUnavailable) || messageOf(err) != "unavailable/unknown_type: t.a is not served by host" {
		t.Fatalf("unknown type: %v", err)
	}
	local := Local("t.l", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"ping", Method(tObject(), tObject())}}})
	_, err = host.Hold(local, nil, nil)
	if messageOf(err) != "unavailable/unknown_type: t.l is not served by host" {
		t.Fatalf("local: %v", err)
	}
}

// host.ts:840-846: the longest matching prefix routes a host name.
func TestRouteForLongestPrefix(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	short := Route{Principal: func(string) Principal { return "short" }}
	long := Route{Principal: func(string) Principal { return "long" }}
	host.Route("session:", short)
	host.Route("session:admin", long)
	host.run(func() {
		if route, ok := host.routeFor("session:admin:1"); !ok || route.Principal("") != "long" {
			t.Error("longest prefix not chosen")
		}
		if route, ok := host.routeFor("session:1"); !ok || route.Principal("") != "short" {
			t.Error("shorter prefix not chosen")
		}
		if _, ok := host.routeFor("worker:1"); ok {
			t.Error("unmatched name routed")
		}
	})
}

// host.ts:911-925: remembered child modules accumulate per prefix without duplicates; removals tombstone modules and
// a later re-add clears the tombstone.
func TestRememberRemoteModules(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	host.run(func() {
		host.rememberRemoteModules(map[string][]string{"session:": {"a", "b", "a"}}, nil)
		host.rememberRemoteModules(map[string][]string{"session:": {"c"}}, map[string][]string{"session:": {"a"}})
		if got := host.remoteModules["session:"]; !slices.Equal(got, []string{"b", "c"}) {
			t.Errorf("modules %v", got)
		}
		if got := host.remoteRemoved["session:"]; !slices.Equal(got, []string{"a"}) {
			t.Errorf("removed %v", got)
		}
		host.rememberRemoteModules(map[string][]string{"session:": {"a"}}, nil)
		if got := host.remoteModules["session:"]; !slices.Equal(got, []string{"b", "c", "a"}) {
			t.Errorf("re-added %v", got)
		}
		if got := host.remoteRemoved["session:"]; len(got) != 0 {
			t.Errorf("tombstone kept %v", got)
		}
	})
}

// cluster.ts:17-23: a child gets the modules of the longest prefix of its name, or none.
func TestModulesForLongestPrefix(t *testing.T) {
	remote := map[string][]string{"session:": {"a"}, "session:admin": {"b"}}
	if got := modulesFor(remote, "session:admin:1"); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("admin %v", got)
	}
	if got := modulesFor(remote, "session:1"); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("session %v", got)
	}
	if got := modulesFor(remote, "worker"); got == nil || len(got) != 0 {
		t.Fatalf("unmatched %v", got)
	}
}

// cluster.ts:26-37: a missing vote rejects aborted/vote_deadline after the deadline and is forgotten.
func TestAwaitVoteDeadline(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	connection, _ := childConnection(t, host, "w")
	var vote *Promise[*WireReloadReport]
	host.run(func() { vote = awaitVote(host, connection, 9, 20*time.Millisecond) })
	_, err := vote.Await(t.Context())
	if !IsChordError(err, CodeAborted) || messageOf(err) != "aborted/vote_deadline: votes missing after 20 ms" {
		t.Fatalf("vote: %v", err)
	}
	host.run(func() {
		if _, ok := connection.votes[9]; ok {
			t.Error("expired vote still registered")
		}
	})
}

// cluster.ts:157-166: a child runs one reload at a time: a second prepare is voted down with unavailable/reload_running.
func TestChildRejectsSecondPrepare(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	connection, link := childConnection(t, host, "w")
	host.run(func() {
		_ = host.onReloadFrame(connection, PrepareFrame{Reload: 1, CodeVersion: 2, Plugins: []string{}})
		_ = host.onReloadFrame(connection, PrepareFrame{Reload: 2, CodeVersion: 2, Plugins: []string{}})
	})
	var second []VoteFrame
	for _, vote := range framesOf[VoteFrame](link) {
		if vote.Reload == 2 {
			second = append(second, vote)
		}
	}
	if len(second) != 1 || second[0].Error == nil || second[0].Error.Code != CodeUnavailable || second[0].Error.Reason != "reload_running" {
		t.Fatalf("second prepare votes %+v", second)
	}
	host.waitTasks()
}

// host.ts:765-781: a child ahead of this host is reported and closed; one in step is synced.
func TestPeerHello(t *testing.T) {
	var reported []error
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(err error) { reported = append(reported, err) }})
	ahead, aheadLink := childConnection(t, host, "w")
	host.run(func() { host.peerHello(ahead, 5) })
	if len(reported) != 1 || messageOf(reported[0]) != "invalid: w runs code version 5, ahead of 1" {
		t.Fatalf("reported %v", reported)
	}
	if !slices.Equal(aheadLink.reasons, []string{"code version ahead of parent"}) {
		t.Fatalf("close reasons %v", aheadLink.reasons)
	}
	inStep, _ := childConnection(t, host, "v")
	host.run(func() {
		host.peerHello(inStep, 1)
		if !inStep.synced {
			t.Error("a child in step is not synced")
		}
	})
}

// host.ts:585-606 and 675-694: a registry view reads the current table, a subscriber hears the current table at once
// and then every changed table of the current code version (all of them when all is set), and a retired code version's table is
// superseded.
func TestRegistryViewAndSubscribers(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	view := host.Registry(Registry[any]{Name: "tools", Conflict: "error"})
	type heard struct {
		keys        []string
		codeVersion int
	}
	var calls []heard
	unsubscribe := view.Subscribe(func(table RegistryTable[any], codeVersion int) {
		calls = append(calls, heard{keys: table.Keys(), codeVersion: codeVersion})
	})
	if len(calls) != 1 || len(calls[0].keys) != 0 || calls[0].codeVersion != 1 {
		t.Fatalf("initial %+v", calls)
	}
	value := &struct{}{}
	tables := map[string]RegistryTable[any]{"tools": NewRegistryTable([]string{"read"}, map[string]any{"read": value})}
	host.run(func() { host.installTables(host.current, tables, false) })
	if len(calls) != 2 || !slices.Equal(calls[1].keys, []string{"read"}) {
		t.Fatalf("after change %+v", calls)
	}
	if got, ok := view.Get("read", nil); !ok || got != value {
		t.Fatalf("get %v %v", got, ok)
	}
	same := map[string]RegistryTable[any]{"tools": NewRegistryTable([]string{"read"}, map[string]any{"read": value})}
	host.run(func() { host.installTables(host.current, same, false) })
	if len(calls) != 2 {
		t.Fatalf("unchanged table notified: %+v", calls)
	}
	host.run(func() { host.installTables(host.current, same, true) })
	if len(calls) != 3 {
		t.Fatalf("all did not notify: %+v", calls)
	}
	unsubscribe()
	host.run(func() { host.installTables(host.current, map[string]RegistryTable[any]{}, false) })
	if len(calls) != 3 {
		t.Fatal("unsubscribed listener notified")
	}
	defer func() {
		if !IsChordError(recoveredError(recover()), CodeSuperseded) {
			t.Error("a retired code version's table is not superseded")
		}
	}()
	view.Table(&CtxImpl{codeVersion: 4})
}

// host.ts:977-981: tables are the same when keys, order and values (===) are.
func TestSameTable(t *testing.T) {
	shared := map[string]any{"x": 1.0}
	a := NewRegistryTable([]string{"k", "l"}, map[string]any{"k": shared, "l": "s"})
	if !sameTable(a, NewRegistryTable([]string{"k", "l"}, map[string]any{"k": shared, "l": "s"})) {
		t.Fatal("identical tables differ")
	}
	if sameTable(a, NewRegistryTable([]string{"k", "l"}, map[string]any{"k": map[string]any{"x": 1.0}, "l": "s"})) {
		t.Fatal("an equal but distinct object is ===")
	}
	if sameTable(a, NewRegistryTable([]string{"l", "k"}, map[string]any{"k": shared, "l": "s"})) {
		t.Fatal("order is ignored")
	}
	if !sameTable(RegistryTable[any]{}, NewRegistryTable(nil, nil)) {
		t.Fatal("empty tables differ")
	}
}

// host.ts:936-944: close disposes objects and closes every link as "host closed".
func TestCloseClosesLinks(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	_, child := childConnection(t, host, "w")
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(child.reasons, []string{"host closed"}) {
		t.Fatalf("close reasons %v", child.reasons)
	}
}

// The loop runs nested sections inline (a factory's ctx.get inside acquire) and queued callbacks after the section, in
// order, as upstream's synchronous calls and promise reactions run.
func TestLoopOrdering(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	var order []string
	host.run(func() {
		host.post(func() { order = append(order, "posted 1") })
		host.run(func() { order = append(order, "nested") })
		host.post(func() { order = append(order, "posted 2") })
		order = append(order, "section end")
	})
	if !slices.Equal(order, []string{"nested", "section end", "posted 1", "posted 2"}) {
		t.Fatalf("order %v", order)
	}
	first := errors.New("first")
	a, _, rejectA := newPromise[struct{}](&host.loop)
	b, _, rejectB := newPromise[struct{}](&host.loop)
	all := promiseAll(&host.loop, []*Promise[struct{}]{a, b})
	rejectB(first)
	rejectA(errors.New("second"))
	if _, err := all.Await(t.Context()); !errors.Is(err, first) {
		t.Fatalf("Promise.all rejection: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("awaiting on the loop did not panic")
		}
	}()
	host.run(func() { _, _ = all.Await(t.Context()) })
}

// host.ts: a type served with lifetime "host" starts the one object for the empty input and the host principal when
// served, and ready awaits it; a type whose input does not accept {} cannot be of lifetime "host".
func TestServeLifetimeHostStartsHostObject(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	hosted := Local("t.e", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{}})
	if err := host.Serve(hosted, noFactory, ServeOptions{Lifetime: LifetimeHost}); err != nil {
		t.Fatal(err)
	}
	host.run(func() {
		if !host.hostObjects.Has("t.e") || !slices.Equal(host.objects.Keys(), []string{"t.e\x00{}\x00host"}) {
			t.Errorf("host objects %v objects %q", host.hostObjects.Keys(), host.objects.Keys())
		}
	})
	keyed := Local("t.k", LocalDef{Version: 1, Input: tObject("id", tString()), Members: Fields[any]{}})
	if err := host.Serve(keyed, noFactory, ServeOptions{Lifetime: LifetimeHost}); err == nil || !strings.Contains(err.Error(), "input that accepts {}") {
		t.Fatalf("serve keyed with lifetime host: %v", err)
	}
	if err := host.Ready(); err != nil {
		t.Fatal(err)
	}
}

// host.ts:233-236 and 365-377: an object's address is the type, the canonical identity of the normalized input and the
// principal's key: "host" for the host principal, "p:" + principalKey (default canonicalJson) for any other.
func TestAddressesUsePrincipalKey(t *testing.T) {
	a := testContract("t.a")
	user := chordjson.ObjectOf("user", "u1", "conn", "c1")
	for _, test := range []struct {
		options HostOptions
		want    string
		// remotePrincipal is the principal part of a remote key for the user.
		remotePrincipal string
	}{
		{HostOptions{Self: chordjson.ObjectOf("user", "self")}, "t.a\x00{}\x00p:{\"conn\":\"c1\",\"user\":\"u1\"}", "p:{\"conn\":\"c1\",\"user\":\"u1\"}"},
		{HostOptions{Self: chordjson.ObjectOf("user", "self"), PrincipalKey: func(principal Principal) string {
			value, _ := principal.(*chordjson.Object).Get("user")
			return value.(string)
		}}, "t.a\x00{}\x00p:u1", "p:u1"},
	} {
		host := CreateHost(test.options)
		if err := host.Serve(a, noFactory, ServeOptions{}); err != nil {
			t.Fatal(err)
		}
		host.run(func() {
			got, err := host.addressOf(a, nil, user, false)
			if err != nil || got != test.want {
				t.Errorf("address %q %v, want %q", got, err, test.want)
			}
			if got, _ := host.addressOf(a, map[string]any{}, host.principal, false); got != "t.a\x00{}\x00host" {
				t.Errorf("host address %q", got)
			}
			// A type placed on another host has a remote key: no host name, the canonical input, the principal part and the
			// consumer's fingerprint.
			elsewhere := Remote("t.w", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("w"), Variants: Fields[VariantDef]{{"", VariantDef{Methods: Fields[MethodSpec]{{"ping", Method(tObject(), tObject())}}}}}})
			if got, _ := host.addressOf(elsewhere, nil, user, false); got != "@t.w\x00{}\x00"+test.remotePrincipal+"\x00"+elsewhere.Fingerprint {
				t.Errorf("remote address %q", got)
			}
		})
	}
}

// host.ts:128-133 and 471-476: a root's ctx carries the given signal.
func TestRootSignal(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	signal, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("stop")
	cancel(cause)
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		if !errors.Is(context.Cause(ctx.Signal()), cause) {
			t.Errorf("signal cause %v", context.Cause(ctx.Signal()))
		}
		return nil, nil
	}, RootOptions{Signal: signal}); err != nil {
		t.Fatal(err)
	}
}

// host.ts:848-872: a route authorizes before resolving and again after; a refusal is forbidden, and a request no
// longer current is gone.
func TestResolveRouteAuthorization(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	principal := chordjson.ObjectOf("user", "u1")
	var authorized []string
	resolved := 0
	decisions := []bool{true, false}
	route := Route{
		Authorize: func(who Principal, hostName string) (bool, error) {
			if who != principal {
				t.Errorf("authorized %v", who)
			}
			authorized = append(authorized, hostName)
			decision := decisions[0]
			decisions = decisions[1:]
			return decision, nil
		},
		Resolve: func(string) (Link, error) {
			resolved++
			return &fakeLink{}, nil
		},
	}
	// resolveRoute starts on the loop and settles a promise (its continuations come back to the loop).
	resolve := func(still func() bool) (*Connection, error) {
		var promise *Promise[*Connection]
		host.run(func() { promise = host.resolveRoute(route, "w:1", principal, still) })
		return promise.Await(context.Background())
	}
	_, err := resolve(nil)
	if !IsChordError(err, CodeForbidden) || messageOf(err) != "forbidden: w:1 refused for this principal" {
		t.Fatalf("second refusal: %v", err)
	}
	if !slices.Equal(authorized, []string{"w:1", "w:1"}) || resolved != 1 {
		t.Fatalf("authorized %v resolved %d", authorized, resolved)
	}
	decisions = []bool{false}
	if _, err := resolve(nil); !IsChordError(err, CodeForbidden) || resolved != 1 {
		t.Fatalf("first refusal: %v, resolved %d", err, resolved)
	}
	decisions = []bool{true}
	_, err = resolve(func() bool { return false })
	if !IsChordError(err, CodeGone) || messageOf(err) != "gone: principal invalidated while resolving" || resolved != 1 {
		t.Fatalf("revoked: %v, resolved %d", err, resolved)
	}
	host.run(func() {
		if host.connections.Len() != 0 {
			t.Errorf("refused routes added %d connections", host.connections.Len())
		}
	})
}

// host.ts:892-898: a client's facet code versions never cross the parent link, so a client reloads under a parent.
func TestClientReloadsUnderParent(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Kind: KindClient, Upstream: &UpstreamOptions{Link: &fakeLink{}, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	_, err := host.Reload(nil, ReloadOptions{})
	var chordError *ChordError
	if errors.As(err, &chordError) && chordError.Reason == "not_coordinator" {
		t.Fatalf("client reload refused: %v", err)
	}
}

// host.ts:58-71 and 318-325: a facet may keep its api and use it after setup: Root runs on the facet's host in its
// current code version, and Registry is the host's view.
func TestFacetApiRootAndRegistry(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Name: "s"})
	var kept FacetApi
	if err := host.Install(Facet{Name: "f", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		kept = api
		return nil
	}}}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ran := false
	value, err := kept.Root(func(ctx Ctx) (any, error) {
		ran = true
		if !strings.HasPrefix(ctx.RootID(), "s#") || ctx.CodeVersion() != host.CodeVersion() {
			t.Errorf("root %q code version %d", ctx.RootID(), ctx.CodeVersion())
		}
		return "done", nil
	}, RootOptions{})
	if err != nil || value != "done" || !ran {
		t.Fatalf("root %v %v ran %v", value, err, ran)
	}
	marker := &struct{}{}
	host.run(func() {
		host.installTables(host.current, map[string]RegistryTable[any]{"tools": NewRegistryTable([]string{"k"}, map[string]any{"k": marker})}, false)
	})
	if got, ok := kept.Registry(Registry[any]{Name: "tools", Conflict: "error"}).Get("k", nil); !ok || got != marker {
		t.Fatalf("registry %v %v", got, ok)
	}
}
