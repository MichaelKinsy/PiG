package host

import (
	"context"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Own tests of host_route.go (packages/chord/test/next/host/route.test.ts) and host_destination.go (which has no upstream
// test file). Every upstream case of route.test.ts builds hosts and links (`relayed`, `createHost`, `memoryLinkPair`,
// `RawPeer`), whose path, link and host are later lanes: they are listed `missing` in the lane's rows. These tests run the
// route over fakes of its seams (routePath, routeLink, routeHost) on the fake timers, and each names the Pi case whose
// behavior it carries; none replaces a `missing` case. The fakes implement the members of Pi's `Path` and `HostLink` that
// route.ts reads, as Pi's classes do (remote/path.ts: attach and detach tell their address's `changed`, `disconnected`
// does too).

// routeSessionKind is `hostKind("session", { id: str() })`.
func routeSessionKind(t testing.TB) *contracts.HostKind {
	t.Helper()
	kind, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
	if err != nil {
		t.Fatal(err)
	}
	return kind
}

// rigLink is a link.Link whose close is observable, the other end of a resolve's result.
type rigLink struct {
	mu     sync.Mutex
	closed bool
}

func (l *rigLink) Send([]byte)            {}
func (l *rigLink) OnMessage(func([]byte)) {}
func (l *rigLink) OnClose(func())         {}
func (l *rigLink) Close()                 { l.mu.Lock(); l.closed = true; l.mu.Unlock() }
func (l *rigLink) isClosed() bool         { l.mu.Lock(); defer l.mu.Unlock(); return l.closed }

// rigHostLink is the routeLink the fake host made of a rigLink: open unless its link was closed, greeted when its peer
// greeted first.
type rigHostLink struct {
	raw     *rigLink
	open    bool
	greeted bool
	role    string
}

func (l *rigHostLink) Open() bool    { return l.open }
func (l *rigHostLink) Greeted() bool { return l.greeted }
func (l *rigHostLink) Role() string  { return l.role }
func (l *rigHostLink) Close()        { l.raw.Close() }

// rigPath is Pi's Path over fake holders: attach and detach tell the route (`changed`) as remote/path.ts does.
type rigPath struct {
	check    func()
	link     routeLink
	linked   bool
	holders  []routedHolder
	dropped  int
	attached []routeLink
}

func (p *rigPath) Link() routeLink         { return p.link }
func (p *rigPath) Linked() bool            { return p.linked }
func (p *rigPath) Held() bool              { return len(p.holders) > 0 }
func (p *rigPath) Holders() []routedHolder { return append([]routedHolder{}, p.holders...) }

func (p *rigPath) Connected(made routeLink) {
	p.link, p.linked = made, true
	for range p.holders {
		p.attached = append(p.attached, made)
	}
}

func (p *rigPath) Disconnected(made routeLink) {
	if p.link != made {
		return
	}
	p.link = nil
	p.dropped += len(p.holders)
	p.check()
}

// attach is Path.attach: holds holder on the current link now, and on every next one.
func (p *rigPath) attach(holder routedHolder) {
	p.holders = append(p.holders, holder)
	if p.link == nil {
		p.check()
	} else {
		p.attached = append(p.attached, p.link)
	}
}

// detach is Path.detach: holder was released or ended.
func (p *rigPath) detach(holder routedHolder) {
	for i, each := range p.holders {
		if each == holder {
			p.holders = append(p.holders[:i:i], p.holders[i+1:]...)
			break
		}
	}
	if len(p.holders) == 0 {
		p.check()
	}
}

// rigHost is the routeHost: closing is the test's, links are made of what the route's resolve returned.
type rigHost struct {
	loopOf  *scopeLoop
	mu      sync.Mutex
	closing bool
	reports []HostError
	made    []*rigHostLink
	options []routeLinkOptions
	// greeted makes the next link greeted first, as a peer that said hello while its link was made.
	greeted bool
}

func (h *rigHost) loop() sectionLoop { return h.loopOf }
func (h *rigHost) Closing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing
}
func (h *rigHost) setClosing() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
}
func (h *rigHost) Report(err HostError) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reports = append(h.reports, err)
}
func (h *rigHost) RouteLink(made link.Link, options routeLinkOptions, _ context.Context) routeLink {
	raw := made.(*rigLink)
	h.mu.Lock()
	defer h.mu.Unlock()
	hostLink := &rigHostLink{raw: raw, open: !raw.isClosed(), greeted: h.greeted, role: "child"}
	h.made = append(h.made, hostLink)
	h.options = append(h.options, options)
	return hostLink
}
func (h *rigHost) reported() []HostError {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostError{}, h.reports...)
}
func (h *rigHost) links() []*rigHostLink {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*rigHostLink{}, h.made...)
}

