package host

// Ports packages/chord/test/hydration.test.ts (Pi b45e0ebc): a local object goes live only with its state, also where it
// forwards or derives from a remote reference's member; activation waits at most ReadyTimeout (a cycle across hosts fails
// with a hint instead of hanging).

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// hydrationSource lives on the worker w; its value is set by set.
var hydrationSource = Remote("t.e8-source", RemoteDef{
	Version: 1, Input: tObject(), Host: NewHostNamed("w"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"v", tString()}},
		Methods: Fields[MethodSpec]{{"set", Method(tString(), nil)}},
	}}},
})

// hydrationForward lives on the server; it forwards (and derives from) the worker's Source.
var hydrationForward = Remote("t.e8-forward", RemoteDef{
	Version: 1, Input: tObject("how", tString()), Host: NewHostNamed("server"),
	Variants: Fields[VariantDef]{{"main", VariantDef{State: Fields[Schema]{{"v", tString()}, {"size", tNumber()}}}}},
})

func serveHydrationSource(t *testing.T, host *HostImpl) {
	t.Helper()
	requestsServe(t, host.Serve, hydrationSource, func(_ any, _ Principal, self Self) (Implementation, error) {
		v, err := self.State("v", tString(), "from the worker", nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "v", v, "set", methodFunc(func(text any, ctx Ctx) (any, error) {
			return nil, v.Replace(ctx, text, CellReplaceOptions{})
		})), nil
	}, ServeOptions{})
}

type hydrationCluster struct {
	server, worker *HostImpl
	browser        func() *HostImpl
}

// hydrationClusterOf is a server with a worker child w (accepted after delay, as a server admits a worker once it is
// ready).
func hydrationClusterOf(t *testing.T, delay time.Duration, readyTimeout *time.Duration) hydrationCluster {
	t.Helper()
	server := requestsHost(t, HostOptions{Name: "server", ReadyTimeout: readyTimeout})
	down, up := MemoryLinkPair()
	worker := requestsHost(t, HostOptions{Name: "w", ReadyTimeout: readyTimeout, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	admit := func() {
		if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"kind": "worker"}, Kind: KindHost, Name: new("w")}); err != nil {
			t.Error(err)
		}
	}
	if delay == 0 {
		admit()
	} else {
		timer := time.AfterFunc(delay, admit)
		t.Cleanup(func() { timer.Stop() })
	}
	return hydrationCluster{server: server, worker: worker, browser: func() *HostImpl {
		down2, up2 := MemoryLinkPair()
		if err := server.Accept(down2, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
			t.Fatal(err)
		}
		return requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up2, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	}}
}

