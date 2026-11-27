// Package deskfix is the `Desk` contract and `desk()` fixture of packages/chord/test/support.ts (Pi f57cd9484): a server
// serving a remote type its caller reads or edits by the mode its admission says, and a client connected to it. The tests of
// the UI bindings' variant (surfaces `LiveIn`, tui `TuiScope.Variant`) share it; it is not imported by production code.
//
// Forced Go difference: Pi's fixture lives in the test support of its package; Go's test files are not importable by another
// package's tests, and the surfaces and tui tests both need it, so it is a package under internal.
package deskfix

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

var background = context.Background()

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// UntilState waits until check holds, checked now and at every state of ref (Pi's `expect.poll`, which a Go test waits on the
// state's own changes instead of the clock: a missing one fails after 30 s).
func UntilState(t testing.TB, ref *host.Reference, check func() bool) {
	t.Helper()
	changed := make(chan struct{}, 1)
	watch, err := ref.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	watch.Start(func(context.Context, chord.NativeWatchCall[*host.RefState]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	// The wait ends with the test (its context) or after 30 s: a failure either way.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for !check() {
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("condition not reached: %v", ctx.Err())
		}
	}
}

// HasGrant reports whether ref has been granted name.
func HasGrant(ref *host.Reference, name string) bool {
	granted, err := ref.Granted()
	return err == nil && granted[name]
}

var server = must(contracts.NewHostKind("server"))

// Desk is a desk its caller reads or edits by its admitted mode: `write` is grantable (Pi's `Desk`).
var Desk = must(contracts.Remote("test.desk", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return server.Address(nil), nil },
	State:   []contracts.RemoteStateEntry{contracts.StateEntry("text", contracts.Str())},
	Methods: []contracts.RemoteMethodEntry{contracts.Meth("write", contracts.NewGrantable(contracts.NewMethod(contracts.Str())))},
}))

// deskMode is the mode `Desk`'s admission reads.
var deskMode = must(contracts.Local("test.desk.mode", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("mode", contracts.Str()), contracts.Mem("set", contracts.NewMethod(contracts.Str()))},
}))

// Fixture is a server serving Desk and a client connected to it (Pi's `{ server, client, admit }`).
type Fixture struct {
	Server, Client host.Host
	mode           *host.Reference
	mu             sync.Mutex
	closed         chan struct{}
}

// Gate holds the desk's next objects loading until the returned function runs: Pi's fixture has no such thing, its tests read a
// state as its watch delivers it, in the turn the state changed in; a Go watch delivers on a goroutine, by when the state
// may have moved on.
func (f *Fixture) Gate() (open func()) {
	gate := make(chan struct{})
	f.mu.Lock()
	f.closed = gate
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		if f.closed == gate {
			f.closed = nil
		}
		f.mu.Unlock()
		close(gate)
	}
}

func (f *Fixture) gate() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// Admit changes the mode the desk admits: "reader", "editor", or "none" (denied).
func (f *Fixture) Admit(mode string) error {
	_, err := f.mode.Call("set", mode, background)
	return err
}

// New is Pi's `desk()`: the client is admitted as the desk's mode says, `reader` at first; Admit changes it. Both hosts are
// closed when the test ends.
func New(t testing.TB) *Fixture {
	t.Helper()
	address := server.Address(nil)
	f := &Fixture{}
	var err error
	f.Server, err = host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	f.Client, err = host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Client.Close(background); err != nil {
			t.Errorf("client close: %v", err)
		}
		if err := f.Server.Close(background); err != nil {
			t.Errorf("server close: %v", err)
		}
	})
	facet := host.DefineFacet(func(fh host.FacetHost, _ host.FacetSelf) error {
		if err := fh.Serve(deskMode, func(_ *delta.JsonObject, self host.Self) (contracts.MembersImplementation, error) {
			mode := self.Writable("reader")
			return contracts.MembersImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"mode": self.Derive([]any{mode}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(next delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						return nil, mode.Set(next)
					},
				},
			}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return fh.Serve(Desk, func(_ *delta.JsonObject, _ data.Principal, _ host.AdmittedValue, self host.Self) (contracts.MembersImplementation, error) {
			if gate := f.gate(); gate != nil {
				// Its object stays loading until the gate opens: a test reads what a reference says in that state.
				self.Open(func(ctx context.Context) (any, error) {
					_, err := chordctx.Receive(ctx, gate)
					return nil, err
				}, func(any, host.NativeCloseReason, context.Context) error { return nil })
			}
			text := self.Writable("a desk")
			shown := self.Derive([]any{text}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
			write := func(next delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return nil, text.Set(next) }
			return contracts.MembersImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"text": shown},
				Methods: map[string]contracts.MethodFunction{"write": write},
			}, nil
		}, host.ServeOptions{Admit: func(_ *delta.JsonObject, _ data.Principal, self host.AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			mode := self.Get(deskMode, delta.JsonObjectOf()).Member("mode")
			return self.Derive([]any{mode}, func(values []any) (delta.JsonValue, error) {
				if values[0] == "reader" {
					return host.Allow(delta.NewJsonObject(0)), nil
				}
				if values[0] == "editor" {
					return host.Allow(delta.JsonObjectOf("granted", []delta.JsonValue{"write"})), nil
				}
				return host.Deny("no access"), nil
			}), nil
		}})
	})
	if report, err := f.Server.Load([]host.HostFacet{{Name: "@test/desk", Facet: facet}}, background); err != nil || len(report.Failed) != 0 {
		t.Fatalf("server load: %v %v", err, report.Failed)
	}
	if report, err := f.Client.Load(nil, background); err != nil || len(report.Failed) != 0 {
		t.Fatalf("client load: %v %v", err, report.Failed)
	}
	mine, theirs := link.MemoryLinkPair()
	if err := f.Server.Accept(theirs, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "user", "id", "u")}, background); err != nil {
		t.Fatal(err)
	}
	if err := f.Client.Connect(mine, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server")}, background); err != nil {
		t.Fatal(err)
	}
	f.mode, err = f.Server.Get(background, deskMode, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