// routeRig is a route of kind "session" over the fakes. attempts counts resolve calls; resolve is the test's, and returns
// the raw link and principal a worker would.
type routeRig struct {
	t       testing.TB
	loop    *scopeLoop
	host    *rigHost
	route   *route
	context context.Context
	cancel  func(cause error)

	mu        sync.Mutex
	attempts  []string
	resolve   func(address contracts.Address, ctx context.Context) (ResolvedLink, error)
	authorize func(principal data.Principal, address contracts.Address) (bool, error)
	paths     map[string]*rigPath
}

func newRouteRig(t testing.TB) *routeRig {
	t.Helper()
	loop := newScopeLoop()
	rig := &routeRig{t: t, loop: loop, host: &rigHost{loopOf: loop}, paths: map[string]*rigPath{}}
	rig.context, rig.cancel = chordctx.WithCancel(chordctx.Background())
	rig.resolve = rig.worker
	rig.authorize = func(data.Principal, contracts.Address) (bool, error) { return true, nil }
	route, err := newRoute(routeSessionKind(t), RouteOptions{
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			return rig.authorize(principal, address)
		},
		Resolve: func(address contracts.Address, ctx context.Context) (ResolvedLink, error) {
			rig.mu.Lock()
			rig.attempts = append(rig.attempts, address.Key)
			resolve := rig.resolve
			rig.mu.Unlock()
			return resolve(address, ctx)
		},
	}, rig.host, rig.context, func(check func()) routePath {
		path := &rigPath{check: check}
		return path
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.route = route
	return rig
}

// worker is the default resolve: a link to a worker whose principal is the session's.
func (r *routeRig) worker(address contracts.Address, _ context.Context) (ResolvedLink, error) {
	return ResolvedLink{Link: &rigLink{}, Principal: delta.JsonObjectOf("kind", "worker", "session", address.Params.Value("id"))}, nil
}

// path is `route.path` for session id, made as an owner.
func (r *routeRig) path(id string) *rigPath {
	r.t.Helper()
	var made routePath
	var err error
	r.loop.Run(func() {
		made, err = r.route.path(delta.JsonObjectOf("id", id), delta.JsonObjectOf("kind", "owner"), chordctx.Background())
	})
	if err != nil {
		r.t.Fatal(err)
	}
	path := made.(*rigPath)
	r.paths[id] = path
	return path
}

func (r *routeRig) attemptKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.attempts...)
}

// within runs work as a section of the host's loop.
func (r *routeRig) within(work func()) { r.loop.Run(work) }

// elapse is Pi's `elapse(ms)`: fake time moves, then the tasks it started are the test's to wait for.
func (r *routeRig) elapse(ms int64) { r.loop.Advance(ms) }

func (r *routeRig) waitAttempts(n int) {
	r.t.Helper()
	scopeEventually(r.t, "resolve attempts", func() bool { return len(r.attemptKeys()) >= n })
}

// settled waits for the resolve that ran to be handled: the address's resolving flag is down.
func (r *routeRig) settled(path *rigPath) {
	r.t.Helper()
	scopeEventually(r.t, "the resolve handled", func() bool {
		resolving := true
		r.loop.Run(func() {
			resolving = false
			for _, routed := range r.route.addresses {
				if routed.path == routePath(path) && routed.resolving {
					resolving = true
				}
			}
		})
		return !resolving
	})
}

var _ routeHost = (*rigHost)(nil)
