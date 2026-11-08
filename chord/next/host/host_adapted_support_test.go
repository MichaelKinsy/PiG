package host

import (
	"context"
	"reflect"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Support of host_adapted_test.go: what adapted.test.ts builds with `local(...)`, `defineFacet(...)`, `changesOf`, `valuesOf`,
// `instancesOf(AdaptedMembers, ...)` and `Promise.withResolvers`.

// adaptedLocal is `local(name, { version: 1, params: {}, members })`.
func adaptedLocal(name string, members ...contracts.LocalEntry) *contracts.Contract {
	return mustContract(declareLocal(name, contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: members}))
}

// adaptedObjOf is `obj({ name: int(), ... })`.
func adaptedObjOf(names ...string) contracts.Schema {
	props := make([]contracts.Property, len(names))
	for i, name := range names {
		props[i] = contracts.Prop(name, contracts.Int())
	}
	return contracts.Obj(props...)
}

// adaptedField is `(value) => value.a.b...`.
func adaptedField(names ...string) func(delta.JsonValue) (delta.JsonValue, error) {
	return func(value delta.JsonValue) (delta.JsonValue, error) {
		for _, name := range names {
			value = value.(*delta.JsonObject).Value(name)
		}
		return value, nil
	}
}

// adaptedIdentity is `(value) => value`.
func adaptedIdentity(value delta.JsonValue) (delta.JsonValue, error) { return value, nil }

// adaptedState is a state mapping entry of member through f.
func adaptedState(member string, f func(delta.JsonValue) (delta.JsonValue, error)) contracts.Entry[contracts.StateMapping] {
	return contracts.Entry[contracts.StateMapping]{Name: member, Value: contracts.StateMapping{Func: f}}
}

// adaptedVersion is `{ [version]: { state: { ... } } }`.
func adaptedVersion(version string, state ...contracts.Entry[contracts.StateMapping]) contracts.Entry[contracts.VersionMappings] {
	return contracts.Entry[contracts.VersionMappings]{Name: version, Value: contracts.VersionMappings{State: state}}
}

// adaptedConstants serves contract with the constant state members values; with runs in the factory before it returns
// (nil: nothing), as `self.get(Missing, {})` or `self.open(...)` do in the upstream factories.
func adaptedConstants(contract *contracts.Contract, values map[string]delta.JsonValue, accepts contracts.AcceptsTable, with func(self Self)) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(contract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			if with != nil {
				with(self)
			}
			state := make(map[string]chord.Watchable[delta.JsonValue], len(values))
			for name, value := range values {
				state[name] = constant(value)
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{State: state}}, nil
		}, ServeOptions{Accepts: accepts})
	})
}

// adaptedEmptyFacet serves contract, a type without members.
func adaptedEmptyFacet(contract *contracts.Contract) Facet {
	return adaptedConstants(contract, nil, nil, nil)
}

// adaptedCalls is the collected calls of a watch.
type adaptedCalls = *nexttest.Collector[chord.WatchCall[delta.JsonValue]]

// adaptedWatch is `await ref.member.watch(context)` with `changesOf(watch)`.
func adaptedWatch(t testing.TB, ref *Reference, member string) (chord.WatchHandle[delta.JsonValue], adaptedCalls) {
	t.Helper()
	watch, err := ref.Member(member).Watch(background)
	must(t, err)
	return watch, nexttest.ChangesOf(watch)
}

// adaptedExpectValues is `expect(valuesOf(calls)).toEqual(want)`: the host's deliveries are waited for (the watch's
// frames arrive on a goroutine of their own), until the calls are as many as want, then once more so that a later extra
// frame shows.
func adaptedExpectValues(t testing.TB, host Host, calls adaptedCalls, want ...any) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		turn(host)
		deliveredOn(host)
		if calls.Len() >= len(want) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	turn(host)
	deliveredOn(host)
	if got := nexttest.ValuesOf(calls.Items()); !reflect.DeepEqual(got, append([]any{}, want...)) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

// adaptedLast is `valuesOf(calls).at(-1)`.
func adaptedLast(calls adaptedCalls) any {
	values := nexttest.ValuesOf(calls.Items())
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1]
}

// adaptedUntilLast waits until the last value of calls is want.
func adaptedUntilLast(t testing.TB, host Host, calls adaptedCalls, want any) {
	t.Helper()
	scopeEventually(t, "the last value of the watch is as expected", func() bool {
		turn(host)
		deliveredOn(host)
		return reflect.DeepEqual(adaptedLast(calls), want)
	})
}

