package host

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
	"github.com/MichaelKinsy/PiG/chord/output"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
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
	exStr   = contracts.Str
	exInt   = contracts.Int
	exBool  = contracts.Bool
	exObj   = contracts.Obj
	exArr   = contracts.Arr
	exOpt   = contracts.Opt
	exProp  = contracts.Prop
	exBytes = contracts.Bytes
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
	reference, err := host.Get(typ, params, background, GetOptions{})
	must(t, err)
	return reference
}

// exHold is `host.hold(Type, params, context)`.
func exHold(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	reference, err := host.Hold(typ, params, background, GetOptions{})
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
	must(t, host.Load(nil, background))
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
	must(t, server.Load(facets, background))
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

// exCloseHost is `await host.close(context)`.
func exCloseHost(t testing.TB, host Host) {
	t.Helper()
	must(t, host.Close(background))
}

// --- 01-objects.ts -------------------------------------------------------------------------------------------------

func TestExample01Objects(t *testing.T) {
	greeter := exLocal("example.greeter", contracts.Params{exProp("language", exStr())},
		contracts.Mem("language", exStr()),
		contracts.Mem("greet", contracts.NewMethod(exObj(exProp("name", exStr())), exStr())))
	clock := exLocal("example.clock", contracts.Params{}, contracts.Mem("started", exInt()))
	var created exCollector
	host, _ := exOpenHost(t, loadedOptions{}, func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(clock, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			created.add("clock: created at load")
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"started": exConstant(float64(time.Now().UnixMilli()))}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(greeter, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			language := exParam(params, "language")
			created.add("greeter " + language + ": created by its first get")
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"language": exConstant(language)}, map[string]contracts.MethodFunction{
				"greet": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					if language == "de" {
						return "Hallo " + exParam(args, "name"), nil
					}
					return "Hello " + exParam(args, "name"), nil
				}),
			}), nil
		}, ServeOptions{})
	})
	if got := created.all(); !reflect.DeepEqual(got, []string{"clock: created at load"}) {
		t.Fatalf("after load: %v", got)
	}
	german := exGet(t, host, greeter, jsonObject("language", "de"))
	exEqual(t, "language", exValue(t, german, "language"), "de")
	exEqual(t, "greet", exCall(t, german, "greet", jsonObject("name", "Mario")), "Hallo Mario")
	if got := created.all(); !reflect.DeepEqual(got, []string{"clock: created at load", "greeter de: created by its first get"}) {
		t.Fatalf("created: %v", got)
	}
	// Arguments are checked against the contract at the object.
	_, err := german.Call("greet", jsonObject("name", float64(42)), background)
	refused := exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	if refused.Message == "" {
		t.Fatal("refused: empty message")
	}
	// The same params share one object; releasing the last reference ends it.
	again := exGet(t, host, greeter, jsonObject("language", "de"))
	if len(created.all()) != 2 {
		t.Fatalf("the same params made a second object: %v", created.all())
	}
	german.Release()
	again.Release()
	_, err = german.Member("language").Watch(background)
	_ = exCode(t, err, data.CodeReleased, "")
	exCloseHost(t, host)
}

// --- 02-documents.ts -----------------------------------------------------------------------------------------------

func TestExample02Documents(t *testing.T) {
	item := exObj(exProp("id", exInt()), exProp("text", exStr()))
	todosDoc := exDefineState("todos", documents.StateOptions{
		Schema:  exObj(exProp("items", exArr(item)), exProp("next", exInt())),
		Initial: func() *delta.JsonObject { return jsonObject("items", []any{}, "next", float64(1)) },
	})
	notesDoc := exDefineState("notes", documents.StateOptions{
		Schema:  exObj(exProp("body", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:   true,
	})
	store := exLocal("example.todos", contracts.Params{},
		contracts.Mem("todos", exObj(exProp("items", exArr(item)), exProp("next", exInt()))),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr()), exProp("note", exOpt(exStr()))), exInt())),
		contracts.Mem("note", contracts.NewMethod(exObj(exProp("id", exInt())), exStr())),
		contracts.Mem("clear", contracts.NewMethod()))
	host, _ := exOpenHost(t, loadedOptions{}, func(host FacetHost, _ FacetSelf) error {
		return host.Serve(store, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			todos := stateOf(self, todosDoc)
			notes := self.State(notesDoc).(*documents.Keyed)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"todos": todos.Member()}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					// One transaction over two documents: both change together, or neither does.
					id, err := self.Tx(func(tx *documents.Transaction) (any, error) {
						draft, err := tx.Draft(todos.s)
						if err != nil {
							return nil, err
						}
						id := draft.Get("next").(float64)
						_ = draft.Set("next", id+1)
						_, _ = draft.Array("items").Push(jsonObject("id", id, "text", args.Value("text")))
						if note, ok := args.Get("note"); ok && note != nil {
							noteDraft, err := tx.Draft(notes, fmt.Sprint(int(id)))
							if err != nil {
								return nil, err
							}
							_ = noteDraft.Set("body", note)
						}
						return id, nil
					}, ctx)
					return id, err
				}),
				"note": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					body, err := notes.Get(fmt.Sprint(int(args.Value("id").(float64))), ctx)
					if err != nil || body == nil {
						return "(none)", err
					}
					return body.Value("body"), nil
				}),
				"clear": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := todos.Retire(ctx); err != nil {
						return nil, err
					}
					keys := notes.Keys(ctx)
					defer keys.Close()
					for {
						key, ok, err := keys.Next(ctx)
						if err != nil {
							return nil, err
						}
						if !ok {
							return nil, nil
						}
						if err := notes.Retire(key, ctx); err != nil {
							return nil, err
						}
					}
				}),
			}), nil
		}, ServeOptions{})
	})
	todosRef := exGet(t, host, store, jsonObject())
	first := exCall(t, todosRef, "add", jsonObject("text", "buy milk", "note", "the oat one"))
	exCall(t, todosRef, "add", jsonObject("text", "call Anna"))
	exEqual(t, "todos", exValue(t, todosRef, "todos").(*delta.JsonObject).Value("items"), exJSON(t, `[{"id":1,"text":"buy milk"},{"id":2,"text":"call Anna"}]`))
	exEqual(t, "first", first, float64(1))
	exEqual(t, "note", exCall(t, todosRef, "note", jsonObject("id", first)), "the oat one")
	exCall(t, todosRef, "clear", nil)
	exEqual(t, "after clear", exValue(t, todosRef, "todos"), exJSON(t, `{"items":[],"next":1}`))
	exEqual(t, "note after clear", exCall(t, todosRef, "note", jsonObject("id", first)), "(none)")
	exCloseHost(t, host)
}

// --- 03-derives.ts -------------------------------------------------------------------------------------------------

func TestExample03Derives(t *testing.T) {
	notes := exLocal("example.notes", contracts.Params{},
		contracts.Mem("titles", exArr(exStr())), contracts.Mem("count", exInt()),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr())))))
	badge := exLocal("example.badge", contracts.Params{exProp("user", exStr())}, contracts.Mem("label", exStr()))
	var computed exCollector
	host, _ := exOpenHost(t, loadedOptions{}, func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, exNotesDoc)
			// mapShared calls its function once per note object.
			title := state.MapShared(func(note any) any {
				text := note.(*delta.JsonObject).Value("text").(string)
				computed.add("title computed for: " + text)
				if len(text) > 12 {
					text = text[:12]
				}
				return text
			})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"titles": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					list := values[0].(*delta.JsonObject).Value("list").([]any)
					titles := title(list)
					out := make([]any, len(titles))
					copy(out, titles)
					return out, nil
				}),
				"count": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return float64(len(values[0].(*delta.JsonObject).Value("list").([]any))), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { exPush(draft, jsonObject("text", args.Value("text"))) }, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		// A derive over another object's member, through a reference declared with self.get.
		return host.Serve(badge, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			user := exParam(params, "user")
			count := self.Get(notes, jsonObject())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"label": self.Derive([]any{count.Member("count")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%s: %d notes", user, int(values[0].(float64))), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	})
	notesRef := exGet(t, host, notes, jsonObject())
	badgeRef := exGet(t, host, badge, jsonObject("user", "mario"))
	exCall(t, notesRef, "add", jsonObject("text", "buy milk and bread"))
	exCall(t, notesRef, "add", jsonObject("text", "call Anna"))
	// Nobody watches `titles`, so the commits only marked it; reading computes it, each title once.
	exEqual(t, "titles", exValue(t, notesRef, "titles"), exJSON(t, `["buy milk and","call Anna"]`))
	exCall(t, notesRef, "add", jsonObject("text", "water plants"))
	exEqual(t, "titles", exValue(t, notesRef, "titles"), exJSON(t, `["buy milk and","call Anna","water plants"]`))
	exEqual(t, "badge", exValue(t, badgeRef, "label"), "mario: 3 notes")
	want := []string{"title computed for: buy milk and bread", "title computed for: call Anna", "title computed for: water plants"}
	if got := computed.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("title computations = %v, want %v", got, want)
	}
	exCloseHost(t, host)
}

// --- 04-watches.ts -------------------------------------------------------------------------------------------------

func TestExample04Watches(t *testing.T) {
	countDoc := exDefineState("count", documents.StateOptions{
		Schema:  exObj(exProp("n", exInt())),
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	})
	counter := exLocal("example.counter", contracts.Params{},
		contracts.Mem("count", exObj(exProp("n", exInt()))), contracts.Mem("double", exInt()), contracts.Mem("half", exInt()),
		contracts.Mem("bump", contracts.NewMethod()), contracts.Mem("reset", contracts.NewMethod()))
	var reported exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		if err.Kind == ErrDeriveFailed {
			reported.add(fmt.Sprintf("reported: %s.%s: %v", err.Object, err.Member, err.Error))
			return
		}
		errs.push(err)
	}})
	must(t, host.Load([]HostFacet{{Name: "@example/counter", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(counter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			count := stateOf(self, countDoc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"count": count.Member(),
				"double": self.Derive([]any{count.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil
				}),
				"half": self.Derive([]any{count.s}, func(values []any) (delta.JsonValue, error) {
					n := values[0].(*delta.JsonObject).Value("n").(float64)
					if int(n)%2 == 1 {
						return nil, fmt.Errorf("%d has no half", int(n))
					}
					return n / 2, nil
				}),
			}, map[string]contracts.MethodFunction{
				"bump": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, count.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
				}),
				"reset": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, count.Retire(ctx)
				}),
			}), nil
		}, ServeOptions{})
	})}}, background))
	counterRef := exGet(t, host, counter, jsonObject())
	// A watch holds the value at acquisition; a listener that keeps up gets one call per commit.
	watch, countFrames := exWatch(t, background, counterRef.Member("count"))
	exEqual(t, "acquired", watch.Value(), exJSON(t, `{"n":0}`))
	cancellable, cancel := chordctx.WithCancel(background)
	doubled, doubleFrames := exWatch(t, cancellable, counterRef.Member("double"))
	half, halfFrames := exWatch(t, background, counterRef.Member("half"))
	// A slow listener: commits made while it is busy fold into its next call.
	slow, err := counterRef.Member("count").Watch(background)
	must(t, err)
	slowStarted := make(chan struct{}, 1)
	slowRelease := make(chan struct{})
	var slowFrames exFrames
	slow.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		slowFrames.mu.Lock()
		slowFrames.frames = append(slowFrames.frames, exFrame{Kind: "value", Value: call.Value, Ops: call.Ops})
		first := len(slowFrames.frames) == 1
		slowFrames.mu.Unlock()
		if first {
			slowStarted <- struct{}{}
			<-slowRelease
		}
		return nil
	})
	exCall(t, counterRef, "bump", nil)
	<-slowStarted
	exCall(t, counterRef, "bump", nil)
	exCall(t, counterRef, "bump", nil)
	close(slowRelease)
	// Retiring the singleton resets it to initial(): watches get a root replacement.
	exCall(t, counterRef, "reset", nil)
	scopeEventually(t, "the frames of the three bumps and the reset", func() bool {
		return len(countFrames.all()) == 4 && len(slowFrames.all()) >= 2 && len(doubleFrames.all()) >= 4
	})
	want := []string{
		`{"n":1} [["s",["n"],1]]`, `{"n":2} [["s",["n"],2]]`, `{"n":3} [["s",["n"],3]]`, `{"n":0} [["r",{"n":0}]]`,
	}
	got := []string{}
	for _, frame := range countFrames.all() {
		got = append(got, exText(frame.Value)+" "+exText(frame.Ops))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("count frames = %v, want %v", got, want)
	}
	// The slow listener's second call folds the commits made while it was busy: the newest value, one op on one path.
	slowAll := slowFrames.all()
	exEqual(t, "slow first frame", slowAll[0].Value, exJSON(t, `{"n":1}`))
	exEqual(t, "slow folded frame", slowAll[1].Value, exJSON(t, `{"n":3}`))
	exEqual(t, "slow folded ops", slowAll[1].Ops, exJSON(t, `[["s",["n"],3]]`))
	// A failing derive: one `failed` call per failure, the watch stays open; the next value replaces the root.
	scopeEventually(t, "the half frames", func() bool { return len(halfFrames.all()) >= 4 })
	var halfKinds []string
	for _, frame := range halfFrames.all() {
		if frame.Kind == string(chord.WatchCallFailed) {
			halfKinds = append(halfKinds, "failed: "+frame.Error.Error())
		} else {
			halfKinds = append(halfKinds, "frame "+exText(frame.Value))
		}
	}
	wantHalf := []string{"failed: 1 has no half", "frame 1", "failed: 3 has no half", "frame 0"}
	if !reflect.DeepEqual(halfKinds, wantHalf) {
		t.Fatalf("half calls = %v, want %v", halfKinds, wantHalf)
	}
	if got := reported.all(); !reflect.DeepEqual(got, []string{
		"reported: example.counter.half: 1 has no half", "reported: example.counter.half: 3 has no half",
	}) {
		t.Fatalf("derive_failed reports = %v", got)
	}
	// Cancelling the watch's context ends it; releasing the reference ends the watches taken through it.
	cancel(nil)
	<-doubled.Closed()
	counterRef.Release()
	<-watch.Closed()
	_ = half
	_ = slow
	exCloseHost(t, host)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

// --- 05-errors.ts --------------------------------------------------------------------------------------------------

func TestExample05Errors(t *testing.T) {
	echo := exLocal("example.echo", contracts.Params{exProp("id", exInt())},
		contracts.Mem("id", exInt()), contracts.Mem("echo", contracts.NewMethod(exObj(exProp("text", exStr())), exStr())))
	ping := exLocal("example.ping", contracts.Params{}, contracts.Mem("n", exInt()))
	pong := exLocal("example.pong", contracts.Params{}, contracts.Mem("n", exInt()))
	broken := exLocal("example.broken", contracts.Params{exProp("id", exInt())}, contracts.Mem("n", exInt()))
	sloppy := exLocal("example.sloppy", contracts.Params{exProp("id", exInt())}, contracts.Mem("n", exInt()))
	var reported exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		errs.push(err)
		if err.Kind == ErrActivationFailed {
			reported.add(fmt.Sprintf("reported: %s failed %s", err.Object, err.Failure.Kind))
		} else {
			reported.add("reported: " + string(err.Kind))
		}
	}})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(echo, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"id": exConstant(params.Value("id"))}, map[string]contracts.MethodFunction{
				"echo": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					return args.Value("text"), nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(broken, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("broken: missing configuration")
		}, ServeOptions{}); err != nil {
			return err
		}
		// Chord checks the implementation's members when the object activates.
		return h.Serve(sloppy, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": exConstant(float64(1)), "extra": exConstant(float64(2))}, nil), nil
		}, ServeOptions{})
	})
	// Ping and Pong are served by a second plugin: types with empty params activate at load.
	cycle := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(ping, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(pong, jsonObject()).Member("n"))}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(pong, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(ping, jsonObject()).Member("n"))}, nil), nil
		}, ServeOptions{})
	})
	// A dependency cycle fails every object on it broken, with invalid/cycle; load still resolves.
	must(t, host.Load([]HostFacet{{Name: "@example/errors", Facet: facet}, {Name: "@example/cycle", Facet: cycle}}, background))
	if got := reported.all(); len(got) == 0 || got[0] != "reported: example.ping failed broken" && got[0] != "reported: example.pong failed broken" {
		t.Fatalf("cycle reports = %v", got)
	}
	_, err := host.Get(ping, jsonObject(), background, GetOptions{})
	_ = exCode(t, err, data.CodeInvalid, data.ReasonCycle)
	echoRef := exGet(t, host, echo, jsonObject("id", float64(1)))
	// Arguments are checked against the contract at the object: wrong types and unknown fields are invalid/args.
	_, err = echoRef.Call("echo", jsonObject("text", float64(1)), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	_, err = echoRef.Call("echo", jsonObject("text", "hi", "extra", true), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	// Params select the object and are checked on every get: invalid/params.
	_, err = host.Get(echo, jsonObject("id", "one"), background, GetOptions{})
	_ = exCode(t, err, data.CodeInvalid, data.ReasonParams)
	// An implementation with a member its contract does not declare fails activation.
	_, err = host.Get(sloppy, jsonObject("id", float64(1)), background, GetOptions{})
	if err == nil {
		t.Fatal("unknown member: ok")
	}
	// A get needs the provider's version and fingerprint; another declaration does not fit: contract_mismatch after readyTimeout.
	echoV2 := mustContract(contracts.Local("example.echo", contracts.LocalDeclaration{
		Version: 2, Params: contracts.Params{exProp("id", exInt())}, Members: []contracts.LocalEntry{contracts.Mem("id", exInt())},
	}))
	readyTimeout := 100 * time.Millisecond
	_, err = host.Get(echoV2, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &readyTimeout})
	_ = exCode(t, err, data.CodeContractMismatch, "")
	// A factory that throws fails broken: the get rejects at once with its error, and no object is left behind.
	_, err = host.Get(broken, jsonObject("id", float64(1)), background, GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "broken: missing configuration") {
		t.Fatalf("throwing factory: %v", err)
	}
	// After release, every use of a reference throws released.
	echoRef.Release()
	_, err = echoRef.Call("echo", jsonObject("text", "hi"), background)
	_ = exCode(t, err, data.CodeReleased, "")
	exCloseHost(t, host)
	_, err = host.Get(echo, jsonObject("id", float64(1)), background, GetOptions{})
	_ = exCode(t, err, data.CodeReleased, "")
}

// exNumber is a stored integer as the JSON number Pi's row has.
func exNumber(value any) any {
	if number, ok := value.(int64); ok {
		return float64(number)
	}
	return value
}

// exLoop is the loop of the host a handler's ctx belongs to: what `Awaiting` takes.
func exLoop(ctx Ctx) sectionLoop { return ctx.(*callContext).host.loop() }

// exCtx is the handler's `ctx` as Chord's Ctx.
func exCtx(ctx context.Context) Ctx {
	handler, ok := ctx.(Ctx)
	if !ok {
		panic(fmt.Sprintf("a handler's context is a Ctx, got %T", ctx))
	}
	return handler
}

// exBlocked is a method function that waits until its context is aborted, and then reports why through onAbort.
func exBlocked(onAbort func(cause error)) contracts.MethodFunction {
	return func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		// Pi: `await untilAborted(ctx)`.
		UntilAborted(ctx)
		if onAbort != nil {
			onAbort(context.Cause(ctx))
		}
		return nil, nil
	}
}

// --- 06-natives.ts -------------------------------------------------------------------------------------------------

