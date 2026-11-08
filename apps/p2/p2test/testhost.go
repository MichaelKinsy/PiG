package p2test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Cell is a stub's in-memory state member (v1's `self.state`): the member itself plus Replace and Change for the test to
// drive it, as Pi's test helper `state` is a Writable (`self.writable` and its `set`). Set enters a section of the host's
// loop whichever goroutine calls it.
type Cell struct {
	writable state.Writable[any]
	member   chord.Watchable[delta.JsonValue]
}

// Value is the cell's current value.
func (c *Cell) Value() any { return c.writable.Value() }

// Replace sets the value.
func (c *Cell) Replace(value any) error { return c.writable.Set(value) }

// Change sets the value fn makes of the current one (fn must not mutate it).
func (c *Cell) Change(fn func(current any) any) error { return c.writable.Set(fn(c.writable.Value())) }

// StubSelf is what a stub factory gets: its object's Self, plus Cell for in-memory state members.
type StubSelf struct {
	host.Self
}

// Cell is an in-memory state member holding initial (state members must be JSON watchables).
func (s StubSelf) Cell(initial any) *Cell {
	writable := s.Self.Writable(initial)
	member := s.Self.Derive([]any{writable}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
	return &Cell{writable: writable, member: member}
}

// StubMethod is a stub's method: it may return nil for nothing.
type StubMethod func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error)

// StubMembers are a stub object's members, flat: Variant for a remote type; State members are a *Cell, a Chord watchable,
// or a constant JSON value.
type StubMembers struct {
	Variant string
	State   map[string]any
	Methods map[string]StubMethod
}

// Stub is a contract plus its members, or a factory of them (a dependency a facet under test needs).
type Stub struct {
	Contract *contracts.Contract
	// Factory makes the members of the object for params; principal is the caller of a remote type (the host's own for a
	// local one).
	Factory func(params *delta.JsonObject, principal p2runtime.Principal, self StubSelf) StubMembers
}

func (stub Stub) implementation(params *delta.JsonObject, principal p2runtime.Principal, self host.Self) (contracts.Implementation, error) {
	members := stub.Factory(params, principal, StubSelf{self})
	implementation := contracts.Implementation{Variant: members.Variant}
	implementation.Members.State = map[string]chord.Watchable[delta.JsonValue]{}
	for name, value := range members.State {
		switch typed := value.(type) {
		case *Cell:
			implementation.Members.State[name] = typed.member
		case chord.Watchable[delta.JsonValue]:
			implementation.Members.State[name] = typed
		default:
			implementation.Members.State[name] = host.Expose(state.NewConstant(typed))
		}
	}
	implementation.Members.Methods = map[string]contracts.MethodFunction{}
	for name, method := range members.Methods {
		implementation.Members.Methods[name] = func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return method(args, ctx)
		}
	}
	return implementation, nil
}

// serve serves the stub on a host: a local type's factory gets (params, self), a remote one's (params, principal, self).
func (stub Stub) serve(facet host.FacetHost) error {
	if stub.Contract.Kind == contracts.KindRemote {
		return facet.Serve(stub.Contract, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
			who, err := p2runtime.FromChord(principal)
			if err != nil {
				return contracts.Implementation{}, err
			}
			return stub.implementation(params, who, self)
		}), host.ServeOptions{})
	}
	return facet.Serve(stub.Contract, host.LocalFactory(func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		return stub.implementation(params, p2runtime.Host, self)
	}), host.ServeOptions{})
}

// WorkerFacet is a facet that serves the worker natives and contributes a stub Harness and worker process for a bare test
// host (`kind: "worker"`): the session's Harness (Harness) and the worker process (Worker). Unset parts are inert: no
// location moves, liveness counts nothing, roots run on the host given.
func WorkerFacet(harness *p2runtime.HarnessParts, worker *p2runtime.WorkerParts, rootHost func() host.Host) host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		if err := p2runtime.ServeWorkerNatives(facet); err != nil {
			return err
		}
		if err := facet.Serve(p2runtime.HarnessHost, host.LocalFactory(func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, nil
		}), host.ServeOptions{}); err != nil {
			return err
		}
		if err := p2runtime.ContributeHarness(self, harness); err != nil {
			return err
		}
		parts := &p2runtime.WorkerParts{
			Liveness: p2runtime.NewLiveness(),
			Location: p2runtime.NewSessionLocation(p2runtime.Location{Cwd: "/"}),
			Root: func(fn func(host.Ctx) (any, error), ctx context.Context) (any, error) {
				if rootHost == nil {
					return nil, fmt.Errorf("no root in this test worker")
				}
				return rootHost().Root(fn, ctx, host.RootOptions{})
			},
			CopyFilesTo: func(context.Context, string) error { return fmt.Errorf("no files in this test worker") },
		}
		if worker != nil {
			if worker.Liveness != nil {
				parts.Liveness = worker.Liveness
			}
			if worker.Location != nil {
				parts.Location = worker.Location
			}
			if worker.Root != nil {
				parts.Root = worker.Root
			}
			if worker.CopyFilesTo != nil {
				parts.CopyFilesTo = worker.CopyFilesTo
			}
			parts.Init = worker.Init
		}
		return p2runtime.ContributeWorkerProcess(self, parts)
	})
}