// hydration.test.ts:78-97 "an object that forwards and derives from a remote member goes live with its value"
func TestHydrationAnObjectThatForwardsAndDerivesFromARemoteMemberGoesLiveWithItsValue(t *testing.T) {
	c := hydrationClusterOf(t, 0, nil)
	serveHydrationSource(t, c.worker)
	// Held outside the factory (no self.Get): activation still waits for its snapshot.
	held, err := c.server.Hold(hydrationSource, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestsServe(t, c.server.Serve, hydrationForward, func(input any, _ Principal, self Self) (Implementation, error) {
		source := held
		if inputField(input, "how") == "declared" {
			var err error
			if source, err = self.Get(hydrationSource, map[string]any{}, nil); err != nil {
				return nil, err
			}
		}
		v := source.Member("v").(Readable[any])
		size, err := self.Derive(Sources{v}, func(values ...any) any { return float64(len(values[0].(string))) })
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "v", v, "size", size), nil
	}, ServeOptions{})
	browser := c.browser()
	for _, how := range []string{"held", "declared"} {
		forward, err := browser.Hold(hydrationForward, map[string]any{"how": how}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := readyErr(t, forward); err != nil {
			t.Fatalf("%s: %v", how, err)
		}
		if got := requestsReadable(t, forward, "v").Value(); got != "from the worker" {
			t.Fatalf("%s: v = %v", how, got)
		}
		if got := requestsReadable(t, forward, "size").Value(); got != float64(len("from the worker")) {
			t.Fatalf("%s: size = %v", how, got)
		}
		local, err := c.server.Hold(hydrationForward, map[string]any{"how": how}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := readyErr(t, local); err != nil {
			t.Fatalf("%s local: %v", how, err)
		}
		if got := requestsReadable(t, local, "v").Value(); got != "from the worker" {
			t.Fatalf("%s local: v = %v", how, got)
		}
	}
}

// hydration.test.ts:99-118 "a member read inside a factory throws not_ready; it is a plain value once live"
func TestHydrationAMemberReadInsideAFactoryThrowsNotReadyItIsAPlainValueOnceLive(t *testing.T) {
	host := quiet(t, nil)
	inner := Local("t.e8-inner", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"n", tNumber()}}})
	outer := Local("t.e8-outer", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"m", tNumber()}}})
	mustServe(t, host, inner, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(7), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n), nil
	}, ServeOptions{})
	var seen []any
	mustServe(t, host, outer, func(_ any, _ Principal, self Self) (Implementation, error) {
		held, err := self.Get(inner, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		n := held.Member("n").(Readable[any])
		if thrown := chordPanic(func() { _ = n.Value() }); thrown != nil && thrown.Code == CodeNotReady {
			seen = append(seen, "not_ready")
		} else {
			seen = append(seen, thrown)
		}
		m, err := self.Derive(Sources{n}, func(values ...any) any { return values[0].(float64) * 2 })
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("m", m), nil
	}, ServeOptions{})
	ref := mustRef(t, host, outer, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "not_ready" {
		t.Fatalf("seen = %v, want [not_ready]", seen)
	}
	if got := readable(t, ref, "m").Value(); got != float64(14) {
		t.Fatalf("m = %v, want 14", got)
	}
}

// hydration.test.ts:120-151 "a host object of a child holding the parent's object goes live once the parent admits the
// child"
func TestHydrationAHostObjectOfAChildHoldingTheParentsObjectGoesLiveOnceTheParentAdmitsTheChild(t *testing.T) {
	settings := Remote("t.e8-settings", RemoteDef{
		Version: 1, Input: tObject(), Host: NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"main", VariantDef{State: Fields[Schema]{{"theme", tString()}}}}},
	})
	boot := Local("t.e8-boot", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"theme", tString()}}})
	c := hydrationClusterOf(t, 20*time.Millisecond, nil)
	requestsServe(t, c.server.Serve, settings, func(_ any, _ Principal, self Self) (Implementation, error) {
		theme, err := self.State("theme", tString(), "dark", nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "theme", theme), nil
	}, ServeOptions{})
	facet := Facet{Name: "boot", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(boot, func(_ any, _ Principal, self Self) (Implementation, error) {
			held, err := self.Get(settings, map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			return chordjson.ObjectOf("theme", held.Member("theme")), nil
		}, ServeOptions{Lifetime: LifetimeHost})
	}}}
	if err := c.worker.Install(facet, LoadOptions{AwaitHostObjects: new(false)}); err != nil {
		t.Fatal(err)
	}
	if err := c.worker.Ready(); err != nil {
		t.Fatal(err)
	}
	ref, err := c.worker.Hold(boot, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if got := requestsReadable(t, ref, "theme").Value(); got != "dark" {
		t.Fatalf("theme = %v, want dark", got)
	}
}

// hydration.test.ts:153-195 "a reference cycle across hosts fails at readyTimeout with a cycle hint"
func TestHydrationAReferenceCycleAcrossHostsFailsAtReadyTimeoutWithACycleHint(t *testing.T) {
	cycleA := Remote("t.e8-cycle-a", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{}}}})
	cycleB := Remote("t.e8-cycle-b", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("w"), Variants: Fields[VariantDef]{{"main", VariantDef{}}}})
	timeout := 50 * time.Millisecond
	c := hydrationClusterOf(t, 0, &timeout)
	requestsServe(t, c.server.Serve, cycleA, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Get(cycleB, map[string]any{}, nil); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main"), nil
	}, ServeOptions{})
	requestsServe(t, c.worker.Serve, cycleB, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Get(cycleA, map[string]any{}, nil); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main"), nil
	}, ServeOptions{})
	a, err := c.server.Hold(cycleA, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = readyErr(t, a)
	var chordError *ChordError
	if !IsChordError(err, CodeUnavailable) || !errors.As(err, &chordError) || chordError.Reason != "ready_timeout" || !regexp.MustCompile(`cycle`).MatchString(chordError.Message) {
		t.Fatalf("cycle: %v", err)
	}
}