func TestExample06Natives(t *testing.T) {
	clock := exLocal("example.clock", contracts.Params{exProp("zone", exStr())},
		contracts.Mem("zone", exStr()), contracts.Mem("now", contracts.NewMethod(contracts.TSchema{}, exStr())))
	leaky := exLocal("example.leaky", contracts.Params{}, contracts.Mem("n", exInt()))
	var logs exCollector
	var ticks sync.Map
	var reported exCollector
	var reportedErrors []HostError
	var reportedMu sync.Mutex
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		reportedMu.Lock()
		reportedErrors = append(reportedErrors, err)
		reportedMu.Unlock()
		reported.add(string(err.Kind) + " " + err.Object)
	}})
	_ = errs
	must(t, host.Load([]HostFacet{{Name: "@example/natives", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(clock, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			zone := exParam(params, "zone")
			// `self.open(open, close)` declares a native; it opens after the references are live, in declaration order.
			config := self.Open(func(context.Context) (any, error) {
				logs.add(zone + ": open config")
				return "[" + zone + "]", nil
			}, func(_ any, reason NativeCloseReason, _ context.Context) error {
				logs.add(fmt.Sprintf("%s: close config (%s)", zone, reason.Kind))
				return nil
			})
			ticker := self.Open(func(context.Context) (any, error) {
				prefix, _ := config.Value()
				logs.add(fmt.Sprintf("%s: open ticker, config %v", zone, prefix))
				counter := &exTicker{}
				ticks.Store(zone, counter)
				counter.start()
				return counter, nil
			}, func(value any, reason NativeCloseReason, _ context.Context) error {
				value.(*exTicker).stop()
				logs.add(fmt.Sprintf("%s: close ticker (%s)", zone, reason.Kind))
				return nil
			})
			// The factory only declares: a value is there once its open resolved.
			if _, err := config.Value(); err != nil {
				chordErr := exCode(t, err, data.CodeNotReady, "")
				logs.add(zone + ": in the factory, " + chordErr.Message)
			}
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"zone": exConstant(zone)}, map[string]contracts.MethodFunction{
				"now": exMethod(func(*delta.JsonObject, context.Context) (delta.JsonValue, error) {
					prefix, err := config.Value()
					if err != nil {
						return nil, err
					}
					counter, err := ticker.Value()
					if err != nil {
						return nil, err
					}
					if counter.(*exTicker).count() > 0 {
						return fmt.Sprintf("%v ticking", prefix), nil
					}
					return fmt.Sprintf("%v starting", prefix), nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		// Closes that throw or hang: they never stop the other closes.
		return h.Serve(leaky, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return "socket", nil }, func(any, NativeCloseReason, context.Context) error {
				return errors.New("socket close failed")
			})
			closeTimeout := 50 * time.Millisecond
			self.Open(func(context.Context) (any, error) { return "worker", nil }, func(any, NativeCloseReason, context.Context) error {
				select {} // never settles
			}, NativeOptions{CloseTimeout: &closeTimeout})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": exConstant(float64(1))}, nil), nil
		}, ServeOptions{})
	})}}, background))
	vienna := exGet(t, host, clock, jsonObject("zone", "vienna"))
	exEqual(t, "now", exCall(t, vienna, "now", nil), "[vienna] starting")
	scopeEventually(t, "the ticker ticks", func() bool {
		result, err := vienna.Call("now", nil, background)
		return err == nil && result == "[vienna] ticking"
	})
	vienna.Release()
	scopeEventually(t, "vienna's natives closed", func() bool {
		all := strings.Join(logs.all(), "\n")
		return strings.Contains(all, "vienna: close ticker (end)") && strings.Contains(all, "vienna: close config (end)")
	})
	exGet(t, host, clock, jsonObject("zone", "tokyo"))
	err := host.Close(background)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 1 || joined.Unwrap()[0].Error() != "socket close failed" {
		t.Fatalf("host.close = %v, want an aggregate of [socket close failed]", err)
	}
	all := logs.all()
	wantStart := []string{
		"vienna: in the factory, " + all[0][len("vienna: in the factory, "):], "vienna: open config", "vienna: open ticker, config [vienna]",
	}
	if len(all) < 3 || !strings.HasPrefix(all[0], "vienna: in the factory, ") || !reflect.DeepEqual(all[1:3], wantStart[1:]) {
		t.Fatalf("vienna's start = %v", all)
	}
	joinedLogs := strings.Join(all, "\n")
	for _, line := range []string{"tokyo: close ticker (close)", "tokyo: close config (close)"} {
		if !strings.Contains(joinedLogs, line) {
			t.Fatalf("missing %q in %v", line, all)
		}
	}
	got := reported.all()
	// A close that threw is returned by `host.close` (the aggregate above), not reported; only the timeout is.
	if !reflect.DeepEqual(got, []string{"close_timeout example.leaky"}) {
		t.Fatalf("reported = %v", got)
	}
}

// exTicker is Pi's `{ ticks, timer: setInterval(..., 10) }`.
type exTicker struct {
	mu    sync.Mutex
	ticks int
	timer *time.Ticker
	done  chan struct{}
}

func (e *exTicker) start() {
	e.timer = time.NewTicker(10 * time.Millisecond)
	e.done = make(chan struct{})
	go func() {
		for {
			select {
			case <-e.timer.C:
				e.mu.Lock()
				e.ticks++
				e.mu.Unlock()
			case <-e.done:
				return
			}
		}
	}()
}

func (e *exTicker) stop() {
	e.timer.Stop()
	close(e.done)
}

func (e *exTicker) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ticks
}

// --- 07-failures.ts ------------------------------------------------------------------------------------------------

func TestExample07Failures(t *testing.T) {
	remote := exLocal("example.remote", contracts.Params{}, contracts.Mem("status", exStr()))
	inbox := exLocal("example.inbox", contracts.Params{exProp("user", exStr())}, contracts.Mem("status", exStr()))
	mirror := exLocal("example.mirror", contracts.Params{exProp("id", exInt())}, contracts.Mem("status", exStr()))
	stuck := exLocal("example.stuck", contracts.Params{exProp("id", exInt())}, contracts.Mem("status", exStr()))
	var attempts int
	var reported exCollector
	errs := &hostErrors{}
	readyTimeout := 500 * time.Millisecond
	host := newHost(t, HostOptions{ReadyTimeout: &readyTimeout, OnError: func(err HostError) {
		if err.Kind == ErrActivationFailed {
			reported.add(fmt.Sprintf("%s, %s: %s", err.Object, err.Failure.Kind, err.Failure.Error.Message))
			return
		}
		errs.push(err)
	}})
	must(t, host.Load([]HostFacet{{Name: "@example/failures", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(remote, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			attempts++
			attempt := attempts
			self.Open(func(context.Context) (any, error) {
				// A throwing open is transient: Chord retries it after 1 s, then 2 s, 4 s, ... up to 60 s.
				if attempt <= 2 {
					return nil, errors.New("connection refused")
				}
				return true, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("connected")}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(inbox, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			user := exParam(params, "user")
			// A dependency that fails parks this object (`failed { waiting }`); it goes on once the dependency is live.
			remoteRef := self.Get(remote, jsonObject())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"status": self.Derive([]any{remoteRef.Member("status")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%s: %v", user, values[0]), nil
				}),
			}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(mirror, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				// broken(error) says retrying is pointless: no retry, and getters are rejected at once.
				return nil, data.Broken(errors.New("no mirror configured"))
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("mirroring")}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(stuck, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(ctx context.Context) (any, error) { <-ctx.Done(); return nil, context.Cause(ctx) }, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("never")}, nil), nil
		}, ServeOptions{})
	})}}, background))
	// The mirror: a broken open rejects the get at once.
	_, err := host.Get(mirror, jsonObject("id", float64(1)), background, GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "no mirror configured") {
		t.Fatalf("mirror: %v", err)
	}
	// The stuck native never opens: the attempt times out at readyTimeout; the get (700 ms) rejects with the cause.
	stuckTimeout := 700 * time.Millisecond
	_, err = host.Get(stuck, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &stuckTimeout})
	if err == nil {
		t.Fatal("stuck: ok")
	}
	// The inbox waits through the remote's transient failures (1 s, then 2 s) and goes live: a getter's deadline of 10 s.
	inboxTimeout := 10 * time.Second
	reference, err := host.Get(inbox, jsonObject("user", "mario"), background, GetOptions{ReadyTimeout: &inboxTimeout})
	must(t, err)
	exEqual(t, "inbox status", exValue(t, reference, "status"), "mario: connected")
	if attempts != 3 {
		t.Fatalf("remote attempts = %d, want 3", attempts)
	}
	got := reported.all()
	for _, want := range []string{
		"example.mirror(1), broken: example.mirror(1): no mirror configured",
		"example.remote, transient: example.remote: connection refused",
	} {
		if !slices_Contains(got, want) {
			t.Fatalf("reported %v lacks %q", got, want)
		}
	}
	exCloseHost(t, host)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

func slices_Contains(list []string, item string) bool {
	return slices.Contains(list, item)
}

// exNotesVariants is 08-remote.ts's variants: an editor with `add`, a reader without.
func exTwoPartyHosts(t *testing.T, server, client Host, principal data.Principal) *link.MemoryLink {
	t.Helper()
	up, down := link.MemoryLinkPair()
	must(t, server.Accept(up, LinkOptions{Principal: principal}, background))
	must(t, client.Connect(down, LinkOptions{Principal: serverPrincipal()}, background))
	return up
}

// --- 08-remote.ts --------------------------------------------------------------------------------------------------

func TestExample08Remote(t *testing.T) {
	note := exObj(exProp("text", exStr()), exProp("author", exStr()))
	publicNote := exObj(exProp("text", exStr()))
	notes := exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exServerAddress,
		contracts.Var("editor", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("notes", exArr(note)), exProp("count", exInt())},
			Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr()))))},
		}),
		contracts.Var("reader", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("notes", exArr(publicNote)), exProp("count", exInt())},
		}))
	noteStore := exLocal("example.note-store", contracts.Params{exProp("session", exStr())},
		contracts.Mem("notes", exArr(note)), contracts.Mem("add", contracts.NewMethod(note)))
	doc := exDefineState("notes", documents.StateOptions{
		Schema:  exObj(exProp("list", exArr(note))),
		Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
	})
	redact := state.MapShared(func(entry any) any { return jsonObject("text", entry.(*delta.JsonObject).Value("text")) })
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(noteStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"notes": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, notesDoc.Change(func(draft *delta.Object) { exPush(draft, args) }, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		// A remote factory gets the principal its object is for and returns one variant with exactly its members.
		return h.Serve(notes, func(params *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error) {
			store := self.Get(noteStore, jsonObject("session", exParam(params, "session")))
			count := self.Derive([]any{store.Member("notes")}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].([]any))), nil
			})
			if principalKind(principal) == "guest" {
				return exVariant("reader", map[string]chord.Watchable[delta.JsonValue]{
					"notes": self.Derive([]any{store.Member("notes")}, func(values []any) (delta.JsonValue, error) {
						list := values[0].([]any)
						out := redact(list)
						result := make([]any, len(out))
						copy(result, out)
						return result, nil
					}),
					"count": count,
				}, nil), nil
			}
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{"notes": Expose(store.Member("notes")), "count": count},
				map[string]contracts.MethodFunction{
					"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return store.Call("add", jsonObject("text", args.Value("text"), "author", principalKind(principal)), ctx)
					}),
				}), nil
		}, ServeOptions{})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/notes", Facet: facet})
	clientErrors := &hostErrors{}
	client := newHost(t, HostOptions{OnError: clientErrors.push})
	exTwoPartyHosts(t, server, client, guestPrincipal("S1"))
	must(t, client.Load(nil, background))
	own := exGet(t, server, notes, jsonObject("session", "S1"))
	if exVariantName(own) != "editor" {
		t.Fatalf("the server's variant = %q", exVariantName(own))
	}
	exCall(t, own, "add", jsonObject("text", "hello"))
	guest := exGet(t, client, notes, jsonObject("session", "S1"))
	if exVariantName(guest) != "reader" {
		t.Fatalf("guest variant = %q, want reader", exVariantName(guest))
	}
	exEqual(t, "guest notes", exValue(t, guest, "notes"), exJSON(t, `[{"text":"hello"}]`))
	exEqual(t, "guest count", exValue(t, guest, "count"), float64(1))
	watch, frames := exWatch(t, background, guest.Member("notes"))
	exCall(t, own, "add", jsonObject("text", "world"))
	scopeEventually(t, "the frame of the commit", func() bool { return len(frames.all()) == 1 })
	exEqual(t, "frame", frames.all()[0].Value, exJSON(t, `[{"text":"hello"},{"text":"world"}]`))
	scopeEventually(t, "count after the commit", func() bool { return exValue(t, guest, "count") == float64(2) })
	// `add` is not a member of the reader variant.
	_, err := guest.Call("add", jsonObject("text", "x"), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonMember)
	exCloseHost(t, server)
	scopeEventually(t, "the client sees the server gone", func() bool {
		current := guest.State().Value()
		return current.Kind == RefStateLoading
	})
	exEqual(t, "count after the server closed", exValue(t, guest, "count"), float64(2))
	exCloseHost(t, client)
	ended := guest.State().Value()
	if ended.Kind != RefStateEnded {
		t.Fatalf("after the client closed: %s", ended.Kind)
	}
	<-watch.Closed()
	if clientErrors.Len() > 0 {
		t.Fatalf("client errors: %v", clientErrors.All())
	}
}

// --- 09-calls.ts ---------------------------------------------------------------------------------------------------

func TestExample09Calls(t *testing.T) {
	files := exRemote("example.files", contracts.Params{}, exServerAddress, contracts.Var("owner", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{
			contracts.Meth("whoami", contracts.NewMethod(contracts.TSchema{}, exStr())),
			// A `bytes()` leaf: the argument and result travel as raw segments, never base64.
			contracts.Meth("upload", contracts.NewMethod(exObj(exProp("name", exStr()), exProp("data", exBytes())), exInt())),
			contracts.Meth("download", contracts.NewMethod(exObj(exProp("name", exStr())), exBytes())),
			contracts.Meth("quota", contracts.NewMethod()),
			contracts.Meth("slow", contracts.NewMethod()),
		},
	}))
	var stored sync.Map
	aborted := make(chan error, 2)
	// reached receives once per slow call whose handler runs at the server: a call that never reached it sends nothing.
	reached := make(chan struct{}, 2)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(files, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return exVariant("owner", nil, map[string]contracts.MethodFunction{
				// ctx.principal is the caller as its link stamped it; ctx.incarnation the calling host's run (R20).
				"whoami": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					handler := exCtx(ctx)
					return fmt.Sprintf("%s from %s…", principalKind(handler.Principal()), handler.Incarnation()[:8]), nil
				},
				"upload": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					bytes := args.Value("data").([]byte)
					stored.Store(args.Value("name"), bytes)
					return float64(len(bytes)), nil
				}),
				"download": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					if value, ok := stored.Load(args.Value("name")); ok {
						return value.([]byte), nil
					}
					return []byte{}, nil
				}),
				// A PluginError crosses with its code, message and details; any other throw as code `internal`.
				"quota": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return nil, data.NewPluginError("quota", "the store is full", jsonObject("limit", float64(1)))
				},
				// A handler's context is aborted when its caller cancels or releases the reference.
				"slow": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					reached <- struct{}{}
					return exBlocked(func(cause error) { aborted <- cause })(args, ctx)
				},
			}), nil
		}, ServeOptions{})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/files", Facet: facet})
	clientErrors := &hostErrors{}
	client := newHost(t, HostOptions{OnError: clientErrors.push})
	exTwoPartyHosts(t, server, client, ownerPrincipal())
	must(t, client.Load(nil, background))
	reference := exGet(t, client, files, jsonObject())
	whoami := exCall(t, reference, "whoami", nil).(string)
	if !strings.HasPrefix(whoami, "owner from ") || len([]rune(strings.TrimPrefix(whoami, "owner from "))) != 9 || !strings.HasSuffix(whoami, "…") {
		t.Fatalf("whoami = %q", whoami)
	}
	payload := make([]byte, 1024*1024)
	for i := range payload {
		payload[i] = 7
	}
	exEqual(t, "uploaded bytes", exCall(t, reference, "upload", jsonObject("name", "chart.png", "data", payload)), float64(1024*1024))
	back, ok := exCall(t, reference, "download", jsonObject("name", "chart.png")).([]byte)
	if !ok || len(back) != 1024*1024 {
		t.Fatalf("downloaded: %T of %d bytes", back, len(back))
	}
	_, err := reference.Call("upload", jsonObject("name", float64(1), "data", payload), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	_, err = reference.Call("quota", nil, background)
	var plugin *data.PluginError
	if !errors.As(err, &plugin) || plugin.Code != "quota" || plugin.Message != "the store is full" {
		t.Fatalf("quota: %v", err)
	}
	exEqual(t, "plugin details", plugin.Details, exJSON(t, `{"limit":1}`))
	// A cancelled call rejects with the cancel's error and aborts the handler.
	cancellable, cancel := chordctx.WithCancel(background)
	slowDone := make(chan error, 1)
	go func() { _, err := reference.Call("slow", nil, cancellable); slowDone <- err }()
	<-reached
	cancel(errors.New("the user gave up"))
	if err := <-slowDone; err == nil || err.Error() != "the user gave up" {
		t.Fatalf("client: slow rejected: %v", err)
	}
	if cause := <-aborted; cause == nil {
		t.Fatal("server: slow was aborted without a reason")
	}
	// Releasing the reference aborts a pending call.
	pendingDone := make(chan error, 1)
	go func() { _, err := reference.Call("slow", nil, background); pendingDone <- err }()
	// The handler of this call runs. The activity count cannot say so: the first call's may not have drained yet, so a
	// release could come before this call was made and reject it without it reaching the server, and `aborted` never fires.
	<-reached
	reference.Release()
	_ = exCode(t, <-pendingDone, data.CodeReleased, "")
	<-aborted
	exCloseHost(t, client)
	exCloseHost(t, server)
	if clientErrors.Len() > 0 {
		t.Fatalf("client errors: %v", clientErrors.All())
	}
}

// exActivity is `host.activity.value`.
func exActivity(t testing.TB, host Host) ActivityCounts {
	t.Helper()
	var value delta.JsonValue
	var err error
	// Pi reads it inside a section of the host: so does the test.
	host.(*chordHost).enter(func() { value, err = host.Activity().Value() })
	must(t, err)
	object := value.(*delta.JsonObject)
	count := func(key string) int { number, _ := object.Value(key).(float64); return int(number) }
	return ActivityCounts{References: count("references"), Calls: count("calls"), Jobs: count("jobs"), Lingering: count("lingering")}
}

// exStateKinds starts a `ref.state` watch and collects each state as Pi's `describe` prints it: its kind, and for a
// failed one its cause's kind, whether it retries by itself, and its error's message.
func exStateKinds(t testing.TB, reference *Reference) (chord.NativeWatchHandle[*RefState], *exCollector) {
	t.Helper()
	watch, err := reference.State().Watch(background)
	must(t, err)
	collected := &exCollector{}
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		if call.Kind == chord.WatchCallValue {
			collected.add(exDescribe(call.Value))
		}
		return nil
	})
	return watch, collected
}

// exDescribe is a state for the log: a failure with its cause's kind, when it retries by itself, and its error.
func exDescribe(current *RefState) string {
	if current.Kind != RefStateFailed {
		return string(current.Kind)
	}
	retry := "no automatic retry"
	if current.RetryAt != nil {
		retry = "retries"
	}
	return fmt.Sprintf("failed, %s, %s: %s", current.Cause.Kind, retry, current.Cause.Error.Message)
}

// exWaitState waits until `describe(ref.state.value)` is want; it fails with the last state seen.
func exWaitState(t testing.TB, reference *Reference, want string) {
	t.Helper()
	last, logged := "", ""
	scopeEventually(t, "the state "+want, func() bool {
		last = exStateNow(t, reference)
		if last != want && last != logged {
			logged = last
			t.Logf("state is %q", last)
		}
		return last == want
	})
}

