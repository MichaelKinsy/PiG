package runtime

import (
	"context"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/src/runtime/chord.ts: Chord v2's API with p2's principals. Upstream re-exports Chord's types
// specialised to `P2Principal` (Ctx, Host, Facet, Factory, Self, ...); Go's Chord host carries principals as
// data.Principal, and runtime.ToChord / FromChord (principals.go) convert at the boundary, so those aliases have no Go
// form. What remains is p2's two host kinds and the constructors every p2 host uses.

// ServerKind is the server: one host (`hostKind("server")`).
var ServerKind = mustKind("server")

// SessionKind is a session's worker: one per session (`hostKind("session", { id: str() })`).
var SessionKind = mustKind("session", contracts.Prop("id", contracts.Str()))

func mustKind(name string, params ...contracts.Property) *contracts.HostKind {
	kind, err := contracts.NewHostKind(name, params...)
	if err != nil {
		panic(err)
	}
	return kind
}

// ServerHost is where server objects live (a contract's `hostAddress`: `serverHost`).
func ServerHost(*delta.JsonObject) (contracts.Address, error) {
	return ServerKind.Address(delta.NewJsonObject(0)), nil
}

// SessionHost is where session objects live: the worker of the params' `session` (a contract's `hostAddress`:
// `sessionHost`).
func SessionHost(params *delta.JsonObject) (contracts.Address, error) {
	return SessionKind.Address(delta.JsonObjectOf("id", params.Value("session"))), nil
}

// NewHost is a p2 host (`createHost`): principals keyed and revoked the same way on every host. options' PrincipalKey and
// RevokedBy are set when absent; Principal is the host's own as a Chord principal (ToChord of a p2 Principal).
func NewHost(options host.HostOptions) (host.Host, error) {
	if options.PrincipalKey == nil {
		options.PrincipalKey = PrincipalKeyFunction
	}
	if options.RevokedBy == nil {
		options.RevokedBy = ChordRevokedBy
	}
	return host.CreateHost(options)
}

// Forbidden is Chord's `forbidden(message)`: what a plugin throws for a refused principal.
func Forbidden(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: message}, nil)
}

// CtxOf is the Chord call context of a method handler's ctx.
func CtxOf(ctx context.Context) host.Ctx {
	if each, ok := ctx.(host.Ctx); ok {
		return each
	}
	return nil
}

// FollowNative is a state source following a native that tells listeners of its changes (subscribe returns the stop):
// `self.Follow` of a synchronous observer, so the state is the native's, never a copy a routine keeps. Its value is read()
// at activation and after every change, sent whole (`[["r", value]]`: derives over it diff for the wire). Both run once the
// object's natives opened, so they may read a native's value. A change the native makes inside a method reaches the source
// before the method's answer, and the source's last value is the native's state after its last change, however many
// goroutines change it at once (followedChanges). A read that fails at activation fails the activation with its error (Pi: it
// throws); after a change, the source fails until the next change.
func FollowNative(self host.Self, subscribe func(changed func()) (stop func()), read func() (delta.JsonValue, error)) chord.Watchable[delta.JsonValue] {
	return self.Follow(func(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
		changes := followedChanges{read: read, advance: func(call chord.WatchCall[any]) { observer.Advance(call, ctx) }}
		detach := subscribe(changes.changed)
		value, err := read()
		if err != nil {
			detach()
			return nil, err
		}
		return &host.FollowAttachment[delta.JsonValue]{Value: value, Detach: detach}, nil
	})
}

// followedChanges turns a native's change notifications, which may come from any goroutine at once, into observer calls
// whose last one carries the native's state after its last change. Pi's observers run in the one synchronous step that
// made the change, so their calls follow the changes; here a goroutine that read the state, then lost the host's loop to
// the goroutine that changed it again, would advance its stale read after the newer one and leave the source behind the
// native for good (a login's cancel against the goroutine closing its prompt). The observer applies calls in the order
// they arrive and advance returns once its call is applied, so a goroutine that sees another change notified while its
// call went in reads and advances again: its last call is then made after the last change, and the call applied last is
// always the last goroutine's, or one made after it.
type followedChanges struct {
	// notified counts the notifications.
	notified atomic.Uint64
	read     func() (delta.JsonValue, error)
	advance  func(chord.WatchCall[any])
}

// changed is the native's notification: it returns once the source holds the state read after the last change notified.
func (f *followedChanges) changed() {
	seen := f.notified.Add(1)
	for {
		value, err := f.read()
		if err != nil {
			f.advance(chord.WatchCall[any]{Kind: chord.WatchCallFailed, Error: err})
		} else {
			f.advance(chord.WatchCall[any]{Kind: chord.WatchCallValue, Value: value, Ops: []delta.Op{{"r", value}}})
		}
		latest := f.notified.Load()
		if latest == seen {
			return
		}
		seen = latest
	}
}
