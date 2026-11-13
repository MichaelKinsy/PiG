package host

// pi: packages/chord/test/examples/32-startup.ts

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 32-startup.ts -------------------------------------------------------------------------------------------------

func TestExample32Startup(t *testing.T) {
	// On the server, a host-lifetime object (local, empty params): it starts at `load`.
	bootstrap := exLocal("example.bootstrap", contracts.Params{}, contracts.Mem("agents", exInt()))
	// On the session's worker.
	agent := exRemote("example.agent", contracts.Params{exProp("session", exStr())}, exSessionAddress, contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("models", exInt())},
	}))
	// On the server.
	catalog := exRemote("example.catalog", contracts.Params{}, exServerAddress, contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("models", exInt())},
	}))
	errs := &hostErrors{}
	// startWorker is the worker of a session, started by the server's route; it connects first, then loads (W6).
	type started struct {
		worker Host
		link   *link.MemoryLink
	}
	var workers []started
	var workersMu sync.Mutex
	workerLoaded := make(chan struct{})
	var workerLoadedOnce sync.Once
	// W6: the worker connects first, then loads. Pi's load of the worker takes more microtask turns than the check that
	// Bootstrap is `loading` after the server's load, which a goroutine loading at once does not (a delayed test goroutine finds
	// Bootstrap bound already): the worker loads when the check is done.
	workerLoadGate := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(workerLoadGate) }) }
	defer releaseWorker()
	startWorker := func(id string) started {
		up, down := link.MemoryLinkPair()
		address := Session.Address(jsonObject("id", id))
		worker := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: errs.push})
		must(t, worker.Connect(down, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
		agents := DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(agent, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				// The worker's Agent declares the server's Catalog.
				return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{
					"models": Expose(self.Get(catalog, jsonObject()).Member("models")),
				}, nil), nil
			}, ServeOptions{})
		})
		go func() {
			<-workerLoadGate
			_, _ = worker.Load([]HostFacet{{Name: "@example/agents", Facet: agents}}, background)
			workerLoadedOnce.Do(func() { close(workerLoaded) })
		}()
		return started{worker, up}
	}
	// No readyTimeout is needed to break anything: a long one shows nothing waits for it.
	readyTimeout := 600 * time.Second
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), ReadyTimeout: &readyTimeout, OnError: errs.push})
	mustRoute(t)(server.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			id, _ := address.Params.Value("id").(string)
			made := startWorker(id)
			workersMu.Lock()
			workers = append(workers, made)
			workersMu.Unlock()
			return ResolvedLink{Link: made.link, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	core := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(catalog, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{"models": exConstant(float64(3))}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(bootstrap, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			// The server's Bootstrap declares the worker's Agent.
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"agents": Expose(self.Get(agent, jsonObject("session", "S1")).Member("models")),
			}, nil), nil
		}, ServeOptions{})
	})
	// 1. The server's load: publication (Catalog served), then Bootstrap starts and parks on the worker's Agent. A wait on
	// another host is a first outcome, so `load` resolves without waiting for the worker. The gate holds the worker's load
	// until the read below, so a `load` that did wait for the worker (a remote wait not ending the first pass) cannot
	// return: that is this assertion failing, and it is reported after a bound a passing run is nowhere near (a load of
	// this host takes milliseconds), as `WorkerLoaded` does.
	type loadResult struct {
		report LoadReport
		err    error
	}
	loaded := make(chan loadResult, 1)
	go func() {
		report, err := server.Load([]HostFacet{{Name: "@example/core", Facet: core}}, background)
		loaded <- loadResult{report, err}
	}()
	select {
	case done := <-loaded:
		mustLoad(t)(done.report, done.err)
	case <-time.After(10 * time.Second):
		t.Fatalf("server.Load did not return while the worker's load was held: it waits for the worker, want it resolved at Bootstrap's first outcome")
	}
	if got := exStateNow(t, exHold(t, server, bootstrap, jsonObject())); got != "loading" {
		t.Fatalf("server loaded; Bootstrap is %s, want loading", got)
	}
	releaseWorker()
	// 2. The worker's hold for Catalog bound on the published server; Agent went live, then Bootstrap.
	reference := exGet(t, server, bootstrap, jsonObject())
	exEqual(t, "Bootstrap sees models through the worker", exValue(t, reference, "agents"), float64(3))
	<-workerLoaded
	reference.Release()
	exCloseHost(t, server)
	workersMu.Lock()
	defer workersMu.Unlock()
	for _, made := range workers {
		exCloseHost(t, made.worker)
	}
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