// exParked is the number of calls parked on a reference that waits for its object (`parked.size`); read in a section.
func exParked(host Host, reference *Reference) int {
	size := 0
	host.(*chordHost).enter(func() {
		if parked := reference.binding.parked; parked != nil {
			size = parked.size
		}
	})
	return size
}

// exStateNow is `describe(ref.state.value)`.
func exStateNow(t testing.TB, reference *Reference) string {
	t.Helper()
	return exDescribe(reference.State().Value())
}

// --- 10-drops.ts ---------------------------------------------------------------------------------------------------

func TestExample10Drops(t *testing.T) {
	notes := exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exServerAddress, contracts.Var("editor", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("notes", exArr(exStr()))},
		Methods: []contracts.MethodEntry{
			contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt())),
			contracts.Meth("slow", contracts.NewMethod()),
		},
	}))
	doc := exStringListDoc("notes")
	// Lingers for a minute after its last reference: a client that reconnects within it finds its notes again.
	linger := 60 * time.Second
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{
				"notes": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := notesDoc.Change(func(draft *delta.Object) { exPush(draft, args.Value("text")) }, ctx); err != nil {
						return nil, err
					}
					return float64(len(notesDoc.Value().Value("list").([]any))), nil
				}),
				"slow": exBlocked(nil),
			}), nil
		}, ServeOptions{Linger: &linger})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/notes", Facet: facet})
	clientErrors := &hostErrors{}
	client := newHost(t, HostOptions{OnError: clientErrors.push})
	must(t, client.Load(nil, background))
	connect := func() *link.MemoryLink { return exTwoPartyHosts(t, server, client, ownerPrincipal()) }
	held := exHold(t, client, notes, jsonObject("session", "S1"))
	if exStateNow(t, held) != "loading" || exVariantName(held) != "" {
		t.Fatalf("held without a link: %s, variant %q", exStateNow(t, held), exVariantName(held))
	}
	watch, kinds := exStateKinds(t, held)
	type result struct {
		value any
		err   error
	}
	parked := make(chan result, 1)
	go func() {
		value, err := held.Call("add", jsonObject("text", "written while loading"), background)
		parked <- result{value, err}
	}()
	scopeEventually(t, "the call parked", func() bool { return exParked(client, held) == 1 })
	up := connect()
	if got := <-parked; got.err != nil || got.value != float64(1) {
		t.Fatalf("parked call's result once live: %v, %v", got.value, got.err)
	}
	exEqual(t, "notes", exValue(t, held, "notes"), exJSON(t, `["written while loading"]`))
	if exVariantName(held) != "editor" {
		t.Fatalf("variant = %q", exVariantName(held))
	}
	inFlight := make(chan error, 1)
	go func() { _, err := held.Call("slow", nil, background); inFlight <- err }()
	scopeEventually(t, "the call is running on the server", func() bool { return exActivity(t, server).Calls == 1 })
	up.Close()
	if err := <-inFlight; err == nil {
		t.Fatal("in-flight call: resolved")
	}
	scopeEventually(t, "the drop shows as loading", func() bool { return exStateNow(t, held) == "loading" })
	exEqual(t, "last notes", exValue(t, held, "notes"), exJSON(t, `["written while loading"]`))
	first := make(chan result, 1)
	second := make(chan result, 1)
	go func() {
		value, err := held.Call("add", jsonObject("text", "first while down"), background)
		first <- result{value, err}
	}()
	scopeEventually(t, "the first call parked", func() bool { return exParked(client, held) == 1 })
	go func() {
		value, err := held.Call("add", jsonObject("text", "second while down"), background)
		second <- result{value, err}
	}()
	scopeEventually(t, "the second call parked", func() bool { return exParked(client, held) == 2 })
	_, frames := exWatch(t, background, held.Member("notes"))
	connect()
	if got := <-first; got.err != nil || got.value != float64(2) {
		t.Fatalf("first parked result: %v, %v", got.value, got.err)
	}
	if got := <-second; got.err != nil || got.value != float64(3) {
		t.Fatalf("second parked result: %v, %v", got.value, got.err)
	}
	scopeEventually(t, "the re-hold's frames", func() bool { return len(frames.all()) > 0 })
	scopeEventually(t, "notes after the re-hold", func() bool {
		return exJSONEqual(exValue(t, held, "notes"), exJSON(t, `["written while loading","first while down","second while down"]`))
	})
	held.Release()
	<-watch.Closed()
	if got := exStateNow(t, held); got != "ended" {
		t.Fatalf("state after release = %s", got)
	}
	if got := kinds.all(); len(got) == 0 || got[0] != "live" {
		t.Fatalf("ref.state frames = %v", got)
	}
	exCloseHost(t, client)
	exCloseHost(t, server)
	if clientErrors.Len() > 0 {
		t.Fatalf("client errors: %v", clientErrors.All())
	}
}

// --- 11-linger-and-retry.ts ----------------------------------------------------------------------------------------

func TestExample11LingerAndRetry(t *testing.T) {
	draft := exLocal("example.draft", contracts.Params{exProp("conversation", exStr())},
		contracts.Mem("text", exStr()), contracts.Mem("type", contracts.NewMethod(exObj(exProp("text", exStr())))))
	mirror := exLocal("example.mirror", contracts.Params{exProp("id", exInt())}, contracts.Mem("status", exStr()))
	draftDoc := exDefineState("draft", documents.StateOptions{
		Schema:  exObj(exProp("text", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("text", "") },
	})
	var (
		mu         sync.Mutex
		configured bool
		up         bool
		activated  int
	)
	flag := func(f *bool) bool { mu.Lock(); defer mu.Unlock(); return *f }
	setFlag := func(f *bool, v bool) { mu.Lock(); defer mu.Unlock(); *f = v }
	linger := 200 * time.Millisecond
	errs := &hostErrors{}
	var reported exCollector
	host := newHost(t, HostOptions{OnError: func(err HostError) { errs.push(err); reported.add(string(err.Kind)) }})
	must(t, host.Load([]HostFacet{{Name: "@example/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		// 1. A draft lingers 200 ms after its last reference: a tab that closes and reopens quickly finds its text.
		if err := h.Serve(draft, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			activated++
			mu.Unlock()
			doc := stateOf(self, draftDoc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"text": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("text"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"type": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(d *delta.Object) { _ = d.Set("text", args.Value("text")) }, ctx)
				}),
			}), nil
		}, ServeOptions{Linger: &linger}); err != nil {
			return err
		}
		return h.Serve(mirror, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				// `broken`: retrying cannot help until someone changes something (no automatic retry).
				if !flag(&configured) {
					return nil, data.Broken(errors.New("no mirror configured"))
				}
				// A plain throw is transient: retried with backoff (1 s, doubling).
				if !flag(&up) {
					return nil, errors.New("mirror unreachable")
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("mirroring")}, nil), nil
		}, ServeOptions{})
	})}}, background))
	draftRef := exGet(t, host, draft, jsonObject("conversation", "c1"))
	exCall(t, draftRef, "type", jsonObject("text", "hello"))
	draftRef.Release()
	// Within the linger the object is reused with its text.
	draftRef = exGet(t, host, draft, jsonObject("conversation", "c1"))
	exEqual(t, "within linger", exValue(t, draftRef, "text"), "hello")
	mu.Lock()
	if activated != 1 {
		t.Fatalf("activations within the linger = %d", activated)
	}
	mu.Unlock()
	draftRef.Release()
	scopeEventually(t, "the draft lingers", func() bool { return exActivity(t, host).Lingering == 1 })
	scopeEventually(t, "the linger ended", func() bool { return exActivity(t, host).Lingering == 0 })
	draftRef = exGet(t, host, draft, jsonObject("conversation", "c1"))
	exEqual(t, "after linger", exValue(t, draftRef, "text"), "")
	draftRef.Release()
	// 2. The mirror: broken, then transient, then live.
	// Pi's `host.hold(...)` and `ref.state.watch(...)` run in one synchronous section: the mirror's open fails in a later turn,
	// so the watch opens at `loading` and its first frame is the broken failure. Taken after the hold's section, the watch
	// could open at the failure already, and its first frame would be the retry's.
	var (
		held  *Reference
		watch chord.NativeWatchHandle[*RefState]
		kinds *exCollector
	)
	host.(*chordHost).enter(func() {
		held = exHold(t, host, mirror, jsonObject("id", float64(1)))
		watch, kinds = exStateKinds(t, held)
	})
	want := []string{"failed, broken, no automatic retry: example.mirror(1): no mirror configured"}
	exWaitState(t, held, want[0])
	setFlag(&configured, true)
	held.Retry()
	scopeEventually(t, "the mirror failed transient", func() bool {
		current := held.State().Value()
		return current.Kind == RefStateFailed && current.RetryAt != nil && strings.HasSuffix(current.Cause.Error.Message, "mirror unreachable")
	})
	setFlag(&up, true)
	held.Retry()
	scopeEventually(t, "the mirror is live", func() bool { return exStateNow(t, held) == "live" })
	exEqual(t, "status", exValue(t, held, "status"), "mirroring")
	held.Retry()
	held.Release()
	<-watch.Closed()
	got := kinds.all()
	wantKinds := []string{
		"failed, broken, no automatic retry: example.mirror(1): no mirror configured", "loading", "failed, transient, retries: example.mirror(1): mirror unreachable", "loading", "live",
	}
	if len(got) < len(wantKinds) || !reflect.DeepEqual(got[:len(wantKinds)], wantKinds) {
		t.Fatalf("ref.state frames = %v, want prefix %v", got, wantKinds)
	}
	exCloseHost(t, host)
	// The mirror's failures are reported as activation_failed; nothing else.
	for _, kind := range reported.all() {
		if kind != string(ErrActivationFailed) {
			t.Fatalf("reported %v", reported.all())
		}
	}
}

// --- 12-routes.ts --------------------------------------------------------------------------------------------------

func TestExample12Routes(t *testing.T) {
	notes := exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exSessionAddress,
		contracts.Var("editor", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("notes", exArr(exStr()))},
			Methods: []contracts.MethodEntry{
				contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt())),
				contracts.Meth("whoami", contracts.NewMethod(contracts.TSchema{}, exStr())),
			},
		}),
		contracts.Var("reader", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("notes", exArr(exStr()))},
			Methods: []contracts.MethodEntry{contracts.Meth("whoami", contracts.NewMethod(contracts.TSchema{}, exStr()))},
		}))
	lobby := exRemote("example.lobby", contracts.Params{}, exServerAddress, contracts.Var("visitor", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{contracts.Meth("peek", contracts.NewMethod(exObj(exProp("session", exStr())), exStr()))},
	}))
	noteStore := exLocal("example.note-store", contracts.Params{},
		contracts.Mem("list", exArr(exStr())), contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt())))
	doc := exStringListDoc("notes")
	whoami := func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		return exText(exCtx(ctx).Principal()), nil
	}
	// The worker's facet: an owner edits, anyone else reads; `whoami` shows whom a call acts for.
	workerFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(noteStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"list": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := notesDoc.Change(func(draft *delta.Object) { exPush(draft, args.Value("text")) }, ctx); err != nil {
						return nil, err
					}
					return float64(len(notesDoc.Value().Value("list").([]any))), nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(notes, func(_ *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error) {
			store := self.Get(noteStore, jsonObject())
			if principalKind(principal) != "owner" {
				return exVariant("reader", map[string]chord.Watchable[delta.JsonValue]{"notes": Expose(store.Member("list"))},
					map[string]contracts.MethodFunction{"whoami": whoami}), nil
			}
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{"notes": Expose(store.Member("list"))},
				map[string]contracts.MethodFunction{
					"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return store.Call("add", args, ctx)
					},
					"whoami": whoami,
				}), nil
		}, ServeOptions{})
	})
	serverFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(lobby, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return exVariant("visitor", nil, map[string]contracts.MethodFunction{
				// `ctx.get` acts as the caller: over the route, the worker sees the guest, not the server.
				"peek": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					reference, err := exCtx(ctx).Get(notes, jsonObject("session", args.Value("session")), GetOptions{})
					if err != nil {
						return nil, err
					}
					who, err := reference.Call("whoami", nil, ctx)
					if err != nil {
						return nil, err
					}
					return fmt.Sprintf("%s variant, %v", exVariantName(reference), who), nil
				}),
			}), nil
		}, ServeOptions{})
	})
	// 1. The server: an address, and a route for sessions.
	serverErrors := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: serverErrors.push})
	var resolves int
	var resolveMu sync.Mutex
	var workerLink *link.MemoryLink
	must(t, server.Route(Session, RouteOptions{
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			id, _ := address.Params.Value("id").(string)
			return principalKind(principal) == "owner" || (principalKind(principal) == "guest" && principal.Value("session") == id), nil
		},
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			resolveMu.Lock()
			resolves++
			resolveMu.Unlock()
			up, down := link.MemoryLinkPair()
			id, _ := address.Params.Value("id").(string)
			worker := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: serverErrors.push})
			must(t, worker.Connect(down, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
			go func() { _ = worker.Load([]HostFacet{{Name: "@example/worker", Facet: workerFacet}}, background) }()
			resolveMu.Lock()
			workerLink = up
			resolveMu.Unlock()
			return ResolvedLink{Link: up, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	must(t, server.Load([]HostFacet{{Name: "@example/server", Facet: serverFacet}}, background))
	resolveCount := func() int { resolveMu.Lock(); defer resolveMu.Unlock(); return resolves }
	// 2. The owner's tab gets S1's notes: tab -> server (relays) -> route -> worker. A second get shares the worker.
	owner := exAccept(t, server, ownerPrincipal())
	ownerNotes := exGet(t, owner.Host, notes, jsonObject("session", "S1"))
	if exVariantName(ownerNotes) != "editor" {
		t.Fatalf("owner variant = %q", exVariantName(ownerNotes))
	}
	exCall(t, ownerNotes, "add", jsonObject("text", "milk"))
	again := exGet(t, owner.Host, notes, jsonObject("session", "S1"))
	exEqual(t, "owner notes", exValue(t, again, "notes"), exJSON(t, `["milk"]`))
	if resolveCount() != 1 {
		t.Fatalf("resolves so far = %d", resolveCount())
	}
	// 3. A guest of S1 reads S1 and is refused S2 by `authorize` (no worker is started for it).
	guest := exAccept(t, server, guestPrincipal("S1"))
	read := exGet(t, guest.Host, notes, jsonObject("session", "S1"))
	if exVariantName(read) != "reader" {
		t.Fatalf("guest variant = %q", exVariantName(read))
	}
	exEqual(t, "guest notes", exValue(t, read, "notes"), exJSON(t, `["milk"]`))
	exEqual(t, "guest acts as", exCall(t, read, "whoami", nil), `{"kind":"guest","session":"S1"}`)
	_, err := guest.Host.Get(notes, jsonObject("session", "S2"), background, GetOptions{})
	_ = exCode(t, err, data.CodeForbidden, "")
	if resolveCount() != 1 {
		t.Fatalf("resolves after the refused guest = %d", resolveCount())
	}
	// 4. The guest calls the lobby on the server; its handler's `ctx.get` goes over the route as the guest.
	lobbyRef := exGet(t, guest.Host, lobby, jsonObject())
	exEqual(t, "guest via the lobby", exCall(t, lobbyRef, "peek", jsonObject("session", "S1")), `reader variant, {"kind":"guest","session":"S1"}`)
	// 5. The worker drops: references go loading, the route resolves the address again after its backoff, and the new
	// worker (a fresh run, empty notes here) binds them again.
	watch, states := exStateKinds(t, ownerNotes)
	resolveMu.Lock()
	closing := workerLink
	resolveMu.Unlock()
	closing.Close()
	scopeEventually(t, "loading then live again", func() bool {
		got := states.all()
		return len(got) >= 2 && got[len(got)-1] == "live"
	})
	if got := states.all(); got[0] != "loading" {
		t.Fatalf("after the drop: %v", got)
	}
	exEqual(t, "notes after the drop", exValue(t, ownerNotes, "notes"), exJSON(t, `[]`))
	if resolveCount() != 2 {
		t.Fatalf("resolves = %d, want 2", resolveCount())
	}
	_ = watch
	exCloseHost(t, owner.Host)
	exCloseHost(t, guest.Host)
	exCloseHost(t, server)
	if serverErrors.Len() > 0 || owner.Errors.Len() > 0 || guest.Errors.Len() > 0 {
		t.Fatalf("background errors: %v %v %v", serverErrors.All(), owner.Errors.All(), guest.Errors.All())
	}
}

// --- 13-relay.ts ---------------------------------------------------------------------------------------------------

// exFramedPort is an IpcPort over a byte stream pair (a child's stdin and stdout): Pi's Node IPC channel with
// `serialization: "advanced"`. A frame is a 4-byte length and a leading 0x00 for a link message; anything else is the
// app's own message.
type exFramedPort struct {
	reader io.Reader
	writer io.WriteCloser
	mu     sync.Mutex

	connected   bool
	messages    []func(any)
	disconnects []func()
}

func newExFramedPort(reader io.Reader, writer io.WriteCloser) *exFramedPort {
	port := &exFramedPort{reader: reader, writer: writer, connected: true}
	go port.read()
	return port
}

func (p *exFramedPort) read() {
	for {
		var length [4]byte
		if _, err := io.ReadFull(p.reader, length[:]); err != nil {
			p.ended()
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(p.reader, frame); err != nil {
			p.ended()
			return
		}
		p.mu.Lock()
		listeners := slices.Clone(p.messages)
		p.mu.Unlock()
		for _, listener := range listeners {
			if frame[0] == 0 {
				listener(frame[1:])
			} else {
				listener(string(frame[1:]))
			}
		}
	}
}

func (p *exFramedPort) ended() {
	p.mu.Lock()
	was := p.connected
	p.connected = false
	listeners := slices.Clone(p.disconnects)
	p.mu.Unlock()
	if was {
		for _, listener := range listeners {
			listener()
		}
	}
}

func (p *exFramedPort) Send(message []byte, callback func(error)) (bool, error) {
	frame := make([]byte, 4, 5+len(message))
	binary.BigEndian.PutUint32(frame, uint32(1+len(message)))
	frame = append(append(frame, 0), message...)
	p.mu.Lock()
	_, err := p.writer.Write(frame)
	p.mu.Unlock()
	callback(err)
	return err == nil, nil
}

func (p *exFramedPort) OnMessage(listener func(message any)) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, listener)
	return func() {}
}

func (p *exFramedPort) OnDisconnect(listener func()) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disconnects = append(p.disconnects, listener)
	return func() {}
}

func (p *exFramedPort) Disconnect() {
	p.mu.Lock()
	p.connected = false
	p.mu.Unlock()
	_ = p.writer.Close()
}

func (p *exFramedPort) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

// exSocket is a gorilla WebSocket as link.WebSocketLike: binary frames, one message per frame.
type exSocket struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	listeners map[link.WebSocketEvent][]func(any)
	writeMu   sync.Mutex
	closed    bool
}

func newExSocket(conn *websocket.Conn) *exSocket {
	socket := &exSocket{conn: conn, listeners: map[link.WebSocketEvent][]func(any){}}
	return socket
}

func (s *exSocket) start() { go s.read() }

func (s *exSocket) read() {
	for {
		kind, payload, err := s.conn.ReadMessage()
		if err != nil {
			s.emit(link.WebSocketClose, nil)
			return
		}
		if kind == websocket.BinaryMessage {
			s.emit(link.WebSocketMessage, payload)
		} else {
			s.emit(link.WebSocketMessage, string(payload))
		}
	}
}