// A reload's prepare waits for what the changed object forwards or derives from (reload.ts prepareObject:
// `await object.waitHydrated(run)`); Pi's tests do not reach it, so this is a regression guard of the Go port: the second
// version of the object forwards a reference whose host is accepted but has not connected yet, so the member has no
// snapshot when prepare starts.
func TestHydrationAReloadPreparesAfterWhatTheChangedObjectForwardsHasItsValue(t *testing.T) {
	second := Remote("t.e8-source2", RemoteDef{
		Version: 1, Input: tObject(), Host: NewHostNamed("w2"),
		Variants: Fields[VariantDef]{{"main", VariantDef{State: Fields[Schema]{{"v", tString()}}}}},
	})
	box := Local("t.e8-reload-forward", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"v", tString()}, {"size", tNumber()}}})
	server := requestsHost(t, HostOptions{Name: "server"})
	facet := func(source Ref) *Facet {
		return &Facet{Name: "forward", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(box, func(_ any, _ Principal, self Self) (Implementation, error) {
				v := source.Member("v").(Readable[any])
				size, err := self.Derive(Sources{v}, func(values ...any) any { return float64(len(values[0].(string))) })
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("v", v, "size", size), nil
			}, ServeOptions{})
		}}}
	}
	// The first version forwards a connected worker's member.
	worker := requestsHost(t, HostOptions{Name: "w"})
	down, up := MemoryLinkPair()
	worker.Connect(up, ConnectOptions{Principal: kindServer()})
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"kind": "worker"}, Kind: KindHost, Name: new("w")}); err != nil {
		t.Fatal(err)
	}
	serveHydrationSource(t, worker)
	first, err := server.Hold(hydrationSource, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reloadInstall(t, server, facet(first))
	ref := mustRef(t, server, box, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}

	// The second version forwards a member whose object on another host goes live only after prepare started.
	gate := newRequestsLatch()
	t.Cleanup(gate.open)
	worker2 := requestsHost(t, HostOptions{Name: "w2"})
	down2, up2 := MemoryLinkPair()
	worker2.Connect(up2, ConnectOptions{Principal: kindServer()})
	if err := server.Accept(down2, AcceptOptions{Principal: map[string]any{"kind": "worker"}, Kind: KindHost, Name: new("w2")}); err != nil {
		t.Fatal(err)
	}
	requestsServe(t, worker2.Serve, second, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Resource("gate", ResourceDeclaration[any]{Version: 1, Open: func(options ResourceOpenOptions) (any, error) {
			select {
			case <-gate.ch:
			case <-options.Signal.Done():
			}
			return nil, nil
		}}); err != nil {
			return nil, err
		}
		v, err := self.State("v", tString(), "from the second worker", nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "v", v), nil
	}, ServeOptions{})
	// A hello that arrives while a reload runs is handled after it: the child is in step before the reload starts.
	waitFor(t, "w2 to be in step", func() bool {
		synced := false
		server.run(func() { synced = server.childNamed("w2") != nil && server.childNamed("w2").synced })
		return synced
	})
	held, err := server.Hold(second, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(30*time.Millisecond, gate.open)
	if _, err := server.Reload(reloadPlugins(facet(held)), ReloadOptions{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := readable(t, ref, "v").Value(); got != "from the second worker" {
		t.Fatalf("v after the reload = %v", got)
	}
	if got := readable(t, ref, "size").Value(); got != float64(len("from the second worker")) {
		t.Fatalf("size after the reload = %v", got)
	}
}