// adaptedClosed is `closed`, the flag of `void watch.closed.then(() => { closed = true })`.
func adaptedClosed(watch chord.WatchHandle[delta.JsonValue]) bool {
	select {
	case <-watch.Closed():
		return true
	default:
		return false
	}
}

// adaptedMade are the shared mappings of version, weakly, that read a state member (key false) or a key of a keyed member
// (key true) so far: Pi's `instancesOf(AdaptedMembers, "member" | "key")` taken after the act, over the instances every
// object of the host shows or replaces.
func adaptedMade(host Host, version int, key bool) []weak.Pointer[adaptedMembers] {
	var made []weak.Pointer[adaptedMembers]
	h := host.(*chordHost)
	h.enter(func() {
		attempts := map[*attempt]bool{}
		for _, object := range h.objects {
			if object.phase.attempt != nil {
				attempts[object.phase.attempt] = true
			}
			if object.old != nil {
				attempts[object.old] = true
			}
		}
		for each := range attempts {
			for _, adapted := range each.adapted {
				if adapted.version == version && ((key && len(adapted.keys) > 0) || (!key && len(adapted.members) > 0)) {
					made = append(made, weak.Make(adapted))
				}
			}
		}
	})
	return made
}

// adaptedWeak is `new WeakRef(value)` of a member's value, taken where nothing else of the caller keeps it.
func adaptedWeak(t testing.TB, value delta.JsonValue) weak.Pointer[delta.JsonObject] {
	t.Helper()
	object, ok := value.(*delta.JsonObject)
	if !ok {
		t.Fatalf("value %v is not an object", value)
	}
	return weak.Make(object)
}

// adaptedGate is `Promise.withResolvers<number>()`: a native's open waits for it; open resolves it.
type adaptedGate struct {
	entered signal
	opened  chan struct{}
}

func newAdaptedGate() *adaptedGate {
	return &adaptedGate{entered: newSignal(), opened: make(chan struct{})}
}

// open is `() => {}`'s native: `self.open(async () => gate.promise, () => {})`.
func (g *adaptedGate) native(self Self) {
	self.Open(func(ctx context.Context) (any, error) {
		g.entered.fire()
		select {
		case <-g.opened:
			return nil, nil
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}, func(any, NativeCloseReason, context.Context) error { return nil })
}

// resolve is `gate.resolve(1)`.
func (g *adaptedGate) resolve() { close(g.opened) }

// adaptedReload is `host.reload(...)` unawaited: its error arrives on the channel.
func adaptedReload(host Host, facets ...HostFacet) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := host.Reload(facets, background, ReloadOptions{})
		done <- err
	}()
	return done
}

// adaptedWatching is `ref.member.watch(context)` unawaited: its watch (or error) arrives on the channel.
type adaptedWatching struct {
	watch chord.WatchHandle[delta.JsonValue]
	err   error
}

func adaptedWatchLater(ref *Reference, member string) <-chan adaptedWatching {
	done := make(chan adaptedWatching, 1)
	go func() {
		watch, err := ref.Member(member).Watch(background)
		done <- adaptedWatching{watch: watch, err: err}
	}()
	return done
}

// adaptedWaitWatch is `await opening`: the watch, failing the test with its error.
func adaptedWaitWatch(t testing.TB, opening <-chan adaptedWatching) chord.WatchHandle[delta.JsonValue] {
	t.Helper()
	got := receive(t, opening)
	must(t, got.err)
	return got.watch
}

// adaptedWantWaiting fails unless the state of ref is a failed one waiting.
func adaptedWantWaiting(t testing.TB, ref *Reference) {
	t.Helper()
	state := untilKind(t, ref, RefStateFailed)
	if state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
}

// adaptedLoadOne is `await host.load([{ name, facet }], context)`.
func adaptedLoadOne(t testing.TB, host Host, name string, facet Facet) {
	t.Helper()
	mustLoad(t)(host.Load([]HostFacet{{Name: name, Facet: facet}}, background))
}

// adaptedReloadOne is `await host.reload([{ name, facet }], context)`.
func adaptedReloadOne(t testing.TB, host Host, name string, facet Facet) {
	t.Helper()
	reloadMust(t)(host.Reload([]HostFacet{{Name: name, Facet: facet}}, background, ReloadOptions{}))
}

// adaptedReloadNone is `await host.reload([], context)`.
func adaptedReloadNone(t testing.TB, host Host) {
	t.Helper()
	reloadMust(t)(host.Reload(nil, background, ReloadOptions{}))
}

// adaptedWant fails unless got is want.
func adaptedWant(t testing.TB, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}