func (s *exSocket) emit(event link.WebSocketEvent, value any) {
	s.mu.Lock()
	listeners := slices.Clone(s.listeners[event])
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(value)
	}
}

func (s *exSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 3
	}
	return 1
}

func (s *exSocket) SetBinaryType(string) {}

func (s *exSocket) Send(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (s *exSocket) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.conn.Close()
}

func (s *exSocket) AddEventListener(event link.WebSocketEvent, listener func(data any)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners[event] = append(s.listeners[event], listener)
	return func() {}
}

// exRelayWorkerEnv names the environment variable that makes the test binary the worker of the relay example: Pi's
// `process.argv[2] === "worker"` (the same file is the worker).
const exRelayWorkerEnv = "CHORD_EXAMPLE_13_WORKER"

func exRelayContracts() (sessionInfo, notes *contracts.Contract) {
	initSchema := exObj(exProp("id", exStr()), exProp("title", exStr()), exProp("pid", exInt()))
	sessionInfo = exLocal("example.session-info", contracts.Params{}, contracts.Mem("init", initSchema))
	notes = exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exSessionAddress,
		contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("info", initSchema), exProp("notes", exArr(exStr()))},
			Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt()))},
		}))
	return sessionInfo, notes
}

// exRelayWorker is the worker process of the relay example. 1. Its link first; 2. its host at its address: connect
// first (W6), then load the core facet with the start data, then the plugins; the server closing the link ends it.
func exRelayWorker(id string) {
	sessionInfo, notes := exRelayContracts()
	port := newExFramedPort(os.Stdin, os.Stdout)
	workerLink := link.IpcLink(port)
	address := Session.Address(jsonObject("id", id))
	worker, err := CreateHost(HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: func(err HostError) {
		fmt.Fprintln(os.Stderr, "worker error:", err.Kind, err.Error)
		os.Exit(1)
	}})
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker host:", err)
		os.Exit(1)
	}
	if err := worker.Connect(workerLink, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background); err != nil {
		fmt.Fprintln(os.Stderr, "worker connect:", err)
		os.Exit(1)
	}
	init := jsonObject("id", id, "title", "Session "+id, "pid", float64(os.Getpid()))
	core := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(sessionInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"init": exConstant(init)}, nil), nil
		}, ServeOptions{})
	})
	doc := exStringListDoc("notes")
	notesFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			info := self.Get(sessionInfo, jsonObject())
			return exVariant("owner", map[string]chord.Watchable[delta.JsonValue]{
				"info": Expose(info.Member("init")),
				"notes": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := notesDoc.Change(func(draft *delta.Object) { exPush(draft, args.Value("text")) }, ctx); err != nil {
						return nil, err
					}
					return float64(len(notesDoc.Value().Value("list").([]any))), nil
				}),
			}), nil
		}, ServeOptions{})
	})
	if err := worker.Load([]HostFacet{{Name: "@example/core", Facet: core}, {Name: "@example/notes", Facet: notesFacet}}, background); err != nil {
		fmt.Fprintln(os.Stderr, "worker load:", err)
		os.Exit(1)
	}
	closed := make(chan struct{})
	workerLink.OnClose(func() { close(closed) })
	<-closed
	os.Exit(0)
}

func TestExample13Relay(t *testing.T) {
	if id := os.Getenv(exRelayWorkerEnv); id != "" {
		exRelayWorker(id)
		return
	}
	_, notes := exRelayContracts()
	serverErrors := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: serverErrors.push})
	var children []*exec.Cmd
	var childrenMu sync.Mutex
	// 3. A route forks a worker per session and returns the IPC link at once.
	must(t, server.Route(Session, RouteOptions{
		Authorize: func(principal data.Principal, _ contracts.Address) (bool, error) {
			return principalKind(principal) == "owner", nil
		},
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			id, _ := address.Params.Value("id").(string)
			child := exec.Command(os.Args[0], "-test.run=^TestExample13Relay$")
			child.Env = append(os.Environ(), exRelayWorkerEnv+"="+id)
			stdin, err := child.StdinPipe()
			if err != nil {
				return ResolvedLink{}, err
			}
			stdout, err := child.StdoutPipe()
			if err != nil {
				return ResolvedLink{}, err
			}
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				return ResolvedLink{}, err
			}
			childrenMu.Lock()
			children = append(children, child)
			childrenMu.Unlock()
			return ResolvedLink{Link: link.IpcLink(newExFramedPort(stdout, stdin)), Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	must(t, server.Load(nil, background))
	// 4. Tabs reach the server over a WebSocket; its entry point stamps each as the owner here.
	upgrader := websocket.Upgrader{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		socket := newExSocket(conn)
		must(t, server.Accept(link.WebSocketLink(socket), LinkOptions{Principal: ownerPrincipal()}, background))
		socket.start()
	}))
	defer httpServer.Close()
	// 5. A tab over a WebSocket of its own: its holds go tab -> server (relay) -> worker.
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	must(t, err)
	must(t, resp.Body.Close())
	tabSocket := newExSocket(conn)
	tabErrors := &hostErrors{}
	tab := newHost(t, HostOptions{OnError: tabErrors.push})
	must(t, tab.Connect(link.WebSocketLink(tabSocket), LinkOptions{Principal: serverPrincipal()}, background))
	tabSocket.start()
	must(t, tab.Load(nil, background))
	s1 := exGet(t, tab, notes, jsonObject("session", "S1"))
	info := exValue(t, s1, "info").(*delta.JsonObject)
	if info.Value("id") != "S1" || info.Value("title") != "Session S1" || info.Value("pid") == float64(os.Getpid()) || info.Value("pid").(float64) <= 0 {
		t.Fatalf("tab: S1 info = %s (server pid %d)", exText(info), os.Getpid())
	}
	exCall(t, s1, "add", jsonObject("text", "milk"))
	exCall(t, s1, "add", jsonObject("text", "bread"))
	scopeEventually(t, "tab: S1 notes", func() bool { return exJSONEqual(exValue(t, s1, "notes"), exJSON(t, `["milk","bread"]`)) })
	// 6. Another session is another worker process.
	s2 := exGet(t, tab, notes, jsonObject("session", "S2"))
	info2 := exValue(t, s2, "info").(*delta.JsonObject)
	if info2.Value("id") != "S2" || info2.Value("pid") == info.Value("pid") {
		t.Fatalf("tab: S2 info = %s", exText(info2))
	}
	exEqual(t, "tab: S2 notes", exValue(t, s2, "notes"), exJSON(t, `[]`))
	// 7. Closing the server closes its route links; each worker exits when its link closes.
	exCloseHost(t, tab)
	exCloseHost(t, server)
	childrenMu.Lock()
	defer childrenMu.Unlock()
	if len(children) != 2 {
		t.Fatalf("forked %d workers, want 2", len(children))
	}
	for _, child := range children {
		if err := child.Wait(); err != nil {
			t.Fatalf("worker exit: %v", err)
		}
	}
	if serverErrors.Len() > 0 || tabErrors.Len() > 0 {
		t.Fatalf("background errors: %v %v", serverErrors.All(), tabErrors.All())
	}
}

// --- 14-flow-control.ts --------------------------------------------------------------------------------------------

// exPausable is a slow tab's end of its link: while paused, what arrives waits (so the tab acknowledges nothing).
type exPausable struct {
	link     link.Link
	mu       sync.Mutex
	paused   bool
	waiting  [][]byte
	listener func([]byte)
}

func newExPausable(end link.Link) *exPausable {
	p := &exPausable{link: end}
	end.OnMessage(func(message []byte) {
		p.mu.Lock()
		// Messages that arrive before the host listens wait too (a microtask cannot interleave in Pi).
		if p.paused || p.listener == nil {
			p.waiting = append(p.waiting, message)
			p.mu.Unlock()
			return
		}
		listener := p.listener
		p.mu.Unlock()
		if listener != nil {
			listener(message)
		}
	})
	return p
}

func (p *exPausable) pause() { p.mu.Lock(); p.paused = true; p.mu.Unlock() }

func (p *exPausable) resume() {
	p.mu.Lock()
	p.paused = false
	waiting := p.waiting
	p.waiting = nil
	listener := p.listener
	p.mu.Unlock()
	for _, message := range waiting {
		listener(message)
	}
}

func (p *exPausable) Send(message []byte) { p.link.Send(message) }
func (p *exPausable) OnMessage(listener func([]byte)) {
	p.mu.Lock()
	p.listener = listener
	paused := p.paused
	p.mu.Unlock()
	if !paused {
		p.resume()
	}
}
func (p *exPausable) Close()                  { p.link.Close() }
func (p *exPausable) OnClose(listener func()) { p.link.OnClose(listener) }

func TestExample14FlowControl(t *testing.T) {
	reply := exLocal("example.reply", contracts.Params{},
		contracts.Mem("reply", exObj(exProp("text", exStr()))), contracts.Mem("append", contracts.NewMethod(exStr())))
	view := mustContract(contracts.Remote("example.view", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{exProp("session", exStr())}, HostAddress: exSessionAddress,
		Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("reply", exObj(exProp("text", exStr())))},
		})},
	}))
	replyDoc := exDefineState("reply", documents.StateOptions{
		Schema:  exObj(exProp("text", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("text", "") },
	})
	workerFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(reply, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, replyDoc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"reply": doc.Member()}, map[string]contracts.MethodFunction{
				"append": func(token delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("text", draft.Get("text").(string)+token.(string)) }, ctx)
				},
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(view, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return exVariant("owner", map[string]chord.Watchable[delta.JsonValue]{
				"reply": Expose(self.Get(reply, jsonObject()).Member("reply")),
			}, nil), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	worker := func(id string) Host {
		address := Session.Address(jsonObject("id", id))
		host := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: errs.push})
		must(t, host.Load([]HostFacet{{Name: "@example/worker", Facet: workerFacet}}, background))
		return host
	}
	tab := func(end link.Link) Host {
		host := newHost(t, HostOptions{OnError: errs.push})
		must(t, host.Connect(end, LinkOptions{Principal: serverPrincipal()}, background))
		must(t, host.Load(nil, background))
		return host
	}
	textLen := func(ref *Reference) int {
		return len(exValue(t, ref, "reply").(*delta.JsonObject).Value("text").(string))
	}
	// 1. Folding at the provider: a tab directly on a worker. Paused, it acknowledges nothing; the worker sends two
	// `state` messages (the window), then folds 1,000 appends into its pending queue.
	w1 := worker("S1")
	up, down := link.MemoryLinkPair()
	must(t, w1.Accept(up, LinkOptions{Principal: ownerPrincipal()}, background))
	slow := newExPausable(down)
	t1 := tab(slow)
	view1 := exGet(t, t1, view, jsonObject("session", "S1"))
	reply1 := exGet(t, w1, reply, jsonObject())
	slow.pause()
	for range 1000 {
		exCall(t, reply1, "append", " tok")
	}
	transported(up, down)
	if got, want := textLen(reply1), 4000; got != want {
		t.Fatalf("the worker has %d characters, want %d", got, want)
	}
	// Paused, what the worker sent waits at the tab's end of the link: the tab shows nothing yet.
	if got := textLen(view1); got != 0 {
		t.Fatalf("paused: the tab shows %d characters, want 0", got)
	}
	// 2. Resumed: the tab applies and acknowledges the two, and the freed slot carries one `state` with the fold.
	slow.resume()
	scopeEventually(t, "the tab catches up", func() bool { return textLen(view1) == 4000 })
	// 3. The same slow tab through a server: the worker's window for that origin fills and it folds there; a second, fast
	// tab is another origin and receives every commit.
	serverErrors := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: serverErrors.push})
	var w2 Host
	var w2Ends [2]*link.MemoryLink
	var w2Mu sync.Mutex
	must(t, server.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			a, b := link.MemoryLinkPair()
			id, _ := address.Params.Value("id").(string)
			made := worker(id)
			must(t, made.Connect(b, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
			w2Mu.Lock()
			w2, w2Ends = made, [2]*link.MemoryLink{a, b}
			w2Mu.Unlock()
			return ResolvedLink{Link: a, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	must(t, server.Load(nil, background))
	up2, down2 := link.MemoryLinkPair()
	must(t, server.Accept(up2, LinkOptions{Principal: ownerPrincipal()}, background))
	slow2 := newExPausable(down2)
	t2 := tab(slow2)
	view2 := exGet(t, t2, view, jsonObject("session", "S2"))
	up3, down3 := link.MemoryLinkPair()
	must(t, server.Accept(up3, LinkOptions{Principal: ownerPrincipal()}, background))
	t3 := tab(down3)
	view3 := exGet(t, t3, view, jsonObject("session", "S2"))
	watch, fast := exWatch(t, background, view3.Member("reply"))
	w2Mu.Lock()
	provider, workerLink := w2, w2Ends
	w2Mu.Unlock()
	reply2 := exGet(t, provider, reply, jsonObject())
	slow2.pause()
	for i := range 5 {
		exCall(t, reply2, "append", fmt.Sprintf(" t%d", i))
		// A relay forwards at once and returns each `ack` to the worker, so a fast tab frees its slot before the next commit
		// and receives every one. In Pi the hops are microtasks the next commit's own microtasks follow; here a commit is a call
		// from this goroutine, so wait for the `state` to reach the fast tab and its `ack` the worker before the next one.
		transported(up2, down2, up3, down3, workerLink[0], workerLink[1])
	}
	transported(up2, down2, up3, down3)
	exEqual(t, "the worker has", exValue(t, reply2, "reply"), exJSON(t, `{"text":" t0 t1 t2 t3 t4"}`))
	exEqual(t, "paused: the slow tab shows", exValue(t, view2, "reply"), exJSON(t, `{"text":""}`))
	scopeEventually(t, "the fast tab saw every change", func() bool { return len(fast.all()) == 5 })
	exEqual(t, "the fast tab", exValue(t, view3, "reply"), exJSON(t, `{"text":" t0 t1 t2 t3 t4"}`))
	// 4. Resumed: the slow tab acknowledges the two; the relay returns each `ack` to the worker with the origin, and the
	// worker's freed slot carries one `state` with the three folded appends.
	slow2.resume()
	scopeEventually(t, "the slow tab catches up", func() bool {
		return exJSONEqual(exValue(t, view2, "reply"), exJSON(t, `{"text":" t0 t1 t2 t3 t4"}`))
	})
	exCloseHost(t, t1)
	exCloseHost(t, w1)
	_, _ = watch.Stop()
	exCloseHost(t, t2)
	exCloseHost(t, t3)
	exCloseHost(t, server)
	exCloseHost(t, provider)
	if errs.Len() > 0 || serverErrors.Len() > 0 {
		t.Fatalf("background errors: %v %v", errs.All(), serverErrors.All())
	}
}

// exReferences is `host.references(Type).value`: one JSON object {params, principal?, incarnation} per reference.
func exReferences(t testing.TB, host Host, typ *contracts.Contract) []*delta.JsonObject {
	t.Helper()
	var value delta.JsonValue
	var err error
	host.(*chordHost).enter(func() { value, err = host.References(typ).Value() })
	must(t, err)
	list, _ := value.([]any)
	out := make([]*delta.JsonObject, len(list))
	for i, entry := range list {
		out[i] = entry.(*delta.JsonObject)
	}
	return out
}

// --- 15-revocation.ts ----------------------------------------------------------------------------------------------

func TestExample15Revocation(t *testing.T) {
	// The same function on every host: a guest is revoked by its grant and by its session.
	revokedBy := RevokedBy[delta.JsonValue](func(principal data.Principal) ([]delta.JsonValue, error) {
		if principalKind(principal) == "guest" {
			return []delta.JsonValue{jsonObject("grant", principal.Value("grant")), jsonObject("session", principal.Value("session"))}, nil
		}
		return nil, nil
	})
	guestOf := func(grant, session string) data.Principal {
		return jsonObject("kind", "guest", "grant", grant, "session", session)
	}
	notes := exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exSessionAddress, contracts.Var("all", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("notes", exArr(exStr()))},
		Methods: []contracts.MethodEntry{
			contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())))),
			contracts.Meth("follow", contracts.NewMethod()),
		},
	}))
	doc := exStringListDoc("notes")
	followAborted := make(chan error, 1)
	notesFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			return exVariant("all", map[string]chord.Watchable[delta.JsonValue]{
				"notes": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, notesDoc.Change(func(draft *delta.Object) { exPush(draft, args.Value("text")) }, ctx)
				}),
				// A long-running call: it ends when its context is aborted, and says why.
				"follow": exBlocked(func(cause error) {
					if principalKind(principal) == "guest" {
						followAborted <- cause
					}
				}),
			}), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), RevokedBy: revokedBy, OnError: errs.push})
	var workers []Host
	var workersMu sync.Mutex
	must(t, server.Route(Session, RouteOptions{
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			id, _ := address.Params.Value("id").(string)
			return principalKind(principal) == "owner" || (principalKind(principal) == "guest" && principal.Value("session") == id), nil
		},
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			up, down := link.MemoryLinkPair()
			id, _ := address.Params.Value("id").(string)
			worker := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), RevokedBy: revokedBy, OnError: errs.push})
			must(t, worker.Connect(up, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
			go func() { _ = worker.Load([]HostFacet{{Name: "@example/notes", Facet: notesFacet}}, background) }()
			workersMu.Lock()
			workers = append(workers, worker)
			workersMu.Unlock()
			return ResolvedLink{Link: down, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	must(t, server.Load(nil, background))
	// 2. Two tabs of guest grant g1 (one person, two windows) and the owner's tab, all on session S1.
	one := exAccept(t, server, guestOf("g1", "S1"))
	two := exAccept(t, server, guestOf("g1", "S1"))
	owner := exAccept(t, server, ownerPrincipal())
	guestNotes := exGet(t, one.Host, notes, jsonObject("session", "S1"))
	exGet(t, two.Host, notes, jsonObject("session", "S1"))
	ownerNotes := exGet(t, owner.Host, notes, jsonObject("session", "S1"))
	exCall(t, guestNotes, "add", jsonObject("text", "from the guest"))
	following := make(chan error, 1)
	go func() { _, err := guestNotes.Call("follow", nil, background); following <- err }()
	workersMu.Lock()
	worker := workers[0]
	workersMu.Unlock()
	scopeEventually(t, "the call runs at the worker", func() bool { return exActivity(t, worker).Calls == 1 })
	if got := len(exReferences(t, worker, notes)); got != 3 {
		t.Fatalf("worker S1 references: %d, want 3", got)
	}
	// 3. Revoke the grant: the worker gets `revoke { grant: "g1" }` and ends the guest's objects there (their running
	// calls aborted with forbidden/revoked), and both guest tabs' links close.
	must(t, server.Revoke(jsonObject("grant", "g1"), background))
	cause := <-followAborted
	_ = exCode(t, cause, data.CodeForbidden, data.ReasonRevoked)
	scopeEventually(t, "the guest tab's link closed", func() bool { return guestNotes.State().Value().Kind != RefStateLive })
	if err := <-following; err == nil {
		t.Fatal("the guest's follow call resolved")
	}
	// The owner's Notes is its own object (identities include the principal), untouched.
	if ownerNotes.State().Value().Kind != RefStateLive {
		t.Fatalf("owner tab: %s", ownerNotes.State().Value().Kind)
	}
	exEqual(t, "owner notes", exValue(t, ownerNotes, "notes"), exJSON(t, `[]`))
	scopeEventually(t, "worker S1 references", func() bool {
		refs := exReferences(t, worker, notes)
		return len(refs) == 1 && principalKind(refs[0].Value("principal").(*delta.JsonObject)) == "owner"
	})
	// 4. A worker resolved later gets no past revocation: its objects are new, and the revoked links are closed already.
	s2 := exGet(t, owner.Host, notes, jsonObject("session", "S2"))
	if s2.State().Value().Kind != RefStateLive {
		t.Fatalf("owner on S2: %s", s2.State().Value().Kind)
	}
	for _, host := range []Host{one.Host, two.Host, owner.Host, server} {
		exCloseHost(t, host)
	}
	workersMu.Lock()
	for _, w := range workers {
		exCloseHost(t, w)
	}
	workersMu.Unlock()
	if errs.Len() > 0 || one.Errors.Len() > 0 || two.Errors.Len() > 0 || owner.Errors.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

// --- 16-presence-and-activity.ts -----------------------------------------------------------------------------------

func TestExample16PresenceAndActivity(t *testing.T) {
	doc := exRemote("example.doc", contracts.Params{exProp("id", exStr())}, exServerAddress, contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{exProp("viewers", exInt())},
		Methods: []contracts.MethodEntry{contracts.Meth("think", contracts.NewMethod(contracts.TSchema{}, exInt()))},
	}))
	userOf := func(name string) data.Principal { return jsonObject("kind", "user", "name", name) }
	thinking := make(chan struct{})
	release := make(chan struct{})
	linger := 50 * time.Millisecond
	// 1. The server serves `Doc`; its factory derives the viewer count from `host.references(Doc)`.
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(doc, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			id := exParam(params, "id")
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{
				"viewers": self.Derive([]any{h.References(doc)}, func(values []any) (delta.JsonValue, error) {
					count := 0
					for _, entry := range values[0].([]any) {
						if entry.(*delta.JsonObject).Value("params").(*delta.JsonObject).Value("id") == id {
							count++
						}
					}
					return float64(count), nil
				}),
			}, map[string]contracts.MethodFunction{
				"think": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					close(thinking)
					// Pi: `await release.promise`, which an abort does not end.
					chordctx.Suspend(ctx)
					<-release
					if principalKind(exCtx(ctx).Principal()) == "user" {
						return float64(42), nil
					}
					return float64(0), nil
				},
			}), nil
		}, ServeOptions{Linger: &linger})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/doc", Facet: facet})
	// 2. The supervisor's view: every publication of the counts.
	activityWatch, err := server.Activity().Watch(background)
	must(t, err)
	var published exCollector
	activityWatch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			published.add(exText(call.Value))
		}
		return nil
	})
	// 3. Ada opens the document in two tabs, Bob in one: three references, three incarnations, two objects (one per
	// principal); the viewer count is over every object of the document.
	ada1 := exAccept(t, server, userOf("ada"))
	ada2 := exAccept(t, server, userOf("ada"))
	bob := exAccept(t, server, userOf("bob"))
	adaDoc := exGet(t, ada1.Host, doc, jsonObject("id", "d1"))
	exGet(t, ada2.Host, doc, jsonObject("id", "d1"))
	exGet(t, bob.Host, doc, jsonObject("id", "d1"))
	scopeEventually(t, "viewers seen by ada", func() bool { return exValue(t, adaDoc, "viewers") == float64(3) })
	var present []string
	for _, ref := range exReferences(t, server, doc) {
		incarnation := ref.Value("incarnation").(string)
		present = append(present, ref.Value("principal").(*delta.JsonObject).Value("name").(string)+" @ "+incarnation[len(incarnation)-4:])
	}
	if len(present) != 3 || present[0][:3] != "ada" || present[1][:3] != "ada" || present[2][:3] != "bob" {
		t.Fatalf("references = %v, want ada, ada, bob", present)
	}
	if present[0] == present[1] {
		t.Fatalf("two tabs of ada have one incarnation: %v", present)
	}
	// 4. A call running counts as activity until it settles.
	thought := make(chan any, 1)
	go func() { value, _ := adaDoc.Call("think", nil, background); thought <- value }()
	<-thinking
	if got := exActivity(t, server).Calls; got != 1 {
		t.Fatalf("activity while thinking: %d calls", got)
	}
	close(release)
	if got := <-thought; got != float64(42) {
		t.Fatalf("think = %v", got)
	}
	// 5. Bob's link drops: a departure, at once. Bob's own `Doc` object lingers.
	bob.Link.Close()
	scopeEventually(t, "viewers after bob's link dropped", func() bool { return exValue(t, adaDoc, "viewers") == float64(2) })
	// 6. Ada closes both tabs: the object lingers (50 ms) with no references, then ends; then everything is zero.
	exCloseHost(t, ada1.Host)
	exCloseHost(t, ada2.Host)
	scopeEventually(t, "references at the end", func() bool { return len(exReferences(t, server, doc)) == 0 })
	scopeEventually(t, "the lingering objects ended", func() bool { return exActivity(t, server) == ActivityCounts{} })
	scopeEventually(t, "the last publication", func() bool {
		all := published.all()
		return len(all) > 0 && all[len(all)-1] == `{"references":0,"calls":0,"jobs":0,"lingering":0}`
	})
	frames := published.all()
	if len(frames) == 0 || frames[len(frames)-1] != `{"references":0,"calls":0,"jobs":0,"lingering":0}` {
		t.Fatalf("the supervisor's last publication = %v", frames)
	}
	sawCall, sawThree := false, false
	for _, frame := range frames {
		sawCall = sawCall || strings.Contains(frame, `"calls":1`)
		sawThree = sawThree || strings.Contains(frame, `"references":3`)
	}
	if !sawCall || !sawThree {
		t.Fatalf("publications %v lack a running call or the three holds", frames)
	}
	_, _ = activityWatch.Stop()
	exCloseHost(t, server)
}

