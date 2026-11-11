package host

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/examples/01-objects.ts .. 32-startup.ts (there is no 26 and 27). Pi's examples are
// scripts that print what they observe; each Go test runs the same scenario with the same inputs and asserts exactly
// what the script prints (`console.log` lines become expectations in the order Pi prints them; `console.error` lines,
// which Pi prints for background errors that must not happen, become an assertion that `onError` got nothing).
// Where Pi waits with `setTimeout`/`sleep` for an effect, the test polls the observable the script prints (scopeEventually),
// never a duration.
//
// API forms used here (AttemptObject, Reference, Jobs: see host_next_support_test.go for the host's):
//   - `Self` members: State(def) *StateHandle, KeyedState(def) *KeyedHandle, Derive(sources []any, compute), Get(Type,
//     params) *Binding with Member(name), Open(open, close, options...) Native[any], On(source, handler), OnLive(handler),
//     Job(def, impl) *JobHandle, Table(def) *store.TableHandle, Tx(fn, ctx), Writable(initial), Registry(token).
//   - `Reference` members: Call(method, args, ctx) (any, error), Member(name) chord.Watchable[delta.JsonValue],
//     Variant() string, State() chord.NativeWatchable[*RefState], Retry(), Release().

var (
	exStr     = contracts.Str
	exInt     = contracts.Int
	exBool    = contracts.Bool
	exObj     = contracts.Obj
	exArr     = contracts.Arr
	exUnknown = contracts.Unknown
	exOpt     = contracts.Opt
	exProp    = contracts.Prop
	exBytes   = contracts.Bytes
)

// exLocal is `local(name, { version: 1, params, members })`.
func exLocal(name string, params contracts.Params, members ...contracts.LocalEntry) *contracts.Contract {
	return mustContract(contracts.Local(name, contracts.LocalDeclaration{Version: 1, Params: params, Members: members}))
}

// exRemote is `remote(name, { version: 1, params, hostAddress, variants })`.
func exRemote(name string, params contracts.Params, address contracts.HostAddress, variants ...contracts.VariantEntry) *contracts.Contract {
	return mustContract(contracts.Remote(name, contracts.RemoteDeclaration{Version: 1, Params: params, HostAddress: address, Variants: variants}))
}

// exServerAddress is `hostAddress: () => Server()`.
func exServerAddress(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil }

// exSessionAddress is `hostAddress: ({ session }) => Session({ id: session })`.
func exSessionAddress(params *delta.JsonObject) (contracts.Address, error) {
	session, _ := params.Value("session").(string)
	return Session.Address(jsonObject("id", session)), nil
}

// exDefineState is `defineState(name, { schema, version: 1, initial })` with the options given.
func exDefineState(name string, options documents.StateOptions) *documents.StateDefinition {
	options.Version = 1
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

// exJSON is a JSON value as JSON.parse reads the text.
func exJSON(t testing.TB, text string) any {
	t.Helper()
	value, err := delta.DecodeJson([]byte(text))
	must(t, err)
	return value
}

// exEqual fails the test unless got and want are equal JSON values.
func exEqual(t testing.TB, what string, got, want any) {
	t.Helper()
	if !exJSONEqual(got, want) {
		t.Fatalf("%s = %s, want %s", what, exText(got), exText(want))
	}
}

// exReceive is a receive from ch that fails the test after the deadline of scopeEventually instead of hanging.
func exReceive[T any](t testing.TB, what string, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("never happened: %s", what)
		panic("unreachable")
	}
}

func exText(value any) string {
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

func exJSONEqual(got, want any) bool {
	return nexttest.JSONEqual(got, want) || exText(got) == exText(want)
}

// exMethod is a MethodFunction from a typed function over the argument object.
func exMethod(fn func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error)) contracts.MethodFunction {
	return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		object, _ := args.(*delta.JsonObject)
		return fn(object, ctx)
	}
}

// exImpl is a local implementation: its state members and methods.
func exImpl(members map[string]chord.Watchable[delta.JsonValue], methods map[string]contracts.MethodFunction) contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{State: members, Methods: methods}}
}

// exVariant is a remote implementation of variant.
func exVariant(variant string, members map[string]chord.Watchable[delta.JsonValue], methods map[string]contracts.MethodFunction) contracts.Implementation {
	return contracts.Implementation{Variant: variant, Members: contracts.VariantImplementation{State: members, Methods: methods}}
}

// exConstant is `constant(value)`.
func exConstant(value delta.JsonValue) chord.Watchable[delta.JsonValue] {
	return Expose(state.NewConstant(value))
}

// exParam is `params.<name>` as a string.
func exParam(params *delta.JsonObject, name string) string {
	value, _ := params.Value(name).(string)
	return value
}

// exGet is `await host.get(Type, params, context)`.
func exGet(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	reference, err := host.Get(background, typ, params, GetOptions{})
	must(t, err)
	return reference
}

// exHold is `host.hold(Type, params, context)`.
func exHold(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	reference, err := host.Hold(background, typ, params, GetOptions{})
	must(t, err)
	return reference
}

// exValue is `ref.<member>.value`.
func exValue(t testing.TB, reference *Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := reference.Member(member).Value()
	must(t, err)
	return value
}

// exVariantName is `ref.variant`.
func exVariantName(reference *Reference) string {
	variant, _, _ := reference.Variant()
	return variant
}