// TestHostOptions is what NewTestHost takes.
type TestHostOptions struct {
	// Address is where the host is (the server by default).
	Address *contracts.Address
	// Worker makes a worker host (kind "worker") instead of the server's.
	Worker bool
	// Principal is whom Root acts as.
	Principal p2runtime.Principal
	Facets    []host.Facet
	Stubs     []Stub
	// BeforeLoad runs before Load: routes (`host.Route`) are declared there.
	BeforeLoad func(host.Host)
	// Store is the host's store, for facets that persist state or jobs (a test opens one with runtime.OpenHostStore on
	// a temporary directory and closes it after the host).
	Store store.Store
	// Server overrides the server's natives (a server host): no workers and no resource options unless given.
	Server *p2runtime.ServerParts
}

// TestHost is one or more facets without processes, HTTP, SQLite or a provider, on a host at an address with the base
// registries of its kind. Install only the stubs these facets need.
type TestHost struct {
	Host   host.Host
	t      testing.TB
	opts   TestHostOptions
	base   host.Facet
	mu     sync.Mutex
	errors []host.HostError
	// clients are closed with the host.
	clients []host.Host
}

// Errors are the errors the host and its clients reported.
func (h *TestHost) Errors() []host.HostError {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]host.HostError(nil), h.errors...)
}

func (h *TestHost) record(failure host.HostError) {
	h.mu.Lock()
	h.errors = append(h.errors, failure)
	h.mu.Unlock()
}

// NewTestHost makes the host and loads its facets; a facet that fails fails the test. It closes with the test.
func NewTestHost(t testing.TB, options TestHostOptions) *TestHost {
	t.Helper()
	address := options.Address
	if address == nil {
		server := p2runtime.ServerKind.Address(delta.NewJsonObject(0))
		address = &server
	}
	principal := p2runtime.Server
	if options.Worker {
		principal = p2runtime.Host
	}
	testHost := &TestHost{t: t, opts: options}
	created, err := p2runtime.NewHost(host.HostOptions{Address: address, Principal: p2runtime.ToChord(principal), OnError: reportTo(testHost.record), Store: options.Store})
	if err != nil {
		t.Fatal(err)
	}
	testHost.Host = created
	base := host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		if options.Worker {
			if err := p2runtime.ServeWorkerRegistries(facet); err != nil {
				return err
			}
		} else {
			if err := p2runtime.ServeServerRegistries(facet); err != nil {
				return err
			}
			if err := p2runtime.ServeServerNatives(facet); err != nil {
				return err
			}
			parts := &p2runtime.ServerParts{
				Workers: p2runtime.NoWorkers{},
				Root: func(fn func(host.Ctx) (any, error), ctx context.Context) (any, error) {
					return created.Root(fn, ctx, host.RootOptions{})
				},
			}
			if options.Server != nil {
				if options.Server.Workers != nil {
					parts.Workers = options.Server.Workers
				}
				if options.Server.Root != nil {
					parts.Root = options.Server.Root
				}
				parts.ResourceOptions = options.Server.ResourceOptions
			}
			if err := p2runtime.ContributeServerNatives(self, parts); err != nil {
				return err
			}
		}
		for _, stub := range options.Stubs {
			if err := stub.serve(facet); err != nil {
				return err
			}
		}
		return nil
	})
	testHost.base = base
	if options.BeforeLoad != nil {
		options.BeforeLoad(created)
	}
	if err := testHost.load(created, base, options.Facets); err != nil {
		_ = created.Close(Background)
		t.Fatal(err)
	}
	t.Cleanup(testHost.Close)
	return testHost
}