// --- 17-limits-and-compression.ts ----------------------------------------------------------------------------------

// exCountingLink is a link end counting the bytes its host receives.
type exCountingLink struct {
	link.Link
	mu       sync.Mutex
	received int
}

func (c *exCountingLink) OnMessage(listener func([]byte)) {
	c.Link.OnMessage(func(message []byte) {
		c.mu.Lock()
		c.received += len(message)
		c.mu.Unlock()
		listener(message)
	})
}

func (c *exCountingLink) bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received
}

// exWords are the words of entries that compress like prose (about 3x), not like a repeated string.
var exWords = strings.Fields("the a model tool call file read write test error result agent session note code run build")

// exText is Pi's `text(i)`: about 1 KB of words picked by a fixed pseudo-random sequence (JavaScript's double arithmetic).
func exEntryText(i int) string {
	seed := float64(i*7919 + 1)
	var words []string
	for length := 0; length < 1000; length += len(words[len(words)-1]) + 1 {
		seed = math.Mod(seed*1103515245+12345, 2147483648)
		word := exWords[int(seed)%len(exWords)]
		if int(seed)%5 == 0 {
			word = fmt.Sprintf("%s%d", word, int(seed)%1000)
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

func TestExample17LimitsAndCompression(t *testing.T) {
	entry := exObj(exProp("id", exStr()), exProp("text", exStr()))
	store := exLocal("example.store", contracts.Params{exProp("id", exStr())},
		contracts.Mem("entries", exArr(entry)), contracts.Mem("append", contracts.NewMethod(entry)))
	conversation := exRemote("example.conversation", contracts.Params{exProp("id", exStr())}, exServerAddress,
		contracts.Var("v", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("entries", exArr(entry))},
			Methods: []contracts.MethodEntry{contracts.Meth("append", contracts.NewMethod(entry))},
		}))
	entries := exDefineState("entries", documents.StateOptions{
		Schema:  exObj(exProp("list", exArr(entry))),
		Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
	})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(store, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, entries)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"entries": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"append": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { exPush(draft, args) }, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(conversation, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			backing := self.Get(store, jsonObject("id", exParam(params, "id")))
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{"entries": Expose(backing.Member("entries"))},
				map[string]contracts.MethodFunction{
					"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return backing.Call("append", args, ctx)
					},
				}), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	// setUp is a server compressing with compression and a tab without a compressor (a browser) on a link the server
	// accepts with limits; the tab's own link to the server has tabLimits.
	setUp := func(compression link.Compression, limits, tabLimits link.PartLimits) (Host, Host, *exCountingLink) {
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Compression: compression, OnError: errs.push})
		must(t, server.Load([]HostFacet{{Name: "@example/conversation", Facet: facet}}, background))
		tab := newHost(t, HostOptions{OnError: errs.push})
		must(t, tab.Load(nil, background))
		a, b := link.MemoryLinkPair()
		counted := &exCountingLink{Link: b}
		must(t, server.Accept(a, LinkOptions{Principal: jsonObject("kind", "user"), Limits: limits}, background))
		must(t, tab.Connect(counted, LinkOptions{Principal: serverPrincipal(), Limits: tabLimits}, background))
		return server, tab, counted
	}
	fill := func(server Host, id string) {
		backing := exGet(t, server, store, jsonObject("id", id))
		for i := range 500 {
			exCall(t, backing, "append", jsonObject("id", fmt.Sprintf("e%d", i), "text", exEntryText(i)))
		}
	}
	// 1. A 500 KB snapshot to a browser tab, without and with compression at the server.
	var snapshotBytes []int
	for _, compression := range []link.Compression{nil, nextnode.NodeCompression} {
		server, tab, counted := setUp(compression, link.PartLimits{}, link.PartLimits{})
		fill(server, "c1")
		reference := exGet(t, tab, conversation, jsonObject("id", "c1"))
		if got := len(exValue(t, reference, "entries").([]any)); got != 500 {
			t.Fatalf("snapshot: %d entries, want 500", got)
		}
		snapshotBytes = append(snapshotBytes, counted.bytes())
		exCloseHost(t, tab)
		exCloseHost(t, server)
	}
	if snapshotBytes[0] < 500*1000 || snapshotBytes[1] >= snapshotBytes[0]/2 {
		t.Fatalf("received bytes (uncompressed, deflated) = %v: want >= 500 KB and the deflated snapshot well below half", snapshotBytes)
	}
	// 2. Limits per part: the server admits at most 64 KiB per part from this tab. A larger argument fails its call only.
	{
		part := 64.0 * 1024
		server, tab, _ := setUp(nextnode.NodeCompression, link.PartLimits{Part: &part}, link.PartLimits{})
		reference := exGet(t, tab, conversation, jsonObject("id", "c2"))
		_, err := reference.Call("append", jsonObject("id", "big", "text", strings.Repeat("x", 100_000)), background)
		_ = exCode(t, err, data.CodeTooLarge, data.ReasonPart)
		exCall(t, reference, "append", jsonObject("id", "small", "text", "fits"))
		scopeEventually(t, "the link stays: entries", func() bool {
			list, _ := exValue(t, reference, "entries").([]any)
			return len(list) == 1 && list[0].(*delta.JsonObject).Value("id") == "small"
		})
		exCloseHost(t, tab)
		exCloseHost(t, server)
	}
	// 3. A compression bomb: 6 MB of zeros deflated, past the tab's decoded limit of 1 MiB. Only that hold fails.
	{
		decoded := 1024.0 * 1024
		server, tab, _ := setUp(nextnode.NodeCompression, link.PartLimits{}, link.PartLimits{Decoded: &decoded})
		bomb := exGet(t, server, store, jsonObject("id", "bomb"))
		for i := range 6 {
			exCall(t, bomb, "append", jsonObject("id", fmt.Sprintf("z%d", i), "text", strings.Repeat("0", 1_000_000)))
		}
		fill(server, "fine")
		_, err := tab.Get(conversation, jsonObject("id", "bomb"), background, GetOptions{})
		_ = exCode(t, err, data.CodeTooLarge, data.ReasonPart)
		fine := exGet(t, tab, conversation, jsonObject("id", "fine"))
		if got := len(exValue(t, fine, "entries").([]any)); got != 500 {
			t.Fatalf("another hold on the same link: %d entries", got)
		}
		exCloseHost(t, tab)
		exCloseHost(t, server)
	}
	// 4. A client with a compressor still sends its arguments up uncompressed: the server, which accepted its link as a
	// child, would refuse a compressed part. Its 100 KB argument arrives as JSON, within the server's default limits.
	{
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Compression: nextnode.NodeCompression, OnError: errs.push})
		must(t, server.Load([]HostFacet{{Name: "@example/conversation", Facet: facet}}, background))
		client := newHost(t, HostOptions{Compression: nextnode.NodeCompression, OnError: errs.push})
		must(t, client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		counted := &exCountingLink{Link: a}
		must(t, server.Accept(counted, LinkOptions{Principal: jsonObject("kind", "user")}, background))
		must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal()}, background))
		reference := exGet(t, client, conversation, jsonObject("id", "c4"))
		exCall(t, reference, "append", jsonObject("id", "big", "text", strings.Repeat(exEntryText(0), 100)))
		if got := counted.bytes(); got < 100*1000 {
			t.Fatalf("a compressing client's 100 KB argument: %d bytes received by the server", got)
		}
		exCloseHost(t, client)
		exCloseHost(t, server)
	}
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

// --- 18-routines.ts ------------------------------------------------------------------------------------------------

