package tuitest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TopologyOptions are what NewTopology takes.
type TopologyOptions struct {
	// Server and Worker are the stubs of the "server" host and the "session:s" worker host.
	Server, Worker []p2test.Stub
	// Facets are the TUI's facets on the client host (the core facet is added by the caller: pass tui.CoreFacet first).
	Facets []host.Facet
	// Role is the visitor's role; "owner" by default.
	Role tuiruntime.TuiRole
	// Columns is the terminal width; 80 by default.
	Columns int
	// Ready, when set, holds the worker back until it is closed (a starting worker, a reconnect): the session is unreachable until then.
	Ready <-chan struct{}
}

// Topology is the deployed topology for tests (test/p2/tui-fixture.ts `topology`): a "server" host, a "session:s" worker host below it (the server's link to
// it is the sessions plugin's route), and the TUI's client host above it running a TuiRuntime with the given TUI facets on a virtual terminal.
type Topology struct {
	*Fixture
	Server *p2test.TestHost
	Worker host.Host
	Client host.Host
}

// NewTopology builds the topology: the runtime is connected, viewing session "s" on the chat screen.
func NewTopology(t testing.TB, options TopologyOptions) *Topology {
	t.Helper()
	owner := p2runtime.OwnerPrincipal{Access: "local"}
	// The worker host below the server: reached through the server's route (spawned on first use, as the session workers' route does), once Ready.
	workerAddress := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "s"))
	worker := p2test.NewTestHost(t, p2test.TestHostOptions{Address: &workerAddress, Worker: true, Stubs: options.Worker})
	var once sync.Once
	var down link.Link
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Stubs: options.Server, Principal: owner, BeforeLoad: func(h host.Host) {
		_, err := h.Route(p2runtime.SessionKind, host.RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(address contracts.Address, ctx context.Context) (host.ResolvedLink, error) {
				// One worker host: other sessions (a new one the TUI opens) have none to reach.
				if id, _ := address.Params.Value("id").(string); id != "s" {
					return host.ResolvedLink{}, fmt.Errorf("no worker for %s", address.Key)
				}
				if options.Ready != nil {
					chordctx.Suspend(ctx)
					select {
					case <-options.Ready:
					case <-ctx.Done():
						return host.ResolvedLink{}, context.Cause(ctx)
					}
				}
				var err error
				once.Do(func() {
					var up link.Link
					down, up = link.MemoryLinkPair()
					err = worker.Host.Connect(up, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, p2test.Background)
				})
				if err != nil {
					return host.ResolvedLink{}, err
				}
				return host.ResolvedLink{Link: down, Principal: p2runtime.ToChord(p2runtime.WorkerPrincipal{Session: "s", Generation: 1})}, nil
			},
		}, p2test.Background)
		if err != nil {
			t.Error(err)
		}
	}})
	facets := make([]host.HostFacet, len(options.Facets))
	for i, facet := range options.Facets {
		facets[i] = host.HostFacet{Name: "tui-" + string(rune('0'+i)), Facet: facet}
	}
	client := server.Client(owner, facets...)
	columns := options.Columns
	if columns == 0 {
		columns = 80
	}
	role := options.Role
	if role == "" {
		role = tuiruntime.RoleOwner
	}
	f := &Fixture{T: t, Host: server, queue: make(chan func(), 4096), stop: make(chan struct{})}
	f.Terminal = vterm.New(columns, 24)
	f.TUI = pitui.NewTuiAltScreen(f.Terminal, nil, t.TempDir())
	f.start(client, func(rt *tuiruntime.TuiRuntime) {
		rt.Connected = true
		rt.Role = role
		rt.Session, rt.HasSession = "s", true
		rt.Screen = "chat"
	})
	// `await runtime.start()`: the surfaces exist before the test types.
	f.Poll(f.Runtime.SlotsReady)
	return &Topology{Fixture: f, Server: server, Worker: worker.Host, Client: client}
}

// Screen is the visible screen as text, after a render (the scrollbar column trimmed).
func (f *Fixture) Screen() string {
	f.T.Helper()
	f.Do(func() { f.TUI.RenderNow() })
	lines := f.Terminal.Viewport()
	for i, line := range lines {
		lines[i] = strings.TrimRight(strings.TrimRight(line, "│┃"), " ")
	}
	return strings.Join(lines, "\n")
}

// Attach is a second runtime on h (a reload's: a new terminal and TUI over the same client host); prepare sets its state before it starts.
func Attach(t testing.TB, h host.Host, columns int, prepare func(*tuiruntime.TuiRuntime)) *Fixture {
	t.Helper()
	f := &Fixture{T: t, queue: make(chan func(), 4096), stop: make(chan struct{})}
	f.Terminal = vterm.New(columns, 24)
	f.TUI = pitui.NewTuiAltScreen(f.Terminal, nil, t.TempDir())
	f.start(h, prepare)
	return f
}
