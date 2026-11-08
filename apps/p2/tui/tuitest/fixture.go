// Package tuitest is the fixture of the TUI tests, the Go twin of test/p2/tui-fixture.ts and the `runtimeWith` helpers of the tui-*.test.ts files: a test host
// with the TUI facets, a virtual terminal, a TuiAltScreen and a TuiRuntime.
//
// Forced Go difference: the runtime's methods run on the TUI's owner loop. The fixture runs one goroutine as that loop; Do runs a test's own steps on it and the
// runtime's Post (callbacks of watched references and registries) queues onto it, so a test body never races the runtime.
package tuitest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// Options are what New takes.
type Options struct {
	// Facets are loaded on the host in order.
	Facets []host.Facet
	// Columns and Rows are the terminal size; 80x24 by default.
	Columns, Rows int
	// Host adds to the test host options (Stubs, Worker, ...); Facets is ignored.
	Host p2test.TestHostOptions
}

// Fixture is the test host, terminal and runtime.
type Fixture struct {
	T        testing.TB
	Host     *p2test.TestHost
	Terminal *vterm.Terminal
	TUI      *pitui.TuiAltScreen
	Runtime  *tuiruntime.TuiRuntime

	queue chan func()
	stop  chan struct{}
	wg    sync.WaitGroup
}

func (f *Fixture) loop() {
	defer f.wg.Done()
	for {
		select {
		case fn := <-f.queue:
			fn()
		case <-f.stop:
			return
		}
	}
}

// Do runs fn on the owner loop and waits for it.
func (f *Fixture) Do(fn func()) {
	f.T.Helper()
	done := make(chan struct{})
	f.queue <- func() {
		defer close(done)
		fn()
	}
	<-done
}

// Send sends terminal input to the TUI (stage 2 of the precedence contract runs first, then the focused runtime).
func (f *Fixture) Send(data string) {
	f.T.Helper()
	f.Do(func() { f.TUI.HandleTerminalInput(data) })
}

// Poll waits for cond (run on the owner loop) to hold.
func (f *Fixture) Poll(cond func() bool) {
	f.T.Helper()
	Poll(f.T, func() bool {
		ok := false
		f.Do(func() { ok = cond() })
		return ok
	})
}

// Poll waits for cond to hold, failing the test after five seconds.
func Poll(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("poll: the condition never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// New is the host with options.Facets, the runtime on it, started and focused on a TuiAltScreen laid out over the runtime's root. Everything stops with the test.
func New(t testing.TB, options Options) *Fixture {
	t.Helper()
	hostOptions := options.Host
	hostOptions.Facets = options.Facets
	columns, rows := options.Columns, options.Rows
	if columns == 0 {
		columns, rows = 80, 24
	}
	f := &Fixture{T: t, queue: make(chan func(), 4096), stop: make(chan struct{})}
	f.Host = p2test.NewTestHost(t, hostOptions)
	f.Terminal = vterm.New(columns, rows)
	f.TUI = pitui.NewTuiAltScreen(f.Terminal, nil, t.TempDir())
	f.start(f.Host.Host, nil)
	return f
}

// start runs the owner loop and the runtime on h; prepare sets the runtime's state before it starts.
func (f *Fixture) start(h host.Host, prepare func(*tuiruntime.TuiRuntime)) {
	t := f.T
	f.TUI.SetOwnerDispatcher(func(ctx context.Context, fn func()) error {
		select {
		case f.queue <- fn:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	// Timer-scheduled renders run on the owner loop too: components are not safe for a renderer goroutine of their own.
	f.TUI.SetRenderDispatcher(func(render func()) {
		select {
		case f.queue <- render:
		case <-f.stop:
		}
	})
	f.wg.Add(1)
	go f.loop()
	f.Do(func() {
		f.Runtime = tuiruntime.NewTuiRuntime(h, f.TUI, nil)
		if prepare != nil {
			prepare(f.Runtime)
		}
		f.Runtime.Start()
		f.TUI.SetLayoutRoot(f.Runtime.Root)
		f.TUI.SetFocus(f.Runtime)
		f.TUI.Start()
	})
	t.Cleanup(func() {
		f.Do(func() {
			f.TUI.Stop()
			f.Runtime.Dispose()
		})
		close(f.stop)
		f.wg.Wait()
	})
}