func TestExample18Routines(t *testing.T) {
	session := exObj(exProp("archived", exBool()))
	catalog := exLocal("example.catalog", contracts.Params{},
		contracts.Mem("sessions", contracts.Map(session)), contracts.Mem("archive", contracts.NewMethod(exObj(exProp("session", exStr())))))
	forwardStore := exLocal("example.forwards", contracts.Params{}, contracts.Mem("open", exArr(exStr())), contracts.Mem("cleanups", exArr(exStr())))
	sessions := exDefineState("example.sessions", documents.StateOptions{
		Schema: exObj(exProp("sessions", contracts.Map(session))),
		Initial: func() *delta.JsonObject {
			return jsonObject("sessions", jsonObject("s1", jsonObject("archived", false), "s2", jsonObject("archived", false)))
		},
	})
	forwardList := exDefineState("example.forward-list", documents.StateOptions{
		Schema:  exObj(exProp("open", exArr(exStr())), exProp("cleanups", exArr(exStr()))),
		Initial: func() *delta.JsonObject { return jsonObject("open", []any{"s1", "s2"}, "cleanups", []any{}) },
	})
	// 1. The host reports background failures: a routine call that threw arrives as `routine_failed`.
	var reported exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		if err.Kind == ErrRoutineFailed {
			reported.add(fmt.Sprintf("%s %s #%d failed: %v", err.Object, err.Routine.Kind, err.Routine.Position, err.Error))
			return
		}
		errs.push(err)
	}})
	var flaky = struct {
		sync.Mutex
		on bool
	}{on: true}
	var ons exCollector
	must(t, host.Load([]HostFacet{{Name: "@example/forwards", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(catalog, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, sessions)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"sessions": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("sessions"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"archive": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(d *delta.Object) {
						_ = d.Object("sessions").Set(exParam(args, "session"), jsonObject("archived", true))
					}, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(forwardStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			forwards := stateOf(self, forwardList)
			catalogRef := self.Get(catalog, jsonObject())
			// 2. Reconcile: close every forward of an archived session. Idempotent, from the current value; the first
			// close fails once (a flaky remote), and the retry comes with the whole value.
			self.On(catalogRef.Member("sessions"), func(call chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
				ons.add(exText(call.Ops))
				current := call.Value.(*delta.JsonObject)
				open := forwards.Value().Value("open").([]any)
				// The handler's awaits (Pi's async function) run off the loop.
				return Awaiting(exLoop(ctx), func() error {
					for _, name := range open {
						archived, _ := current.Value(name.(string)).(*delta.JsonObject)
						if archived == nil || archived.Value("archived") != true {
							continue
						}
						flaky.Lock()
						fail := flaky.on
						flaky.on = false
						flaky.Unlock()
						if fail {
							return fmt.Errorf("closing the forward of %s timed out", name)
						}
						if err := forwards.Change(func(d *delta.Object) {
							var kept []any
							for _, existing := range d.Array("open").Snapshot() {
								if existing != name {
									kept = append(kept, existing)
								}
							}
							_ = d.Set("open", kept)
						}, ctx); err != nil {
							return err
						}
					}
					return nil
				})
			})
			// 3. A periodic task: a loop with `sleep`, ended by the object's end (the abort rejects `sleep`).
			self.OnLive(func(ctx Ctx) *Handled {
				return Awaiting(exLoop(ctx), func() error {
					for {
						if err := Sleep(400*time.Millisecond, ctx); err != nil {
							return nil
						}
						if err := forwards.Change(func(d *delta.Object) { _, _ = d.Array("cleanups").Push("tick") }, ctx); err != nil {
							return nil
						}
					}
				})
			})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"open": self.Derive([]any{forwards.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("open"), nil
				}),
				"cleanups": self.Derive([]any{forwards.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("cleanups"), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	})}}, background))
	store := exGet(t, host, forwardStore, jsonObject())
	catalogRef := exGet(t, host, catalog, jsonObject())
	// 4. Archive s2: the routine's call throws once, is retried after 1 s with the whole value, and closes the forward.
	exCall(t, catalogRef, "archive", jsonObject("session", "s2"))
	scopeEventually(t, "the failed close was reported", func() bool { return len(reported.all()) == 1 })
	exEqual(t, "reported", reported.all(), []any{"example.forwards on #1 failed: closing the forward of s2 timed out"})
	scopeEventually(t, "open forwards", func() bool { return exJSONEqual(exValue(t, store, "open"), exJSON(t, `["s1"]`)) })
	scopeEventually(t, "periodic cleanups so far", func() bool { return len(exValue(t, store, "cleanups").([]any)) > 0 })
	if got := ons.all(); len(got) < 2 {
		t.Fatalf("on calls = %v, want the first value and the archive's", got)
	}
	// 5. Closing the host aborts the routines: the periodic loop ends in its `sleep`, nothing is reported.
	exCloseHost(t, host)
	if len(reported.all()) != 1 || errs.Len() > 0 {
		t.Fatalf("closed with reports %v %v", reported.all(), errs.All())
	}
}

// --- 19-jobs.ts ----------------------------------------------------------------------------------------------------

func TestExample19Jobs(t *testing.T) {
	shellState := exDefineState("example.shell.state", documents.StateOptions{
		Schema:  exObj(exProp("output", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("output", "") },
	})
	one := 1
	// 1. The job: closed args (checked at every start), a result, a state document per run, one run at a time.
	shellJob, err := DefineJob("example.shell", JobOptions{
		Version:     1,
		Args:        exObj(exProp("command", exStr()), exProp("lines", exInt())),
		Result:      exObj(exProp("exit", exInt()), exProp("output", exStr())),
		State:       shellState,
		Concurrency: &one,
	})
	must(t, err)
	// A second job `finish` chains: it announces a finished command.
	notifyJob, err := DefineJob("example.notify", JobOptions{Version: 1, Args: exObj(exProp("text", exStr())), Result: exStr()})
	must(t, err)
	entriesDoc := exStringListDoc("example.entries")
	shell := exObj(exProp("id", exStr()), exProp("phase", exStr()), exProp("command", exStr()), exProp("output", exStr()))
	guestShell := exObj(exProp("id", exStr()), exProp("phase", exStr()), exProp("command", exStr()))
	runArgs := exObj(exProp("command", exStr()), exProp("lines", exInt()), exProp("requestId", exStr()))
	chatStore := exLocal("example.chat-store", contracts.Params{},
		contracts.Mem("entries", exArr(exStr())), contracts.Mem("shells", exArr(shell)),
		contracts.Mem("runShell", contracts.NewMethod(runArgs, exStr())),
		contracts.Mem("abortShell", contracts.NewMethod(exObj(exProp("id", exStr())))))
	chat := exRemote("example.chat", contracts.Params{}, exServerAddress,
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("entries", exArr(exStr())), exProp("shells", exArr(shell))},
			Methods: []contracts.MethodEntry{
				contracts.Meth("runShell", contracts.NewMethod(runArgs, exStr())),
				contracts.Meth("abortShell", contracts.NewMethod(exObj(exProp("id", exStr())))),
			},
		}),
		contracts.Var("guest", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("entries", exArr(exStr())), exProp("shells", exArr(guestShell))},
		}))
	// A fake process: one line every 30 ms; `Sleep` returns when the run is aborted.
	runProcess := func(lines int, ctx context.Context, onLine func(string)) (int, error) {
		for n := 1; n <= lines; n++ {
			if err := Sleep(30*time.Millisecond, ctx); err != nil {
				return 0, err
			}
			onLine(fmt.Sprintf("line %d\n", n))
		}
		return 0, nil
	}
	errs := &hostErrors{}
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(chatStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			entries := stateOf(self, entriesDoc)
			notify := self.Job(notifyJob, JobImplementation{
				Run: func(args any, _ JobCtx) (any, error) {
					return "notified: " + exParam(args.(*delta.JsonObject), "text"), nil
				},
				Finish: func(outcome Outcome, tx documents.Tx) error {
					if outcome.Kind == OutcomeDone {
						draft, err := tx.Draft(entries.s)
						if err != nil {
							return err
						}
						_, err = draft.Array("list").Push(outcome.Result)
						return err
					}
					return nil
				},
			})
			shellHandle := self.Job(shellJob, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					lines := int(args.(*delta.JsonObject).Value("lines").(float64))
					// The output's bounds are required: a tail of at most 2,000 lines and 64 KiB.
					out := output.NewOutputBuffer(output.OutputLimits{MaxBytes: 64 * 1024, MaxLines: 2000, Retain: "tail"})
					// The buffer is single-threaded in Pi; Progress writes on a task of its own here.
					var outMu sync.Mutex
					write := func() (int, error) {
						outMu.Lock()
						text := out.Snapshot().Text
						outMu.Unlock()
						_, err := ctx.State().Change(func(draft *delta.Object) (any, error) {
							return nil, draft.Set("output", text)
						}, ctx, ChangeOptions{})
						return len(text), err
					}
					progress := output.NewProgress(write, func(err error) { errs.push(HostError{Kind: "progress_failed", Error: err}) }, 50)
					exit, runErr := runProcess(lines, ctx, func(line string) {
						outMu.Lock()
						out.PushString(line)
						outMu.Unlock()
						progress.Mark()
					})
					// The last output reaches `outcome.state`, also after an abort (changes are taken until run settles).
					output.SettleAndAwait(progress.Stop(), nil)
					_, _ = write()
					if runErr != nil {
						return nil, runErr
					}
					outMu.Lock()
					defer outMu.Unlock()
					return jsonObject("exit", float64(exit), "output", out.Snapshot().Text), nil
				},
				Finish: func(outcome Outcome, tx documents.Tx) error {
					args := outcome.Args.(*delta.JsonObject)
					command := exParam(args, "command")
					lines := strings.Count(outcome.State.(*delta.JsonObject).Value("output").(string), "\n")
					var text string
					if outcome.Kind == OutcomeDone {
						text = fmt.Sprintf("%s: exit %d, %d lines", command, int(outcome.Result.(*delta.JsonObject).Value("exit").(float64)), lines)
					} else {
						text = fmt.Sprintf("%s: interrupted (%s), %d lines kept", command, outcome.Error.Kind, lines)
					}
					draft, err := tx.Draft(entries.s)
					if err != nil {
						return err
					}
					if _, err := draft.Array("list").Push(text); err != nil {
						return err
					}
					// Chaining: the next job starts atomically with this outcome (both commit or neither).
					_, err = tx.Start(notify.(documents.JobHandle), jsonObject("text", command))
					return err
				},
			})
			// What the views read: one object per run.
			toShell := func(running any) any {
				out := []any{}
				for _, entry := range running.([]any) {
					object := entry.(*delta.JsonObject)
					if object.Value("phase") == "blocked" {
						out = append(out, jsonObject("id", object.Value("id"), "phase", object.Value("phase"), "command", "?", "output", ""))
						continue
					}
					out = append(out, jsonObject("id", object.Value("id"), "phase", object.Value("phase"),
						"command", object.Value("args").(*delta.JsonObject).Value("command"),
						"output", object.Value("state").(*delta.JsonObject).Value("output")))
				}
				return out
			}
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"entries": self.Derive([]any{entries.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
				"shells": self.Derive([]any{shellHandle.Running()}, func(values []any) (delta.JsonValue, error) {
					return toShell(values[0]), nil
				}),
			}, map[string]contracts.MethodFunction{
				"runShell": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					key := exParam(args, "requestId")
					run, err := shellHandle.Start(jsonObject("command", args.Value("command"), "lines", args.Value("lines")), ctx, documents.StartOptions{Key: &key})
					if err != nil {
						return nil, err
					}
					return run.ID(), nil
				}),
				"abortShell": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					go func() { _, _ = shellHandle.Abort(exParam(args, "id"), background) }()
					return nil, nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(chat, func(_ *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error) {
			store := self.Get(chatStore, jsonObject())
			if principalKind(principal) != "owner" {
				// A guest sees no output: the view drops it.
				return exVariant("guest", map[string]chord.Watchable[delta.JsonValue]{
					"entries": Expose(store.Member("entries")),
					"shells": self.Derive([]any{store.Member("shells")}, func(values []any) (delta.JsonValue, error) {
						out := []any{}
						for _, entry := range values[0].([]any) {
							object := entry.(*delta.JsonObject)
							out = append(out, jsonObject("id", object.Value("id"), "phase", object.Value("phase"), "command", object.Value("command")))
						}
						return out, nil
					}),
				}, nil), nil
			}
			return exVariant("owner", map[string]chord.Watchable[delta.JsonValue]{
				"entries": Expose(store.Member("entries")), "shells": Expose(store.Member("shells")),
			}, map[string]contracts.MethodFunction{
				"runShell": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("runShell", args, ctx)
				},
				"abortShell": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("abortShell", args, ctx)
				},
			}), nil
		}, ServeOptions{})
	})
	// 2. A server and two tabs, the owner's and a guest's.
	server, serverErrors := exServer(t, nil, HostFacet{Name: "@example/chat", Facet: facet})
	ownerTab := exAccept(t, server, ownerPrincipal())
	guestTab := exAccept(t, server, jsonObject("kind", "guest"))
	owner := exGet(t, ownerTab.Host, chat, jsonObject())
	guest := exGet(t, guestTab.Host, chat, jsonObject())
	if exVariantName(owner) != "owner" || exVariantName(guest) != "guest" {
		t.Fatalf("variants: %q %q", exVariantName(owner), exVariantName(guest))
	}
	// 3. The owner runs a command; its request id dedupes a resent request (the same run comes back).
	id := exCall(t, owner, "runShell", jsonObject("command", "npm test", "lines", float64(5), "requestId", "r1"))
	again := exCall(t, owner, "runShell", jsonObject("command", "npm test", "lines", float64(5), "requestId", "r1"))
	if id != again {
		t.Fatalf("a resent request returns another run: %v, %v", id, again)
	}
	scopeEventually(t, "the owner sees the output", func() bool {
		list := exValue(t, owner, "shells").([]any)
		return len(list) == 1 && list[0].(*delta.JsonObject).Value("output") != ""
	})
	ownerShell := exValue(t, owner, "shells").([]any)[0].(*delta.JsonObject)
	if ownerShell.Value("id") != id || ownerShell.Value("phase") != "running" || ownerShell.Value("command") != "npm test" {
		t.Fatalf("owner sees: %s", exText(ownerShell))
	}
	scopeEventually(t, "the guest sees the run", func() bool {
		list := exValue(t, guest, "shells").([]any)
		return len(list) == 1 && list[0].(*delta.JsonObject).Value("id") == id
	})
	guestSees := exValue(t, guest, "shells").([]any)[0].(*delta.JsonObject)
	if _, hasOutput := guestSees.Get("output"); hasOutput || guestSees.Value("command") != "npm test" {
		t.Fatalf("guest sees: %s", exText(guestSees))
	}
	// 4. One run at a time: a second command is refused while the first runs.
	_, err = owner.Call("runShell", jsonObject("command", "npm run build", "lines", float64(1), "requestId", "r2"), background)
	_ = exCode(t, err, data.CodeUnavailable, data.ReasonBusy)
	scopeEventually(t, "entries after the first run", func() bool {
		return exJSONEqual(exValue(t, owner, "entries"), exJSON(t, `["npm test: exit 0, 5 lines","notified: npm test"]`))
	})
	// 5. Abort is a request: the run stops at its next `sleep`, its last output is flushed, `finish` records it.
	long := exCall(t, owner, "runShell", jsonObject("command", "tail -f log", "lines", float64(100), "requestId", "r3"))
	scopeEventually(t, "the long run has output", func() bool {
		list := exValue(t, owner, "shells").([]any)
		return len(list) == 1 && list[0].(*delta.JsonObject).Value("id") == long && list[0].(*delta.JsonObject).Value("output") != ""
	})
	exCall(t, owner, "abortShell", jsonObject("id", long))
	scopeEventually(t, "entries after the abort", func() bool { return len(exValue(t, owner, "entries").([]any)) == 4 })
	entries := exValue(t, owner, "entries").([]any)
	if text := entries[2].(string); !strings.HasPrefix(text, "tail -f log: interrupted (aborted), ") || !strings.HasSuffix(text, " lines kept") || entries[3] != "notified: tail -f log" {
		t.Fatalf("entries after the abort: %s", exText(entries))
	}
	scopeEventually(t, "nothing running", func() bool { return len(exValue(t, owner, "shells").([]any)) == 0 })
	exCloseHost(t, ownerTab.Host)
	exCloseHost(t, guestTab.Host)
	exCloseHost(t, server)
	if errs.Len() > 0 || serverErrors.Len() > 0 {
		t.Fatalf("background errors: %v %v", errs.All(), serverErrors.All())
	}
}

// exFileStore opens a store over the SQLite file at path, as `openStore(sqliteBackend(await openNodeSqliteDatabase(path)))`;
// the app owns it: the test closes it after the host.
func exFileStore(t testing.TB, path string) *store.ChordStore {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	must(t, err)
	opened, err := store.OpenStore(store.SqliteBackend(database), background, store.StoreOptions{})
	must(t, err)
	return opened.(*store.ChordStore)
}

// exCloseStore is `await store.close(context)`.
func exCloseStore(t testing.TB, st store.Store) {
	t.Helper()
	must(t, st.Close(background))
}

// --- 20-tables.ts --------------------------------------------------------------------------------------------------

func TestExample20Tables(t *testing.T) {
	// Flat columns, defaults, a key of non-opt str or int columns, and indexes, which end with the key.
	edits, err := store.DefineTable("edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()), store.Col("path", store.Str()), store.Col("at", store.Int()),
			store.Col("hits", store.Int(contracts.Default(0))), store.Col("done", store.Bool(contracts.Default(false))),
			store.Col("note", store.Opt(store.Str())),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
	})
	must(t, err)
	history := exLocal("example.history", contracts.Params{},
		contracts.Mem("record", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("path", exStr()), exProp("at", exInt())))),
		contracts.Mem("hit", contracts.NewMethod(exStr())), contracts.Mem("finish", contracts.NewMethod(exStr())),
		contracts.Mem("hits", contracts.NewMethod(exStr(), exInt())),
		contracts.Mem("open", contracts.NewMethod(exObj(exProp("after", exOpt(exStr()))),
			exObj(exProp("ids", exArr(exStr())), exProp("next", exOpt(exStr()))))),
		contracts.Mem("finished", contracts.NewMethod(exObj(), exInt())))
	column := store.NewColumnRef
	facet := func(h FacetHost, _ FacetSelf) error {
		// Tables need a stable identity: a local type with empty params (one object per host).
		return h.Serve(history, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			table := self.(StoreSelf).Table(edits)
			// A query of the table: a condition and an order (the key is the tie-break); immutable, built once.
			open := store.NewQuery(edits).Where(func(row store.ColumnRefs) store.Condition { return store.Eq(row["done"], false) }).
				OrderBy(func(row store.ColumnRefs) *store.ColumnRef { return row["at"] }, "desc")
			return exImpl(nil, map[string]contracts.MethodFunction{
				"record": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					// Writes are staged synchronously (a bad row fails here) and commit together in one step.
					row := store.Row{"id": args.Value("id"), "path": args.Value("path"), "at": int64(args.Value("at").(float64))}
					_, err := self.Tx(func(tx *documents.Transaction) (any, error) { return nil, tx.Put(table, row) }, ctx)
					return nil, err
				}),
				"hit": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// A set is values or an expression over the row's columns, computed by the store.
					_, err := self.Tx(func(tx *documents.Transaction) (any, error) {
						return nil, tx.Update(table, store.Row{"id": id}, store.Row{"hits": store.Add(column("hits"), 1)})
					}, ctx)
					return nil, err
				},
				"finish": func(path delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// One statement for every matching row; its count is recorded with the write.
					_, err := self.Tx(func(tx *documents.Transaction) (any, error) {
						return nil, tx.UpdateWhere(table, store.Prefix(column("path"), path.(string)), store.Row{"done": true})
					}, ctx)
					return nil, err
				},
				"hits": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					row, err := table.Get(store.Row{"id": id}, ctx)
					if err != nil || row == nil {
						return float64(0), err
					}
					return float64(row["hits"].(int64)), nil
				},
				"open": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					options := store.PageOptions{}
					if after, ok := args.Get("after"); ok && after != nil {
						cursor := after.(string)
						options.After = &cursor
					}
					page, err := table.Page(open, 1, ctx, options)
					if err != nil {
						return nil, err
					}
					ids := []any{}
					for _, row := range page.Items {
						ids = append(ids, row["id"])
					}
					result := jsonObject("ids", ids)
					if page.Next != nil {
						result.Set("next", *page.Next)
					}
					return result, nil
				}),
				"finished": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					count, err := table.Count(store.NewQuery(edits).Where(func(row store.ColumnRefs) store.Condition { return store.Eq(row["done"], true) }), ctx)
					return float64(count), err
				}),
			}), nil
		}, ServeOptions{})
	}
	// A store over SQLite (`:memory:` here, a file in an app): the app opens it, gives it to one host, and closes it.
	st := exFileStore(t, ":memory:")
	host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
	ref := exGet(t, host, history, jsonObject())
	for i := range 5 {
		path := "src/b.ts"
		if i%2 == 0 {
			path = "src/a.ts"
		}
		exCall(t, ref, "record", jsonObject("id", fmt.Sprintf("e%d", i), "path", path, "at", float64(i)))
	}
	exCall(t, ref, "hit", "e1")
	exCall(t, ref, "hit", "e1")
	exCall(t, ref, "finish", "src/a")
	exEqual(t, "hits of e1", exCall(t, ref, "hits", "e1"), float64(2))
	// Pages continue from an opaque cursor.
	first := exCall(t, ref, "open", jsonObject()).(*delta.JsonObject)
	exEqual(t, "open, first page", first.Value("ids"), exJSON(t, `["e3"]`))
	next := exCall(t, ref, "open", jsonObject("after", first.Value("next"))).(*delta.JsonObject)
	exEqual(t, "open, next page", next.Value("ids"), exJSON(t, `["e1"]`))
	exEqual(t, "finished", exCall(t, ref, "finished", jsonObject()), float64(3))
	exCloseHost(t, host)
	exCloseStore(t, st)
}

// --- 21-persisted-documents.ts -------------------------------------------------------------------------------------

func TestExample21PersistedDocuments(t *testing.T) {
	// A persisted singleton: loaded once when its object activates; every 20th stored change is a new base.
	tags := exDefineState("tags", documents.StateOptions{
		Schema:  exObj(exProp("tags", exArr(exStr()))),
		Initial: func() *delta.JsonObject { return jsonObject("tags", []any{}) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 20
		},
	})
	// A persisted keyed document: each key loaded on its first read, then cached until idle for ten minutes.
	notes := exDefineState("notes", documents.StateOptions{
		Schema:         exObj(exProp("body", exStr())),
		Initial:        func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:          true,
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	notebook := exLocal("example.notebook", contracts.Params{},
		contracts.Mem("tags", exObj(exProp("tags", exArr(exStr())))),
		contracts.Mem("write", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("body", exStr()), exProp("tag", exStr())))),
		contracts.Mem("read", contracts.NewMethod(exStr(), exStr())))
	facet := func(h FacetHost, _ FacetSelf) error {
		// Persisted documents need a stable identity, as tables do: a local type with empty params.
		return h.Serve(notebook, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			tagsDoc := stateOf(self, tags)
			notesDoc := self.State(notes).(*documents.Keyed)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"tags": tagsDoc.Member()}, map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					// Both documents commit in one store step, or neither does.
					_, err := self.Tx(func(tx *documents.Transaction) (any, error) {
						draft, err := tx.Draft(notesDoc, exParam(args, "id"))
						if err != nil {
							return nil, err
						}
						if err := draft.Set("body", args.Value("body")); err != nil {
							return nil, err
						}
						tagsDraft, err := tx.Draft(tagsDoc.s)
						if err != nil {
							return nil, err
						}
						for _, existing := range tagsDraft.Array("tags").Snapshot() {
							if existing == args.Value("tag") {
								return nil, nil
							}
						}
						_, err = tagsDraft.Array("tags").Push(args.Value("tag"))
						return nil, err
					}, ctx)
					return nil, err
				}),
				"read": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					body, err := notesDoc.Get(id.(string), ctx)
					if err != nil || body == nil {
						return "(none)", err
					}
					return body.Value("body"), nil
				},
			}), nil
		}, ServeOptions{})
	}
	// session is a host on the store at path, run until work is done with the notebook.
	session := func(path string, work func(notebook *Reference)) {
		st := exFileStore(t, path)
		host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
		work(exGet(t, host, notebook, jsonObject()))
		exCloseHost(t, host)
		exCloseStore(t, st)
	}
	path := t.TempDir() + "/notebook.db"
	session(path, func(ref *Reference) {
		exCall(t, ref, "write", jsonObject("id", "groceries", "body", "oat milk", "tag", "home"))
		exCall(t, ref, "write", jsonObject("id", "standup", "body", "demo the store", "tag", "work"))
		exEqual(t, "first host, tags", exValue(t, ref, "tags").(*delta.JsonObject).Value("tags"), exJSON(t, `["home","work"]`))
	})
	// A new host on the same file: the documents come back from the store.
	session(path, func(ref *Reference) {
		exEqual(t, "second host, tags", exValue(t, ref, "tags").(*delta.JsonObject).Value("tags"), exJSON(t, `["home","work"]`))
		exEqual(t, "second host, standup", exCall(t, ref, "read", "standup"), "demo the store")
	})
}

// --- 22-migrations.ts ----------------------------------------------------------------------------------------------

