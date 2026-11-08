package tuitest

import (
	"testing"

	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// OnHost is a runtime on client (a host that loaded the TUI facets), connected as role, on a virtual terminal of columns x rows (100x30 by default), the
// session "s" shown on the chat screen when session is true. For tests whose server is a real p2 server (p2test.Fixture) rather than a stub topology.
func OnHost(t testing.TB, client host.Host, role tuiruntime.TuiRole, columns, rows int, session bool) *Fixture {
	t.Helper()
	return OnHostWith(t, client, role, columns, rows, func(rt *tuiruntime.TuiRuntime) {
		if session {
			rt.Session, rt.HasSession = "s", true
			rt.Screen = "chat"
		}
	})
}

// OnHostWith is OnHost with the runtime's state set by prepare (on the owner loop, before the runtime starts): a reload restores a snapshot there.
func OnHostWith(t testing.TB, client host.Host, role tuiruntime.TuiRole, columns, rows int, prepare func(*tuiruntime.TuiRuntime)) *Fixture {
	t.Helper()
	if columns == 0 {
		columns, rows = 100, 30
	}
	f := &Fixture{T: t, queue: make(chan func(), 4096), stop: make(chan struct{})}
	f.Terminal = vterm.New(columns, rows)
	f.TUI = pitui.NewTuiAltScreen(f.Terminal, nil, t.TempDir())
	f.start(client, func(rt *tuiruntime.TuiRuntime) {
		rt.Connected = true
		rt.Role = role
		prepare(rt)
	})
	return f
}
