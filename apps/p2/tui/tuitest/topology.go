package tuitest

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	"github.com/MichaelKinsy/PiG/chord/next/host"
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
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Stubs: options.Server, Principal: owner})
	worker := server.WorkerHost("s", 1, p2test.StubFacet(options.Worker...))
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
	return &Topology{Fixture: f, Server: server, Worker: worker, Client: client}
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