func TestExample22Migrations(t *testing.T) {
	// Version 1, the base, written in full.
	editsV1, err := store.DefineTable("edits", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("file", store.Str()), store.Col("at", store.Int())}, Key: []string{"id"},
	})
	must(t, err)
	// Version 2: `file` becomes `path`, a `dir` column is added and filled from each row (the data step).
	editsV2, err := store.EvolveTable(editsV1, func(e *store.TableEditor) *store.TableEditor {
		return e.Rename("file", "path").Add("dir", store.Str(contracts.Default(""))).Rows(func(row store.Row) (any, error) {
			path := row["path"].(string)
			return store.Row{"dir": path[:strings.LastIndex(path, "/")]}, nil
		})
	})
	must(t, err)
	// Version 3: `dir` is dropped again, an index is added. Data functions stay as deployed; history only grows.
	editsV3, err := store.EvolveTable(editsV2, func(e *store.TableEditor) *store.TableEditor {
		return e.Drop("dir").Index("by_path", []string{"path", "at"})
	})
	must(t, err)
	history := exLocal("example.history", contracts.Params{},
		contracts.Mem("record", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("at", exInt())))),
		contracts.Mem("dump", contracts.NewMethod(exObj(), exArr(exStr()))))
	// facetOf is the facet of one deployment: its newest table version, and how it writes a row.
	facetOf := func(definition *store.TableDefinition, columns []string, row func(id string, at int64) store.Row) func(FacetHost, FacetSelf) error {
		return func(h FacetHost, _ FacetSelf) error {
			return h.Serve(history, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				edits := self.(StoreSelf).Table(definition)
				return exImpl(nil, map[string]contracts.MethodFunction{
					"record": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx *documents.Transaction) (any, error) {
							return nil, tx.Put(edits, row(exParam(args, "id"), int64(args.Value("at").(float64))))
						}, ctx)
						return nil, err
					}),
					"dump": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						lines := []any{}
						rows := edits.Rows(store.NewQuery(definition), ctx)
						for {
							entry, ok, err := rows.Next(ctx)
							if err != nil {
								return nil, err
							}
							if !ok {
								return lines, nil
							}
							// JSON.stringify(row): the keys in column order.
							ordered := delta.NewJsonObject(len(columns))
							for _, column := range columns {
								ordered.Set(column, exNumber(entry[column]))
							}
							lines = append(lines, exText(ordered))
						}
					}),
				}), nil
			}, ServeOptions{})
		}
	}
	// deployment is a host on the store at path serving facet, run until work is done.
	deployment := func(path string, facet func(FacetHost, FacetSelf) error, work func(ref *Reference)) {
		st := exFileStore(t, path)
		// Objects with empty params activate at load: the migration runs before `load` resolves.
		host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
		work(exGet(t, host, history, jsonObject()))
		exCloseHost(t, host)
		exCloseStore(t, st)
	}
	path := t.TempDir() + "/history.db"
	deployment(path, facetOf(editsV1, []string{"id", "file", "at"}, func(id string, at int64) store.Row {
		return store.Row{"id": id, "file": "src/" + id + ".ts", "at": at}
	}), func(ref *Reference) {
		for i := range 3 {
			exCall(t, ref, "record", jsonObject("id", fmt.Sprintf("e%d", i), "at", float64(i)))
		}
		exEqual(t, "version 1", exCall(t, ref, "dump", jsonObject()),
			exJSON(t, `["{\"id\":\"e0\",\"file\":\"src/e0.ts\",\"at\":0}","{\"id\":\"e1\",\"file\":\"src/e1.ts\",\"at\":1}","{\"id\":\"e2\",\"file\":\"src/e2.ts\",\"at\":2}"]`))
	})
	// The next deployment declares version 3: the store goes through 2 (rename, add, rows) and 3 (drop, index).
	deployment(path, facetOf(editsV3, []string{"id", "path", "at"}, func(id string, at int64) store.Row {
		return store.Row{"id": id, "path": "lib/" + id + ".ts", "at": at}
	}), func(ref *Reference) {
		exCall(t, ref, "record", jsonObject("id", "e9", "at", float64(9)))
		exEqual(t, "version 3", exCall(t, ref, "dump", jsonObject()),
			exJSON(t, `["{\"id\":\"e0\",\"path\":\"src/e0.ts\",\"at\":0}","{\"id\":\"e1\",\"path\":\"src/e1.ts\",\"at\":1}","{\"id\":\"e2\",\"path\":\"src/e2.ts\",\"at\":2}","{\"id\":\"e9\",\"path\":\"lib/e9.ts\",\"at\":9}"]`))
	})
}

// --- 23-keyed-watches.ts -------------------------------------------------------------------------------------------

func TestExample23KeyedWatches(t *testing.T) {
	notesDef := exDefineState("notes", documents.StateOptions{
		Schema:  exObj(exProp("body", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:   true,
	})
	notebook := exLocal("example.notebook", contracts.Params{},
		// The note "pinned" as a state member: `initial()` while absent.
		contracts.Mem("pinned", exObj(exProp("body", exStr()))),
		contracts.Mem("write", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("body", exStr())))),
		contracts.Mem("remove", contracts.NewMethod(exStr())))
	// The example's own window into the object: its keyed handle, to watch other keys from outside.
	var notes *documents.Keyed
	host, _ := exOpenHost(t, loadedOptions{}, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notebook, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(notesDef).(*documents.Keyed)
			notes = handle
			pinned, err := handle.At("pinned")
			if err != nil {
				return contracts.Implementation{}, err
			}
			// Pinned while this attempt lives, and loaded before the object goes live.
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"pinned": Expose(pinned)}, map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					_, err := handle.Change(exParam(args, "id"), func(draft *delta.Object) (any, error) {
						return nil, draft.Set("body", args.Value("body"))
					}, ctx)
					return nil, err
				}),
				"remove": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, handle.Retire(id.(string), ctx)
				},
			}), nil
		}, ServeOptions{})
	})
	ref := exGet(t, host, notebook, jsonObject())
	// A watch of the state member: every value call carries the value, its ops and the retired flag.
	_, pinnedFrames := exWatch(t, background, ref.Member("pinned"))
	exCall(t, ref, "write", jsonObject("id", "pinned", "body", "buy milk"))
	// A watch of one key; an absent key gives `undefined` (watch it once it exists, or pin it with `at`).
	absent, err := notes.Watch("later", background)
	must(t, err)
	if absent != nil {
		t.Fatalf("watch of an absent key = %v, want none", absent)
	}
	exCall(t, ref, "write", jsonObject("id", "later", "body", "call Anna"))
	laterWatch, err := notes.Watch("later", background)
	must(t, err)
	if laterWatch == nil {
		t.Fatal("watch of a present key: none")
	}
	var laterFrames exFrames
	laterWatch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		laterFrames.mu.Lock()
		defer laterFrames.mu.Unlock()
		laterFrames.frames = append(laterFrames.frames, exFrame{Kind: string(call.Kind), Value: call.Value, Retired: call.Retired})
		return nil
	})
	exCall(t, ref, "remove", "later")
	exCall(t, ref, "remove", "pinned")
	// Frames are delivered after the commit, asynchronously.
	scopeEventually(t, "the retirements' frames", func() bool { return len(pinnedFrames.all()) == 2 && len(laterFrames.all()) >= 1 })
	pinned := pinnedFrames.all()
	exEqual(t, "pinned", pinned[0].Value, exJSON(t, `{"body":"buy milk"}`))
	if pinned[0].Retired || !pinned[1].Retired {
		t.Fatalf("pinned retired flags = %v, %v", pinned[0].Retired, pinned[1].Retired)
	}
	exEqual(t, "pinned after the retirement", pinned[1].Value, exJSON(t, `{"body":""}`))
	later := laterFrames.all()
	if last := later[len(later)-1]; !last.Retired {
		t.Fatalf("later: last frame not retired: %+v", later)
	}
	exCloseHost(t, host)
}

// --- 24-keyed-members.ts -------------------------------------------------------------------------------------------

func TestExample24KeyedMembers(t *testing.T) {
	profile := exObj(exProp("name", exStr()), exProp("status", exStr()))
	profiles := exDefineState("profiles", documents.StateOptions{
		Schema:  profile,
		Initial: func() *delta.JsonObject { return jsonObject("name", "", "status", "offline") },
		Keyed:   true,
	})
	// The directory: every user's profile, one keyed member.
	directory := exLocal("example.directory", contracts.Params{},
		contracts.Mem("profiles", contracts.Keyed(profile)),
		contracts.Mem("set", contracts.NewMethod(exObj(exProp("user", exStr()), exProp("name", exStr()), exProp("status", exStr())))))
	// A badge per user: its own object, showing one profile.
	badge := exLocal("example.badge", contracts.Params{exProp("user", exStr())}, contracts.Mem("label", exStr()))
	host, _ := exOpenHost(t, loadedOptions{}, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(directory, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(profiles).(*documents.Keyed)
			// The keyed handle is the member as it is.
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"profiles": ExposeKeyed(handle)},
				Methods: map[string]contracts.MethodFunction{
					"set": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := handle.Change(exParam(args, "user"), func(draft *delta.Object) (any, error) {
							if err := draft.Set("name", args.Value("name")); err != nil {
								return nil, err
							}
							return nil, draft.Set("status", args.Value("status"))
						}, ctx)
						return nil, err
					}),
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(badge, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			user := exParam(params, "user")
			// Pinned for this attempt; the badge goes live once the profile is loaded.
			profile, err := self.Get(directory, jsonObject()).Keyed("profiles").At(user)
			if err != nil {
				return contracts.Implementation{}, err
			}
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"label": self.Derive([]any{profile}, func(values []any) (delta.JsonValue, error) {
					entry := values[0].(*delta.JsonObject)
					name, _ := entry.Value("name").(string)
					if name == "" {
						name = user
					}
					return fmt.Sprintf("%s (%v)", name, entry.Value("status")), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	})
	dir := exGet(t, host, directory, jsonObject())
	exCall(t, dir, "set", jsonObject("user", "ana", "name", "Ana", "status", "online"))
	badgeRef := exGet(t, host, badge, jsonObject("user", "ana"))
	exEqual(t, "badge", exValue(t, badgeRef, "label"), "Ana (online)")
	exCall(t, dir, "set", jsonObject("user", "ana", "name", "Ana", "status", "away"))
	exEqual(t, "badge after a change", exValue(t, badgeRef, "label"), "Ana (away)")
	// Any key, read where it is needed.
	_, found, err := dir.Keyed("profiles").Get(background, "bo")
	must(t, err)
	if found {
		t.Fatal("ref.profiles.get(bo): found")
	}
	badgeRef.Release()
	exCloseHost(t, host)
}

// exBytesSource is a store.ByteSource over a file's contents, in chunks (`createReadStream`).
type exBytesSource struct {
	content []byte
	chunk   int
}

func (s *exBytesSource) Next(context.Context) ([]byte, bool, error) {
	if len(s.content) == 0 {
		return nil, false, nil
	}
	n := min(s.chunk, len(s.content))
	chunk := s.content[:n]
	s.content = s.content[n:]
	return chunk, true, nil
}

func (s *exBytesSource) Close() error { return nil }

// --- 25-export-import.ts -------------------------------------------------------------------------------------------

func TestExample25ExportImport(t *testing.T) {
	visits, err := store.DefineTable("visits", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("page", store.Str()), store.Col("count", store.Int())}, Key: []string{"page"},
	})
	must(t, err)
	title := exDefineState("title", documents.StateOptions{
		Schema:         exObj(exProp("text", exStr())),
		Initial:        func() *delta.JsonObject { return jsonObject("text", "untitled") },
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	site := exLocal("example.site", contracts.Params{},
		contracts.Mem("title", exObj(exProp("text", exStr()))), contracts.Mem("visit", contracts.NewMethod(exStr(), exInt())),
		contracts.Mem("rename", contracts.NewMethod(exStr())))
	facet := func(h FacetHost, _ FacetSelf) error {
		return h.Serve(site, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			table := self.(StoreSelf).Table(visits)
			titleDoc := stateOf(self, title)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"title": titleDoc.Member()}, map[string]contracts.MethodFunction{
				"visit": func(page delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					count, err := self.Tx(func(tx *documents.Transaction) (any, error) {
						row, err := tx.Get(table, store.Row{"page": page})
						if err != nil {
							return nil, err
						}
						next := int64(1)
						if row != nil {
							next = row["count"].(int64) + 1
						}
						return next, tx.Put(table, store.Row{"page": page, "count": next})
					}, ctx)
					if err != nil {
						return nil, err
					}
					return float64(count.(int64)), nil
				},
				"rename": func(text delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, titleDoc.Change(func(draft *delta.Object) { _ = draft.Set("text", text) }, ctx)
				},
			}), nil
		}, ServeOptions{})
	}
	// serve is a host on st serving the site, run until work is done.
	serve := func(st store.Store, work func(ref *Reference)) {
		host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
		work(exGet(t, host, site, jsonObject()))
		exCloseHost(t, host)
	}
	directory := t.TempDir()
	original := exFileStore(t, directory+"/a.db")
	serve(original, func(ref *Reference) {
		exCall(t, ref, "rename", "Chord")
		exCall(t, ref, "visit", "/")
		exCall(t, ref, "visit", "/")
		exCall(t, ref, "visit", "/docs")
	})
	// Export to a local file: the queue is held only while the parts are read.
	archive := directory + "/archive.jsonl"
	export, err := original.Export(background)
	must(t, err)
	encoded := store.EncodeJsonl(export)
	var written []byte
	for {
		chunk, ok, err := encoded.Next(background)
		must(t, err)
		if !ok {
			break
		}
		written = append(written, chunk...)
	}
	must(t, os.WriteFile(archive, written, 0o600))
	if len(written) == 0 {
		t.Fatal("archive: 0 bytes")
	}
	seq := original.LastSeq()
	exCloseStore(t, original)
	// Import into an empty backend: every part is checked; a refused archive leaves the backend empty.
	content, err := os.ReadFile(archive)
	must(t, err)
	database, err := node.OpenNodeSqliteDatabase(directory+"/b.db", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	copied, err := store.ImportStore(store.SqliteBackend(database), store.DecodeJsonl(&exBytesSource{content: content, chunk: 64 * 1024}), background, store.StoreOptions{})
	must(t, err)
	if copied.LastSeq() != seq {
		t.Fatalf("imported at seq %d, want %d", copied.LastSeq(), seq)
	}
	serve(copied, func(ref *Reference) {
		exEqual(t, "title", exValue(t, ref, "title").(*delta.JsonObject).Value("text"), "Chord")
		exEqual(t, "next visit of /", exCall(t, ref, "visit", "/"), float64(3))
	})
	exCloseStore(t, copied)
}

// --- 28-persisted-jobs.ts ------------------------------------------------------------------------------------------

func TestExample28PersistedJobs(t *testing.T) {
	importState := exDefineState("notes.import.state", documents.StateOptions{
		Schema:  exObj(exProp("done", exInt())),
		Initial: func() *delta.JsonObject { return jsonObject("done", float64(0)) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 16
		},
	})
	importJob, err := DefineJob("notes.import", JobOptions{
		Version: 1,
		Args:    exObj(exProp("path", exStr())),
		Result:  exObj(exProp("count", exInt())),
		State:   importState,
		// After a crash or a stop, `run` runs again: it must be idempotent.
		Persist: &JobPersistence{Resume: ResumeRerun},
	})
	must(t, err)
	notes := exLocal("notes.store", contracts.Params{},
		contracts.Mem("start", contracts.NewMethod(exStr(), exStr())), contracts.Mem("abort", contracts.NewMethod(exStr(), exStr())))
	// How the current process's `run` behaves: hang (to be cut off) or finish.
	var behaviour struct {
		sync.Mutex
		finish bool
	}
	var logs exCollector
	var jobs struct {
		sync.Mutex
		handle JobHandle
	}
	facet := func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			imports := self.Job(importJob, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					logs.add(fmt.Sprintf("run %s: from done = %v", exParam(args.(*delta.JsonObject), "path"), mustState(ctx).Value("done")))
					// Progress, deferred: published at once, stored within 100 ms (or with the next immediate change).
					deferred := documents.CommitPersistence{Kind: documents.CommitDeferred, WithinMs: new(100.0)}
					if _, err := ctx.State().Change(func(draft *delta.Object) (any, error) {
						return nil, draft.Set("done", draft.Get("done").(float64)+1)
					}, ctx, ChangeOptions{Persistence: &deferred}); err != nil {
						return nil, err
					}
					behaviour.Lock()
					finish := behaviour.finish
					behaviour.Unlock()
					if !finish {
						UntilAborted(ctx)
						return nil, context.Cause(ctx)
					}
					return jsonObject("count", mustState(ctx).Value("done")), nil
				},
				Finish: func(outcome Outcome, _ documents.Tx) error {
					if outcome.Kind == OutcomeDone {
						logs.add("finish: done " + exText(outcome.Result))
					} else {
						logs.add("finish: " + string(outcome.Kind) + " " + string(outcome.Error.Kind))
					}
					return nil
				},
			})
			jobs.Lock()
			jobs.handle = imports
			jobs.Unlock()
			return exImpl(nil, map[string]contracts.MethodFunction{
				"start": func(path delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					run, err := imports.Start(jsonObject("path", path), ctx, documents.StartOptions{})
					if err != nil {
						return nil, err
					}
					return run.ID(), nil
				},
				"abort": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					result, err := imports.Abort(id.(string), ctx)
					return string(result), err
				},
			}), nil
		}, ServeOptions{})
	}
	// process is a store over the file and a host serving `notes`.
	process := func(path string) (*store.ChordStore, Host) {
		st := exFileStore(t, path)
		closeTimeout := 100 * time.Millisecond
		host := newHost(t, HostOptions{Store: st, CloseTimeout: &closeTimeout, OnError: func(err HostError) { logs.add("onError: " + string(err.Kind)) }})
		must(t, host.Load([]HostFacet{{Name: "@example/notes", Facet: DefineFacet(facet)}}, background))
		return st, host
	}
	path := t.TempDir() + "/session.sqlite"
	// 1. Start a run, then crash: the store closes under the live host.
	st, host := process(path)
	notesRef := exGet(t, host, notes, jsonObject())
	id := exCall(t, notesRef, "start", "inbox").(string)
	scopeEventually(t, "run 1", func() bool { return len(logs.all()) >= 1 })
	// The run's state: its base at the start, then the deferred change, stored by the object's timer within 100 ms.
	scopeEventually(t, "the deferred change was stored", func() bool { return len(stateRevisions(t, st)) >= 2 })
	exCloseStore(t, st)
	// 2. The next process counts the crash and reruns it (its state as stored: the deferred change was stored in time).
	st, host = process(path)
	loggedLen := -1
	scopeEventually(t, "the run is rerun", func() bool {
		if n := len(logs.all()); n != loggedLen {
			loggedLen = n
			t.Logf("log: %v", logs.all())
		}
		return slices_Contains(logs.all(), "run inbox: from done = 1")
	})
	scopeEventually(t, "the rerun's deferred change was stored", func() bool { return len(stateRevisions(t, st)) >= 3 })
	// 3. A clean close writes the run back `stopped`: the next process resumes it without counting.
	exCloseHost(t, host)
	exCloseStore(t, st)
	behaviour.Lock()
	behaviour.finish = true
	behaviour.Unlock()
	st, host = process(path)
	loggedFinish := -1
	scopeEventually(t, "the run finishes", func() bool {
		if n := len(logs.all()); n != loggedFinish {
			loggedFinish = n
			t.Logf("log: %v", logs.all())
		}
		return slices_Contains(logs.all(), `finish: done {"count":3}`)
	})
	// unsettled is `jobs.running.value.length`, read inside a section of the host.
	unsettled := func(on Host) int {
		jobs.Lock()
		handle := jobs.handle
		jobs.Unlock()
		count := 0
		on.(*chordHost).enter(func() {
			running, err := handle.Running().Value()
			must(t, err)
			count = len(running.([]any))
		})
		return count
	}
	scopeEventually(t, "no unsettled runs", func() bool { return unsettled(host) == 0 })
	// 4. A stored abort: marked, joined.
	behaviour.Lock()
	behaviour.finish = false
	behaviour.Unlock()
	notesRef = exGet(t, host, notes, jsonObject())
	second := exCall(t, notesRef, "start", "drafts").(string)
	scopeEventually(t, "the second run runs", func() bool { return slices_Contains(logs.all(), "run drafts: from done = 0") })
	exEqual(t, "abort", exCall(t, notesRef, "abort", second), "marked")
	scopeEventually(t, "the aborted run settled", func() bool { return unsettled(host) == 0 })
	exEqual(t, "once settled", exCall(t, notesRef, "abort", second), "terminal")
	exEqual(t, "the first run", exCall(t, notesRef, "abort", id), "terminal")
	exCloseHost(t, host)
	exCloseStore(t, st)
	want := []string{"run inbox: from done = 0", "run inbox: from done = 1", "run inbox: from done = 2", `finish: done {"count":3}`}
	got := []string{}
	for _, line := range logs.all() {
		if !strings.HasPrefix(line, "onError") {
			got = append(got, line)
		}
	}
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("log = %v, want prefix %v", got, want)
	}
}