// exCall is `await ref.<method>(args, ctx)`.
func exCall(t testing.TB, reference *Reference, method string, args any) any {
	t.Helper()
	result, err := reference.Call(method, args, background)
	must(t, err)
	return result
}

// exOpenHost is `createHost({ onError })` loaded with one facet; the errors collected are the ones Pi prints with
// `console.error`, and the test fails on any at its end.
func exOpenHost(t *testing.T, options loadedOptions, setup func(host FacetHost, self FacetSelf) error) (Host, *hostErrors) {
	t.Helper()
	host, errs := loaded(t, setup, options)
	t.Cleanup(func() {
		if errs.Len() > 0 {
			t.Errorf("background errors: %v", errs.All())
		}
	})
	return host, errs
}

// exCode asserts that err is a ChordError of code (and reason when not empty).
func exCode(t testing.TB, err error, code data.ChordErrorCode, reason data.ChordErrorReason) *data.ChordError {
	t.Helper()
	chordErr, ok := data.IsChordErrorCode(err, code)
	if !ok {
		t.Fatalf("err = %v, want a ChordError %s/%s", err, code, reason)
	}
	if reason != "" && chordErr.Reason != reason {
		t.Fatalf("err reason = %q, want %q (%s)", chordErr.Reason, reason, chordErr.Message)
	}
	return chordErr
}

// exTab is a tab host on a link the server accepts as principal.
type exTab struct {
	Host   Host
	Link   *link.MemoryLink
	Errors *hostErrors
}

// exAccept is Pi's `tab(principal)`: a host connected to a server over a new memory link, which the server accepts as
// principal; it returns the tab and the server's end of the link.
func exAccept(t *testing.T, server Host, principal data.Principal) *exTab {
	t.Helper()
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: errs.push})
	mustLoad(t)(host.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: principal}, background))
	must(t, host.Connect(b, LinkOptions{Principal: serverPrincipal()}, background))
	t.Cleanup(func() {
		if errs.Len() > 0 {
			t.Errorf("tab background errors: %v", errs.All())
		}
	})
	return &exTab{Host: host, Link: a, Errors: errs}
}

// exServer is a server host at Server() acting as `server`, loaded with facets.
func exServer(t *testing.T, revokedBy RevokedBy[delta.JsonValue], facets ...HostFacet) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), RevokedBy: revokedBy, OnError: errs.push})
	mustLoad(t)(server.Load(facets, background))
	t.Cleanup(func() {
		if errs.Len() > 0 {
			t.Errorf("server background errors: %v", errs.All())
		}
	})
	return server, errs
}

// exCollector collects strings from callbacks on other goroutines.
type exCollector struct {
	mu    sync.Mutex
	items []string
}

func (c *exCollector) add(item string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, item)
}

func (c *exCollector) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.items...)
}

// sawFrame reports whether a collected item contains part.
func (c *exCollector) sawFrame(part string) bool {
	return slices.ContainsFunc(c.all(), func(item string) bool { return strings.Contains(item, part) })
}

// exFrames is a watch started on `watch` that records its calls as `value ops`.
type exFrames struct {
	mu     sync.Mutex
	frames []exFrame
}

type exFrame struct {
	Kind    string
	Value   any
	Ops     []delta.Op
	Retired bool
	Error   error
}

func (f *exFrames) all() []exFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]exFrame(nil), f.frames...)
}

// exWatch is `watch.start(async (call) => frames.push(call))` of a state member.
func exWatch(t testing.TB, ctx context.Context, source JSONSource) (chord.WatchHandle[delta.JsonValue], *exFrames) {
	t.Helper()
	watch, err := source.Watch(ctx)
	must(t, err)
	frames := &exFrames{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		frames.mu.Lock()
		defer frames.mu.Unlock()
		frames.frames = append(frames.frames, exFrame{Kind: string(call.Kind), Value: call.Value, Ops: call.Ops, Retired: call.Retired, Error: call.Error})
		return nil
	})
	return watch, frames
}

// exNotesDoc is the `notes` document of the examples: `{ list: arr(obj({ text: str() })) }`.
var exNotesDoc = exDefineState("notes", documents.StateOptions{
	Schema:  exObj(exProp("list", exArr(exObj(exProp("text", exStr()))))),
	Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
})

// exStringListDoc is `{ list: arr(str()) }` under name.
func exStringListDoc(name string) *documents.StateDefinition {
	return exDefineState(name, documents.StateOptions{
		Schema:  exObj(exProp("list", exArr(exStr()))),
		Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
	})
}

// exPush is `draft.list.push(item)`.
func exPush(draft *delta.Object, item any) { _, _ = draft.Array("list").Push(item) }

// exAdd is `await doc.change((draft) => void draft.list.push(item), ctx); return doc.value.list.length`. Pi reads
// `doc.value` in the continuation of the await, on the event loop, where the change it awaited is the last one adopted.
// A Go handler resumes on a goroutine of its own, outside a section (a document's value is read from a section), where
// a concurrent call's commit may be adopted first. The length the change left is what its `push` returns, read in the
// change's own step on the line.
func exAdd(doc *singletonDoc, item any, ctx context.Context) (delta.JsonValue, error) {
	var length int
	if err := doc.Change(func(draft *delta.Object) { length, _ = draft.Array("list").Push(item) }, ctx); err != nil {
		return nil, err
	}
	return float64(length), nil
}

// exCloseHost is `await host.close(context)`.
func exCloseHost(t testing.TB, host Host) {
	t.Helper()
	must(t, host.Close(background))
}