func plugins(base host.Facet, facets []host.Facet) []host.HostFacet {
	loaded := []host.HostFacet{{Name: "@p2/test-base", Facet: base}}
	for i, facet := range facets {
		loaded = append(loaded, host.HostFacet{Name: fmt.Sprintf("test-facet-%d", i), Facet: facet})
	}
	return loaded
}

func (h *TestHost) load(created host.Host, base host.Facet, facets []host.Facet) error {
	report, err := created.Load(plugins(base, facets), Background)
	if err == nil && len(report.Failed) > 0 {
		err = report.Failed[0].Error
	}
	return err
}

// Root runs fn as the host's own code, acting as the options' Principal (the host's when none).
func (h *TestHost) Root(fn func(ctx host.Ctx) (any, error)) (any, error) {
	rootOptions := host.RootOptions{}
	if h.opts.Principal != nil {
		rootOptions.Principal = p2runtime.ToChord(h.opts.Principal)
	}
	return h.Host.Root(fn, Background, rootOptions)
}

// Client is a memory-linked client whose principal is stamped by the bare host, just as by the server, once it loaded
// facets (TUI facets, say).
func (h *TestHost) Client(principal p2runtime.Principal, facets ...host.HostFacet) host.Host {
	h.t.Helper()
	down, up := link.MemoryLinkPair()
	if err := h.Host.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(principal)}, Background); err != nil {
		h.t.Fatal(err)
	}
	client, err := p2runtime.NewHost(host.HostOptions{OnError: reportTo(h.record)})
	if err != nil {
		h.t.Fatal(err)
	}
	remote := p2runtime.Server
	if h.opts.Worker {
		remote = p2runtime.Host
	}
	if err := client.Connect(up, host.LinkOptions{Principal: p2runtime.ToChord(remote)}, Background); err != nil {
		h.t.Fatal(err)
	}
	report, err := client.Load(facets, Background)
	if err == nil && len(report.Failed) > 0 {
		err = report.Failed[0].Error
	}
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.clients = append(h.clients, client)
	h.mu.Unlock()
	return client
}

// Reload reloads the host with facets in place of the loaded ones (the same base and stubs); it fails a failed plugin.
func (h *TestHost) Reload(base host.Facet, facets []host.Facet) error {
	report, err := h.Host.Reload(plugins(base, facets), Background, host.ReloadOptions{})
	if err == nil && len(report.Failed) > 0 {
		err = report.Failed[0].Error
	}
	return err
}

<<<<<<< HEAD
// ReloadFacets reloads the host with facets in place of the loaded ones, under the base and stubs NewTestHost made.
func (h *TestHost) ReloadFacets(facets ...host.Facet) error { return h.Reload(h.base, facets) }
||||||| merged common ancestors
=======
// ReloadFacets reloads the host with facets in place of the loaded ones, under the host's own base.
func (h *TestHost) ReloadFacets(facets []host.Facet) error { return h.Reload(h.base, facets) }
>>>>>>> ec394bcefadfc0be926095f42c72d3f8b202efe2

// WorkerHost is the worker of session, a host of its own at the session's address with a link to this server host, which
// stamps it the worker's principal (as a real worker's link is): the registries of a worker and the given facets are loaded
// on it (p2test.WorkerFacet gives it the worker natives). It closes with the test host.
func (h *TestHost) WorkerHost(session string, generation int, facets ...host.Facet) host.Host {
	h.t.Helper()
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", session))
	worker, err := p2runtime.NewHost(host.HostOptions{Address: &address, Principal: p2runtime.ToChord(p2runtime.Host), OnError: reportTo(h.record)})
	if err != nil {
		h.t.Fatal(err)
	}
	down, up := link.MemoryLinkPair()
	if err := h.Host.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.WorkerPrincipal{Session: session, Generation: generation})}, Background); err != nil {
		h.t.Fatal(err)
	}
	if err := worker.Connect(up, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, Background); err != nil {
		h.t.Fatal(err)
	}
	base := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error { return p2runtime.ServeWorkerRegistries(facet) })
	if err := h.load(worker, base, facets); err != nil {
		_ = worker.Close(Background)
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.clients = append(h.clients, worker)
	h.mu.Unlock()
	return worker
}

// SetCell sets cell to value from the test (the write enters a section of the host's loop).
func (h *TestHost) SetCell(cell *Cell, value any) error { return cell.Replace(value) }

// Close closes the clients and the host (once).
func (h *TestHost) Close() {
	h.mu.Lock()
	clients := h.clients
	h.clients = nil
	h.mu.Unlock()
	for _, client := range clients {
		_ = client.Close(Background)
	}
	_ = h.Host.Close(Background)
}