// mustState is the run state's value (`ctx.state.value`).
func mustState(ctx JobCtx) *delta.JsonObject {
	value, err := ctx.State().Value()
	if err != nil {
		panic(err)
	}
	return value
}

// --- 29-registries.ts ----------------------------------------------------------------------------------------------

// exHandler is a route's handler: it returns the response, or an error where Pi's throws.
type exHandler func(path string) (string, error)

// exRoutes is a plugin's contribution to the http registry: its routes, in the order it declares them.
type exRoutes struct {
	Routes []registries.RecordEntry[exHandler]
}

func TestExample29Registries(t *testing.T) {
	// 1. The token, shared by every plugin (bundled like a contract): routes in, a router out.
	http, err := registries.DefineRegistry[any, any]("example.http", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	// The HTTP server: it reads the router at each request and guards the contribution it calls.
	server := exLocal("example.server", contracts.Params{},
		contracts.Mem("request", contracts.NewMethod(exObj(exProp("path", exStr())), exStr())))
	// A plugin whose routes are known only at runtime: it contributes a writable it sets.
	mounts := exLocal("example.mounts", contracts.Params{}, contracts.Mem("mount", contracts.NewMethod(exObj(exProp("path", exStr())))))
	var logs exCollector
	// 2. The defining plugin serves the registry: the first route by plugin name wins, a duplicate is reported.
	httpPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(registries.Registry[any, any](http), registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], composeContext registries.ComposeContext) (any, error) {
				typed := make(registries.RegistryTable[exRoutes], len(table))
				for i, entry := range table {
					typed[i] = registries.RegistryEntry[exRoutes]{Plugin: entry.Plugin, Type: entry.Type, Version: entry.Version, Value: entry.Value.(exRoutes)}
				}
				return registries.KeyedEntries(typed, func(value exRoutes) []registries.RecordEntry[exHandler] { return value.Routes }, composeContext.Report), nil
			},
		}.Erase(), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(server, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			router := self.Registry(registries.Registry[any, any](http))
			return exImpl(nil, map[string]contracts.MethodFunction{
				"request": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					path := exParam(args, "path")
					value := router.Value()
					route, ok := value.(*registries.KeyedItems[exHandler]).Get(path)
					if !ok {
						return "404", nil
					}
					// A contribution is another plugin's code: catch, report its plugin, go on.
					response, err := route.Value(path)
					if err != nil {
						logs.add(fmt.Sprintf("route %s of %s threw: %v", path, route.Plugin, err))
						return "500", nil
					}
					return response, nil
				}),
			}), nil
		}, ServeOptions{})
	})
	// 3. Two plugins contribute from their facets: plain values, in the table while the plugin is loaded.
	notesPlugin := DefineFacet(func(_ FacetHost, self FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http), exRoutes{Routes: []registries.RecordEntry[exHandler]{
			{Key: "/notes", Value: func(string) (string, error) { return "notes", nil }},
			{Key: "/broken", Value: func(string) (string, error) {
				return "", errors.New("Expected property name or '}' in JSON at position 1")
			}},
		}})
	})
	searchPlugin := DefineFacet(func(_ FacetHost, self FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http), exRoutes{Routes: []registries.RecordEntry[exHandler]{
			{Key: "/search", Value: func(string) (string, error) { return "results", nil }},
			{Key: "/notes", Value: func(string) (string, error) { return "search's notes", nil }},
		}})
	})
	// 4. A host-lifetime object contributes a writable: its changes recompose the router.
	mountsPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(mounts, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			routes := self.Writable(exRoutes{})
			self.(StoreSelf).Contribute(registries.Registry[any, any](http), routes)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"mount": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					path := exParam(args, "path")
					current := routes.Value().(exRoutes)
					err := routes.Set(exRoutes{Routes: append(append([]registries.RecordEntry[exHandler](nil), current.Routes...),
						registries.RecordEntry[exHandler]{Key: path, Value: func(string) (string, error) { return "mounted " + path, nil }})})
					return nil, err
				}),
			}), nil
		}, ServeOptions{})
	})
	var conflicts exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		if err.Kind == "registry_conflict" {
			conflicts.add(fmt.Sprintf("%s %v", err.Registry.Message, err.Registry.Plugins))
			return
		}
		errs.push(err)
	}})
	// 5. A reader before `load` waits: the provider comes with `load`, which composes every registry before any object.
	type early struct {
		value registries.RegistryValue[any]
		err   error
	}
	earlyRead := make(chan early, 1)
	go func() {
		value, err := host.Registry(registries.Registry[any, any](http), background, GetOptions{})
		earlyRead <- early{value, err}
	}()
	must(t, host.Load([]HostFacet{
		{Name: "@example/http", Facet: httpPlugin}, {Name: "@example/notes", Facet: notesPlugin},
		{Name: "@example/search", Facet: searchPlugin}, {Name: "@example/mounts", Facet: mountsPlugin},
	}, background))
	read := <-earlyRead
	must(t, read.err)
	routerNow := func() []string {
		value, err := read.value.Value(), error(nil)
		must(t, err)
		var out []string
		for key, item := range value.(*registries.KeyedItems[exHandler]).All() {
			out = append(out, key+" ("+item.Plugin+")")
		}
		return out
	}
	if got, want := routerNow(), []string{"/notes (@example/notes)", "/broken (@example/notes)", "/search (@example/search)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes: %v, want %v", got, want)
	}
	if got := conflicts.all(); len(got) != 1 || !strings.Contains(got[0], `"/notes" is contributed twice`) || !strings.Contains(got[0], "@example/notes @example/search") {
		t.Fatalf("conflicts = %v", got)
	}
	// 6. Requests: a duplicate went to the first plugin by name; a throwing contribution is guarded at the use site.
	serverRef := exGet(t, host, server, jsonObject())
	exEqual(t, "/notes", exCall(t, serverRef, "request", jsonObject("path", "/notes")), "notes")
	exEqual(t, "/broken", exCall(t, serverRef, "request", jsonObject("path", "/broken")), "500")
	if got := logs.all(); len(got) != 1 || !strings.HasPrefix(got[0], "route /broken of @example/notes threw: ") {
		t.Fatalf("guarded: %v", got)
	}
	// 7. A watch of the composed value gets each new router; mounting recomposes it.
	watch, err := read.value.Watch(background)
	must(t, err)
	var routers exCollector
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			var keys []string
			for key := range call.Value.(*registries.KeyedItems[exHandler]).All() {
				keys = append(keys, key)
			}
			routers.add(strings.Join(keys, ","))
		}
		return nil
	})
	mountsRef := exGet(t, host, mounts, jsonObject())
	exCall(t, mountsRef, "mount", jsonObject("path", "/files"))
	loggedRouters := -1
	scopeEventually(t, "the router with /files", func() bool {
		if n := len(routers.all()); n != loggedRouters {
			loggedRouters = n
			t.Logf("routers: %v", routers.all())
		}
		return slices_Contains(routers.all(), "/files,/notes,/broken,/search")
	})
	exEqual(t, "/files", exCall(t, serverRef, "request", jsonObject("path", "/files")), "mounted /files")
	_, _ = watch.Stop()
	exCloseHost(t, host)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

// --- 30-host-root.ts -----------------------------------------------------------------------------------------------

func TestExample30HostRoot(t *testing.T) {
	notes := exLocal("example.notes", contracts.Params{exProp("session", exStr())},
		contracts.Mem("list", exArr(exStr())), contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr())))))
	list := exStringListDoc("example.list")
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, list)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"list": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) {
						exPush(draft, fmt.Sprintf("%s (by %s)", exParam(args, "text"), principalKind(exCtx(ctx).Principal())))
					}, ctx)
				}),
			}), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: errs.push})
	// 1. A scope made before `load` resolved: its `ctx.get` parks until the host is loaded.
	type outcome struct {
		value any
		err   error
	}
	earlyDone := make(chan outcome, 1)
	go func() {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			reference, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
			if err != nil {
				return nil, err
			}
			return reference.Member("list").Value()
		}, background, RootOptions{})
		earlyDone <- outcome{value, err}
	}()
	must(t, host.Load([]HostFacet{{Name: "@example/notes", Facet: facet}}, background))
	early := <-earlyDone
	must(t, early.err)
	exEqual(t, "before load, read after it", early.value, exJSON(t, `[]`))
	// 2. A tool's call: get, call, return plain data. The reference is released when fn settles.
	addNote := func(text string) any {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			reference, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
			if err != nil {
				return nil, err
			}
			if _, err := reference.Call("add", jsonObject("text", text), ctx); err != nil {
				return nil, err
			}
			return reference.Member("list").Value()
		}, background, RootOptions{})
		must(t, err)
		return value
	}
	exEqual(t, "after a tool call", addNote("first"), exJSON(t, `["first (by server)"]`))
	// 3. Base code acting for an authenticated user names the principal.
	owner := jsonObject("kind", "owner", "name", "ada")
	principal, err := host.Root(func(ctx Ctx) (any, error) { return ctx.Principal(), nil }, background, RootOptions{Principal: owner})
	must(t, err)
	exEqual(t, "as the owner", principal, owner)
	// 4. A kept ctx is sealed once fn settled.
	var kept Ctx
	_, err = host.Root(func(ctx Ctx) (any, error) { kept = ctx; return nil, nil }, background, RootOptions{})
	must(t, err)
	if kept.Err() == nil {
		t.Fatal("kept ctx aborted: false")
	}
	_, err = kept.Get(notes, jsonObject("session", "s1"), GetOptions{})
	_ = exCode(t, err, data.CodeReleased, "")
	// 5. Cancelling the context rejects at once and seals the scope; an fn that ignores its signal fails at its next use
	// of Chord.
	cancellable, cancel := chordctx.WithCancel(background)
	stubborn := make(chan error, 1)
	released := make(chan struct{})
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			<-released
			_, err := ctx.Get(notes, jsonObject("session", "s1"), GetOptions{})
			return nil, err
		}, cancellable, RootOptions{})
		stubborn <- err
	}()
	scopeEventually(t, "the scope is open", func() bool { return exActivity(t, host).References >= 0 })
	cancel(errors.New("user pressed stop"))
	if err := <-stubborn; err == nil || err.Error() != "user pressed stop" {
		t.Fatalf("cancelled: %v", err)
	}
	close(released)
	// 6. Closing the host seals the scopes still open and rejects their callers at once: an fn sleeping on its ctx ends.
	sleeping := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) { close(started); return nil, Sleep(60*time.Second, ctx) }, background, RootOptions{})
		sleeping <- err
	}()
	<-started
	exCloseHost(t, host)
	_ = exCode(t, <-sleeping, data.CodeReleased, data.ReasonClosed)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

// --- 31-parked-dependencies.ts -------------------------------------------------------------------------------------

func TestExample31ParkedDependencies(t *testing.T) {
	// The server's notes (the notes plugin), placed on the server.
	notes := exRemote("example.notes", contracts.Params{}, exServerAddress, contracts.Var("editor", contracts.VariantDeclaration{
		State:   []contracts.Property{exProp("list", exArr(exStr()))},
		Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr()))))},
	}))
	// A panel in the tab: a local object over the server's notes.
	panel := exLocal("example.panel", contracts.Params{exProp("id", exInt())}, contracts.Mem("count", exInt()))
	// A local object the panel's sibling needs, which no plugin of the tab serves.
	theme := exLocal("example.theme", contracts.Params{}, contracts.Mem("name", exStr()))
	header := exLocal("example.header", contracts.Params{exProp("id", exInt())}, contracts.Mem("title", exStr()))
	list := exStringListDoc("example.list")
	// The notes plugin's server facet.
	notesPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, list)
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{
				"list": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { exPush(draft, args.Value("text")) }, ctx)
				}),
			}), nil
		}, ServeOptions{})
	})
	// startServer is a server with the given plugins, its link to the tab accepted.
	startServer := func(plugins ...HostFacet) (Host, *link.MemoryLink, *link.MemoryLink) {
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
		must(t, server.Load(plugins, background))
		up, down := link.MemoryLinkPair()
		must(t, server.Accept(up, LinkOptions{Principal: ownerPrincipal()}, background))
		return server, down, up
	}
	// The tab: its panel declares the notes (a dependency); its header a theme nobody serves.
	var panels int
	var panelMu sync.Mutex
	tabErrors := &hostErrors{}
	tab := newHost(t, HostOptions{OnError: tabErrors.push})
	must(t, tab.Load([]HostFacet{{Name: "@example/panel", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(panel, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			panelMu.Lock()
			panels++
			panelMu.Unlock()
			remote := self.Get(notes, jsonObject())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"count": self.Derive([]any{remote.Member("list")}, func(values []any) (delta.JsonValue, error) {
					return float64(len(values[0].([]any))), nil
				}),
			}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(header, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"title": Expose(self.Get(theme, jsonObject()).Member("name"))}, nil), nil
		}, ServeOptions{})
	})}}, background))
	// 1. The first server has no notes plugin. Its answer is `failed { waiting }`: not the panel's fault.
	first, firstLink, firstUp := startServer()
	must(t, tab.Connect(firstLink, LinkOptions{Principal: serverPrincipal()}, background))
	panelRef := exHold(t, tab, panel, jsonObject("id", float64(1)))
	watch, kinds := exStateKinds(t, panelRef)
	scopeEventually(t, "panel parked waiting", func() bool { return strings.HasPrefix(exStateNow(t, panelRef), "failed, waiting") })
	// 2. A local type nobody serves here is the same answer: the request waits unbound, indexed under the type's name.
	headerRef := exHold(t, tab, header, jsonObject("id", float64(1)))
	scopeEventually(t, "header parked waiting", func() bool { return strings.HasPrefix(exStateNow(t, headerRef), "failed, waiting") })
	// 3. A getter waits through `waiting` up to its readyTimeout, then rejects with the cause (a hold just shows it).
	timeout := 200 * time.Millisecond
	if _, err := tab.Get(panel, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &timeout}); err == nil {
		t.Fatal("get gave up: resolved")
	}
	// 4. Time passes: no timer runs for the parked panel, its factory is not run again.
	panelMu.Lock()
	if panels != 1 || !strings.HasPrefix(exStateNow(t, panelRef), "failed, waiting") {
		t.Fatalf("still parked: %s, factory runs: %d", exStateNow(t, panelRef), panels)
	}
	panelMu.Unlock()
	// 5. The server restarts with the notes plugin (the link drop shows as loading); the tab reconnects: its request is
	// held again and binds, and the parked panel goes on.
	firstUp.Close()
	second, secondLink, _ := startServer(HostFacet{Name: "@example/notes", Facet: notesPlugin})
	scopeEventually(t, "the tab's parent link closed", func() bool {
		return tab.Connect(secondLink, LinkOptions{Principal: serverPrincipal()}, background) == nil
	})
	live := exGet(t, tab, panel, jsonObject("id", float64(1)))
	exEqual(t, "panel live, count", exValue(t, live, "count"), float64(0))
	panelMu.Lock()
	if panels != 1 {
		t.Fatalf("factory runs: %d", panels)
	}
	panelMu.Unlock()
	// The panel's notes are the owner's object on the server (a remote object per principal): the tab adds through its own.
	notesRef := exGet(t, tab, notes, jsonObject())
	exCall(t, notesRef, "add", jsonObject("text", "first"))
	scopeEventually(t, "panel count", func() bool { return exValue(t, live, "count") == float64(1) })
	headerRef.Release()
	panelRef.Release()
	live.Release()
	notesRef.Release()
	<-watch.Closed()
	if got := kinds.all(); len(got) == 0 || got[len(got)-1] != "ended" && !slices_Contains(got, "live") {
		t.Fatalf("panel states = %v", got)
	}
	exCloseHost(t, tab)
	exCloseHost(t, second)
	exCloseHost(t, first)
	if tabErrors.Len() > 0 {
		for _, err := range tabErrors.All() {
			if err.Kind != ErrParkedLong {
				t.Fatalf("tab background errors: %v", tabErrors.All())
			}
		}
	}
}

// --- 32-startup.ts -------------------------------------------------------------------------------------------------

func TestExample32Startup(t *testing.T) {
	// On the server, a host-lifetime object (local, empty params): it starts at `load`.
	bootstrap := exLocal("example.bootstrap", contracts.Params{}, contracts.Mem("agents", exInt()))
	// On the session's worker.
	agent := exRemote("example.agent", contracts.Params{exProp("session", exStr())}, exSessionAddress, contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("models", exInt())},
	}))
	// On the server.
	catalog := exRemote("example.catalog", contracts.Params{}, exServerAddress, contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("models", exInt())},
	}))
	errs := &hostErrors{}
	// startWorker is the worker of a session, started by the server's route; it connects first, then loads (W6).
	type started struct {
		worker Host
		link   *link.MemoryLink
	}
	var workers []started
	var workersMu sync.Mutex
	workerLoaded := make(chan struct{})
	startWorker := func(id string) started {
		up, down := link.MemoryLinkPair()
		address := Session.Address(jsonObject("id", id))
		worker := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: errs.push})
		must(t, worker.Connect(down, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
		agents := DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(agent, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				// The worker's Agent declares the server's Catalog.
				return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{
					"models": Expose(self.Get(catalog, jsonObject()).Member("models")),
				}, nil), nil
			}, ServeOptions{})
		})
		go func() {
			_ = worker.Load([]HostFacet{{Name: "@example/agents", Facet: agents}}, background)
			close(workerLoaded)
		}()
		return started{worker, up}
	}
	// No readyTimeout is needed to break anything: a long one shows nothing waits for it.
	readyTimeout := 600 * time.Second
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), ReadyTimeout: &readyTimeout, OnError: errs.push})
	must(t, server.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			id, _ := address.Params.Value("id").(string)
			made := startWorker(id)
			workersMu.Lock()
			workers = append(workers, made)
			workersMu.Unlock()
			return ResolvedLink{Link: made.link, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	core := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(catalog, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{"models": exConstant(float64(3))}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(bootstrap, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			// The server's Bootstrap declares the worker's Agent.
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"agents": Expose(self.Get(agent, jsonObject("session", "S1")).Member("models")),
			}, nil), nil
		}, ServeOptions{})
	})
	// 1. The server's load: publication (Catalog served), then Bootstrap starts and parks on the worker's Agent. A wait on
	// another host is a first outcome, so `load` resolves without waiting for the worker.
	must(t, server.Load([]HostFacet{{Name: "@example/core", Facet: core}}, background))
	if got := exStateNow(t, exHold(t, server, bootstrap, jsonObject())); got != "loading" {
		t.Fatalf("server loaded; Bootstrap is %s, want loading", got)
	}
	// 2. The worker's hold for Catalog bound on the published server; Agent went live, then Bootstrap.
	reference := exGet(t, server, bootstrap, jsonObject())
	exEqual(t, "Bootstrap sees models through the worker", exValue(t, reference, "agents"), float64(3))
	<-workerLoaded
	reference.Release()
	exCloseHost(t, server)
	workersMu.Lock()
	defer workersMu.Unlock()
	for _, made := range workers {
		exCloseHost(t, made.worker)
	}
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
