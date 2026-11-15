package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/host/admission.test.ts (Pi 7cc21c24f6b5; slice 14 §2, test lists 14 and 20). One Test function
// per `describe`, one subtest per `it`, named by the Pi case.

// --- fixtures (Pi 53-317) -------------------------------------------------------------------------------------------

var admModes = func() *documents.StateDefinition {
	d, err := documents.DefineState("modes", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("modes", contracts.Map(contracts.Str()))),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("modes", jsonObject()) },
	})
	if err != nil {
		panic(err)
	}
	return d
}()

var admText = func() *documents.StateDefinition {
	d, err := documents.DefineState("text", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("text", contracts.Str())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("text", "t") },
	})
	if err != nil {
		panic(err)
	}
	return d
}()

// admAccess: who may do what, by principal kind: the store the admissions read.
var admAccess = mustContract(contracts.Local("test.access", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("modes", contracts.Map(contracts.Str())),
		contracts.Mem("set", contracts.NewMethod(contracts.Obj(contracts.Prop("who", contracts.Str()), contracts.Prop("mode", contracts.Str())))),
	},
}))

// admAccessV2: Access with one more member: version 1's consumers bind it only through `accepts`.
var admAccessV2 = mustContract(contracts.Evolve(admAccess, func(e *contracts.LocalEditor) *contracts.LocalEditor {
	return e.AddState("extra", contracts.Str())
}))

// admShare: a per-document source of the admission, with params: it ends with its last reference.
var admShare = mustContract(contracts.Local("test.share", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("open", contracts.Str())},
}))

// admMissing: a type nobody serves.
var admMissing = mustContract(contracts.Local("test.missing", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

func admModeSchema() contracts.Schema {
	return contracts.Obj(contracts.Prop("mode", contracts.OneOf("read", "write")))
}

// admDoc: the admitted type: a reader or a writer variant by the admitted mode.
var admDoc = mustContract(contracts.Remote("test.doc", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Admission:   func() *contracts.Schema { s := admModeSchema(); return &s }(),
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("text", contracts.Str())}}),
		contracts.Var("writer", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("text", contracts.Str())},
			Methods: []contracts.MethodEntry{{Name: "write", Value: contracts.NewMethod(contracts.Str(), contracts.Str())}},
		}),
	},
}))

// admDocV2: Doc with another admission schema (the same wire fingerprint): a reload does not rehearse across it.
var admDocV2 = mustContract(contracts.Evolve(admDoc, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.Admission(contracts.Obj(contracts.Prop("mode", contracts.OneOf("read", "write")), contracts.Prop("since", contracts.Int())))
}))

// admPanel: a consumer of a document on the same host: a derive over its text.
var admPanel = mustContract(contracts.Local("test.panel", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("shout", contracts.Str())},
}))

var admWork = func() *JobDefinition {
	d, err := DefineJob("test.work", JobOptions{Version: 1, Args: contracts.Obj(), Result: contracts.Int()})
	if err != nil {
		panic(err)
	}
	return d
}()

// admLog is a mutex-guarded list of strings a factory or a native appends to (Pi's plain `string[]`).
type admLog = nexttest.Collector[string]

// admAccessCode is what a served Access does besides keeping the modes.
type admAccessCode struct {
	// Opened: a native that opens once this closes: a reload's successor stays `loading` meanwhile.
	Opened chan struct{}
	// NeedsMissing declares a type nobody serves: the attempt parks `failed { waiting }`.
	NeedsMissing bool
	// Broken: its native's open fails `broken` while the flag says so.
	Broken *admFlag
	// Selves collects the factory's `self`, for failLive.
	Selves *attemptList
}

type admFlag struct {
	mu sync.Mutex
	on bool
}

func (f *admFlag) set(on bool) { f.mu.Lock(); f.on = on; f.mu.Unlock() }
func (f *admFlag) get() bool   { f.mu.Lock(); defer f.mu.Unlock(); return f.on }

// admAccessFacet is a facet serving Access (as contract).
func admAccessFacet(code admAccessCode, contract *contracts.Contract, accepts contracts.AcceptsTable) Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(contract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			if code.Selves != nil {
				code.Selves.add(self)
			}
			modes := self.State(admModes).(*documents.Singleton)
			if code.Opened != nil || code.Broken != nil {
				self.Open(func(ctx context.Context) (any, error) {
					if code.Broken != nil && code.Broken.get() {
						return nil, data.Broken(errors.New("access is broken"))
					}
					if code.Opened != nil {
						select {
						case <-code.Opened:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return nil, nil
				}, func(any, NativeCloseReason, context.Context) error { return nil })
			}
			if code.NeedsMissing {
				self.Get(admMissing, jsonObject())
			}
			state := map[string]chord.Watchable[delta.JsonValue]{
				"modes": self.Derive([]any{modes}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("modes"), nil
				}),
			}
			if contract == admAccessV2 {
				state["extra"] = Expose(state_constant("x"))
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: state,
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						request := args.(*delta.JsonObject)
						_, err := modes.Change(ctx, func(draft *delta.Object) (any, error) {
							return nil, draft.Object("modes").Set(request.Value("who").(string), request.Value("mode"))
						})
						return nil, err
					},
				},
			}}, nil
		}, ServeOptions{Accepts: accepts})
	})
}

// state_constant is `constant(value)` (state.Constant).
func state_constant(value delta.JsonValue) *state.Constant { return state.NewConstant(value) }

// admDecide is what a mode means to the admission: `throw` makes its derive throw.
func admDecide(mode string, ok bool) (delta.JsonValue, error) {
	if ok && mode == "throw" {
		return nil, errors.New("the rule threw")
	}
	if ok && (mode == "read" || mode == "write") {
		return Allow(jsonObject("mode", mode)), nil
	}
	return Deny("no access"), nil
}

// admitBy is the admission of Doc: the caller's mode in Access; with share, its per-document source too.
func admitBy(share bool) Admit {
	return func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
		modes := self.Get(admAccess, jsonObject()).Member("modes")
		kind := principalKind(principal)
		decide := func(all *delta.JsonObject) (delta.JsonValue, error) {
			mode, ok := all.Value(kind).(string)
			return admDecide(mode, ok)
		}
		if !share {
			return self.Derive([]any{modes}, func(values []any) (delta.JsonValue, error) {
				return decide(values[0].(*delta.JsonObject))
			}), nil
		}
		open := self.Get(admShare, jsonObject("id", params.Value("id"))).Member("open")
		return self.Derive([]any{modes, open}, func(values []any) (delta.JsonValue, error) {
			return decide(values[0].(*delta.JsonObject))
		}), nil
	}
}

// admDocCode is what a served Doc does.
type admDocCode struct {
	// Built: its factory's runs, `id:mode`.
	Built *admLog
	// Gate: what `write` waits for (nil: nothing).
	Gate chan struct{}
	// Opened: a native that opens once this closes; its opens and closes are logged in Natives.
	Opened  chan struct{}
	Natives *admLog
	// Closing: what the native's close waits for.
	Closing chan struct{}
	Linger  *time.Duration
	Admit   Admit
	// Declare: a declaration only the factory makes (a job: refused; a throw).
	Declare func(self Self, mode string)
	// Write: what `write` does before it answers.
	Write func(text string, self Self) error
}

// admDocFacet is a facet serving Doc (as contract) and Share, Panel.
func admDocFacet(code admDocCode, contract *contracts.Contract, shares *admLog) Facet {
	admit := code.Admit
	if admit == nil {
		admit = admitBy(false)
	}
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		err := host.Serve(contract, func(params *delta.JsonObject, _ data.Principal, admitted delta.JsonValue, self Self) (contracts.Implementation, error) {
			id, mode := params.Value("id").(string), admitted.(*delta.JsonObject).Value("mode").(string)
			code.Built.Append(id + ":" + mode)
			if code.Declare != nil {
				code.Declare(self, mode)
			}
			text := self.State(admText).(*documents.Singleton)
			if code.Opened != nil {
				self.Open(func(ctx context.Context) (any, error) {
					select {
					case <-code.Opened:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					code.Natives.Append("open")
					return nil, nil
				}, func(any, NativeCloseReason, context.Context) error {
					if code.Closing != nil {
						<-code.Closing
					}
					code.Natives.Append("close")
					return nil
				})
			}
			shown := self.Derive([]any{text}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("text"), nil
			})
			if mode == "read" {
				return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"text": shown},
				}}, nil
			}
			write := func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				next := args.(string)
				// As a handler waits (`await gate`): it parks, and its caller's cancellation and its attempt's seal end the wait.
				if code.Gate != nil {
					if _, err := chordctx.Receive(ctx, code.Gate); err != nil {
						return nil, err
					}
				}
				if code.Write != nil {
					if err := code.Write(next, self); err != nil {
						return nil, err
					}
				}
				if _, err := text.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("text", next) }); err != nil {
					return nil, err
				}
				return mode + ":" + next, nil
			}
			return contracts.Implementation{Variant: "writer", Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"text": shown},
				Methods: map[string]contracts.MethodFunction{"write": write},
			}}, nil
		}, ServeOptions{Admit: admit, Linger: code.Linger})
		if err != nil {
			return err
		}
		if err := host.Serve(admShare, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			if shares != nil {
				shares.Append(params.Value("id").(string))
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"open": Expose(state_constant("yes"))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(admPanel, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := self.Get(contractOf(contract), jsonObject("id", params.Value("id")))
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"shout": self.Derive([]any{doc.Member("text")}, func(values []any) (delta.JsonValue, error) {
						return upper(values[0].(string)), nil
					}),
				},
			}}, nil
		}, ServeOptions{})
	})
}

func contractOf(c *contracts.Contract) *contracts.Contract { return c }
func upper(s string) string                                { return string(bytesToUpper(s)) }
func bytesToUpper(s string) []byte {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return b
}

// admServer is a server with Access and Doc in two plugins, its errors, and `server`'s mode set to the option's.
type admServer struct {
	t       *testing.T
	h       Host
	errors  *hostErrors
	plugins map[string]Facet
}

type admOptions struct {
	Mode         string
	Access       admAccessCode
	ReadyTimeout *time.Duration
	// Loop drives the host with the fake clock (Pi's `vi.useFakeTimers`); nil is the real loop.
	Loop sectionLoop
	// Meter makes the host record its counters (Pi's `metered()`).
	Meter *nexttest.Meter
}

func admServed(t *testing.T, code admDocCode, options admOptions) *admServer {
	t.Helper()
	errs := &hostErrors{}
	address := Server.Address(nil)
	h := newHost(t, HostOptions{
		Address: &address, Principal: serverPrincipal(), ReadyTimeout: options.ReadyTimeout, OnError: errs.push, loop: options.Loop,
		Telemetry: meterTelemetry(options.Meter),
	})
	s := &admServer{t: t, h: h, errors: errs, plugins: map[string]Facet{
		"access": admAccessFacet(options.Access, admAccess, nil),
		"doc":    admDocFacet(code, admDoc, nil),
	}}
	_, err := h.Load(s.facets(), background)
	must(t, err)
	if options.Mode != "" {
		s.set(options.Mode, "server")
	}
	return s
}

func (s *admServer) facets() []HostFacet {
	return []HostFacet{{Name: "@test/access", Facet: s.plugins["access"]}, {Name: "@test/doc", Facet: s.plugins["doc"]}}
}

// set is `access.set({ who, mode })` through a getter of Access.
func (s *admServer) set(mode, who string) {
	s.t.Helper()
	access := mustGet(s.t, s.h, admAccess, jsonObject())
	_, err := access.Call("set", jsonObject("who", who, "mode", mode), background)
	must(s.t, err)
}

// reload swaps plugins and reloads the host.
func (s *admServer) reload(next map[string]Facet) (ReloadReport, error) {
	for name, facet := range next {
		s.plugins[name] = facet
	}
	return s.h.Reload(s.facets(), background, ReloadOptions{})
}

func (s *admServer) delivered() { deliveredOn(s.h) }

// told is error's code, reason and message.
func told(err error) [3]string {
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) {
		return [3]string{"", "", err.Error()}
	}
	return [3]string{string(chordErr.Code), string(chordErr.Reason), chordErr.Message}
}

// admValue is `ref.<member>.value`, read in a section: its error is what the getter throws.
func admValue(ref *Reference, member string) (delta.JsonValue, error) {
	var value delta.JsonValue
	var err error
	ref.binding.loop.Run(func() {
		source, serr := ref.binding.member(member)
		if serr != nil {
			err = serr
			return
		}
		value, err = source.Value()
	})
	return value, err
}

// admVariant is `ref.variant`: "" while none is bound.
func admVariant(ref *Reference) string {
	variant, bound, _ := ref.Variant()
	if !bound {
		return ""
	}
	return variant
}

// mustValue is `ref.member.value`.
func mustValue(t *testing.T, ref *Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := admValue(ref, member)
	must(t, err)
	return value
}

// admLive waits until ref is live: Pi's `await delivered()` also lets the microtasks of a rebuild run to the end, where a Go
// rebuild's activation ends on its own goroutines after the delivery scheduler is idle.
func admLive(t *testing.T, ref *Reference) {
	t.Helper()
	until(t, "the reference is live", func() bool { return stateOfRef(ref).Kind == RefStateLive })
}

// admDenied is `toMatchObject({ kind: "failed", cause: { kind: "denied", error: { code: "forbidden", message } } })`.
func admDenied(t *testing.T, state *RefState, message string) {
	t.Helper()
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureDenied || state.Cause.Error.Code != data.CodeForbidden ||
		state.Cause.Error.Message != message || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed { denied: forbidden %q }", state, message)
	}
}

// admHoldStates holds a reference and starts its `ref.state` watch in one section, so the watch's acquisition revision is the
// state before the admission's first microtask (Pi: `host.hold(...)` then `await ref.state.watch()` in one task).
func admHoldStates(t *testing.T, host Host, typ *contracts.Contract, params *delta.JsonObject) (*Reference, *statesCollector) {
	t.Helper()
	var reference *Reference
	var watch chord.NativeWatchHandle[*RefState]
	var err error
	host.(*chordHost).enter(func() {
		if reference, err = host.Hold(background, typ, params, GetOptions{}); err != nil {
			return
		}
		watch, err = reference.State().Watch(background)
	})
	must(t, err)
	return reference, statesOf(t, watch)
}

// --- deny, then allow (Pi 319-369) ------------------------------------------------------------------------------------

func TestAdmissionDenyThenAllow(t *testing.T) {
	t.Run("a direct hold shows failed { denied } with the reason, then binds the variant the value selects", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built}, admOptions{})
		// Pi acquires the watch before the admission's first microtask: its acquisition revision is `loading`, and what it
		// delivers starts at the denial. A Go Hold drains that microtask as its section ends, so the hold and the watch are one
		// section.
		doc, states := admHoldStates(t, s.h, admDoc, jsonObject("id", "a"))
		s.delivered()
		admDenied(t, stateOfRef(doc), "no access")
		// The factory has not run: nothing is built for a caller who is denied.
		if built.Len() != 0 {
			t.Fatalf("built = %v, want none", built.Items())
		}
		s.set("read", "server")
		s.delivered()
		admLive(t, doc)
		if kind, variant, text := stateOfRef(doc).Kind, admVariant(doc), mustValue(t, doc, "text"); kind != RefStateLive || variant != "reader" || text != "t" {
			t.Fatalf("[%v %v %v], want [live reader t]", kind, variant, text)
		}
		expectStrings(t, built.Items(), "a:read")
		expectStrings(t, states.All(), "failed: no access", "loading", "live")
		expectNoErrors(t, s.errors)
		must(t, s.h.Close(background))
	})

	t.Run("a getter rejects at once with the denial, and resolves once allowed", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{})
		_, err := s.h.Get(background, admDoc, jsonObject("id", "a"), GetOptions{})
		if got := told(err); got != [3]string{"forbidden", "", "no access"} {
			t.Fatalf("getter rejected with %v", got)
		}
		s.set("write", "server")
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		if variant := admVariant(doc); variant != "writer" {
			t.Fatalf("variant = %q, want writer", variant)
		}
		must(t, s.h.Close(background))
	})

	t.Run("a dependent's self.get shows the denial, stays parked and heals when access returns", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{})
		panel := mustHold(t, s.h, admPanel, jsonObject("id", "a"))
		s.delivered()
		// Denied, not broken: the dependency's `forbidden` is no authority refusal that never heals.
		admDenied(t, stateOfRef(panel), "no access")
		s.set("read", "server")
		s.delivered()
		admLive(t, panel)
		if kind, shout := stateOfRef(panel).Kind, mustValue(t, panel, "shout"); kind != RefStateLive || shout != "T" {
			t.Fatalf("[%v %v], want [live T]", kind, shout)
		}
		expectNoErrors(t, s.errors)
		must(t, s.h.Close(background))
	})
}

// --- the watch table (Pi 371-491; "2.2") ------------------------------------------------------------------------------------

func TestAdmissionTheWatchTable(t *testing.T) {
	t.Run("an equal value changes nothing: no rebuild, the running call completes", func(t *testing.T) {
		built, gate := &admLog{}, make(chan struct{})
		s := admServed(t, admDocCode{Built: built, Gate: gate}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		watch, err := refStateWatch(doc, background)
		must(t, err)
		states := statesOf(t, watch)
		writing := admStart(t, doc, "write", "x") // doc.variant is "writer"
		// Another person's mode changes the source's value, not this caller's decision.
		s.set("read", "guest")
		s.set("write", "server")
		s.delivered()
		close(gate)
		if got, err := writing.wait(background); err != nil || got != "write:x" {
			t.Fatalf("write = %v, %v, want write:x", got, err)
		}
		expectStrings(t, built.Items(), "a:write")
		expectStrings(t, states.All())
		must(t, s.h.Close(background))
	})

	t.Run("a denial seals the attempt: its calls reject forbidden/revoked, its holders see failed { denied } at once", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built, Gate: make(chan struct{})}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		watch, err := refStateWatch(doc, background)
		must(t, err)
		states := statesOf(t, watch)
		writing := admStart(t, doc, "write", "x")
		s.set("none", "server")
		admDenied(t, stateOfRef(doc), "no access")
		if got := told(admRejection(t, writing)); got != [3]string{"forbidden", "revoked", "test.doc(a): no access"} {
			t.Fatalf("write rejected with %v", got)
		}
		s.delivered()
		// No `loading` in between: the denial is shown in the flush that made it.
		expectStrings(t, states.All(), "failed: no access")
		expectStrings(t, built.Items(), "a:write")
		must(t, s.h.Close(background))
	})

	t.Run("a decision that throws when read is a failed source: sealed, calls unavailable, holders failed { waiting }", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built, Gate: make(chan struct{})}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		writing := admStart(t, doc, "write", "x")
		s.set("throw", "server")
		if got := told(admRejection(t, writing)); got[0] != "unavailable" {
			t.Fatalf("write rejected with %v, want unavailable", got)
		}
		s.delivered()
		if message := admWaiting(t, doc).Cause.Error.Message; !strings.Contains(message, "the rule threw") {
			t.Fatalf("message = %q, want it to contain the rule threw", message)
		}
		// Not denied: the last value stays readable while it waits.
		if text := mustValue(t, doc, "text"); text != "t" {
			t.Fatalf("text = %v, want t", text)
		}
		s.set("write", "server")
		s.delivered()
		admLive(t, doc)
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:write", "a:write")
		must(t, s.h.Close(background))
	})

	t.Run("a call that crossed a seal runs on the next attempt under its value", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		s.set("read", "server")
		// Made while the new attempt admits (the reference is loading): it waits at the gate, never `not_ready`, and
		// meets the reader the new value selects, not the writer it was sent to.
		crossed := admStart(t, doc, "write", "y")
		if got := told(admRejection(t, crossed)); got != [3]string{"invalid", "member", `test.doc(a) (variant reader) has no method "write"`} {
			t.Fatalf("write rejected with %v", got)
		}
		s.set("write", "server")
		if got, err := admStart(t, doc, "write", "z").wait(background); err != nil || got != "write:z" {
			t.Fatalf("write = %v, %v, want write:z", got, err)
		}
		must(t, s.h.Close(background))
	})
}

// --- what a denial removes (Pi 493-546) ---------------------------------------------------------------------------------------

func expectStrings(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func expectNoErrors(t *testing.T, errs *hostErrors) {
	t.Helper()
	if errs.Len() != 0 {
		t.Fatalf("host errors = %v, want none", failures(errs.All()))
	}
}

func meterTelemetry(meter *nexttest.Meter) telemetry.TelemetryContext {
	if meter == nil {
		return nil
	}
	return telemetryOf(meter.Telemetry)
}

// admPresent is `host.references(Type).value`: the listed holders as JSON, read in a section.
func admPresent(h Host, typ *contracts.Contract) []any {
	var value delta.JsonValue
	h.(*chordHost).enter(func() { value, _ = h.References(typ).Value() })
	list, _ := value.([]any)
	return list
}

// admWatched is an admission whose decisions are counted: `decisions` of Pi's `instancesOf(Decision, "declare")`. The
// decision is the `self` its admit declares with, so a wrapper of admit sees each one (weakly, for the collection case).
type admDecisions struct {
	mu   sync.Mutex
	made []weak.Pointer[Decision]
}

func (d *admDecisions) len() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.made) }
func (d *admDecisions) since(from int) []weak.Pointer[Decision] {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]weak.Pointer[Decision](nil), d.made[from:]...)
}

// counting is admit with every decision it declares for recorded.
func (d *admDecisions) counting(admit Admit) Admit {
	return func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
		d.mu.Lock()
		d.made = append(d.made, weak.Make(self.(*Decision)))
		d.mu.Unlock()
		return admit(params, principal, self)
	}
}

// --- a failed admitted object (Pi 547-598) ------------------------------------------------------------------------------

func TestAdmissionAFailedAdmittedObject(t *testing.T) {
	t.Run("whose factory broke for one value is retried by another allowed value, and by nothing else", func(t *testing.T) {
		built := &admLog{}
		declare := func(_ Self, mode string) {
			if mode == "write" {
				panic(errors.New("the writer is broken"))
			}
		}
		s := admServed(t, admDocCode{Built: built, Declare: declare}, admOptions{Mode: "write"})
		doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureBroken {
			t.Fatalf("state = %+v, want failed broken", st)
		}
		// An equal decision and a failed source change nothing: the broken factory is not run again.
		s.set("read", "guest")
		s.set("throw", "server")
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureBroken {
			t.Fatalf("state = %+v, want failed broken", st)
		}
		expectStrings(t, built.Items(), "a:write")
		s.set("read", "server")
		s.delivered()
		admLive(t, doc)
		if kind, variant := stateOfRef(doc).Kind, admVariant(doc); kind != RefStateLive || variant != "reader" {
			t.Fatalf("[%v %v], want [live reader]", kind, variant)
		}
		expectStrings(t, built.Items(), "a:write", "a:read")
		doc.Release()
		must(t, s.h.Close(background))
	})
}

// --- an admission's requests (Pi 600-696) -------------------------------------------------------------------------------

func TestAdmissionAnAdmissionsRequests(t *testing.T) {
	t.Run("are collected with their decisions once the object ends", func(t *testing.T) {
		decisions := &admDecisions{}
		s := admServed(t, admDocCode{Built: &admLog{}, Admit: decisions.counting(admitBy(false))}, admOptions{Mode: "read"})
		before := decisions.len()
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "gc"))
		s.set("write", "server")
		s.delivered()
		s.set("none", "server")
		s.delivered()
		doc.Release()
		s.delivered()
		made := decisions.since(before)
		if len(made) != 3 {
			t.Fatalf("decisions made = %d, want 3", len(made))
		}
		expectCollected(t, made...)
		must(t, s.h.Close(background))
	})

	t.Run("cost one decision read per change of a source, and no factory run for an equal one", func(t *testing.T) {
		meter := nexttest.Metered(t)
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built}, admOptions{Mode: "read", Meter: meter})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		before := meter.Counts()["chord.derive.computes"]
		s.set("read", "guest")
		// The store's `modes` derive and the admission's derive over it: one compute each, nothing else.
		if n := meter.Counts()["chord.derive.computes"] - before; n != 2 {
			t.Fatalf("derive computes = %v, want 2", n)
		}
		expectStrings(t, built.Items(), "a:read")
		doc.Release()
		must(t, s.h.Close(background))
	})
}

// --- presence, linger, parked_long and retry (Pi 698-797) --------------------------------------------------------------

func TestAdmissionPresenceLingerParkedLongAndRetry(t *testing.T) {
	t.Run("lists a holder from its first allow until a denial", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{})
		doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
		s.delivered()
		if present := admPresent(s.h, admDoc); len(present) != 0 {
			t.Fatalf("present = %v, want []", present)
		}
		s.set("read", "server")
		s.delivered()
		if present := admPresent(s.h, admDoc); len(present) != 1 {
			t.Fatalf("present = %v, want one holder with params {id: a}", present)
		}
		// Another allowed value rebuilds the object: its holder stays listed.
		s.set("write", "server")
		if n := len(admPresent(s.h, admDoc)); n != 1 {
			t.Fatalf("present = %d, want 1", n)
		}
		s.delivered()
		s.set("none", "server")
		if n := len(admPresent(s.h, admDoc)); n != 0 {
			t.Fatalf("present = %d, want 0", n)
		}
		// A list made while denied shows none either.
		if n := len(admPresent(s.h, admDoc)); n != 0 {
			t.Fatalf("a new list shows %d, want 0", n)
		}
		doc.Release()
		must(t, s.h.Close(background))
	})

	t.Run("unlists a holder when the attempt that parks denied is not one a seal started with the denial", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		// The source fails: the object waits on it, still listed (no denial yet).
		s.set("throw", "server")
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureWaiting {
			t.Fatalf("state = %+v, want failed waiting", st)
		}
		if n := len(admPresent(s.h, admDoc)); n != 1 {
			t.Fatalf("present = %d, want 1", n)
		}
		// The parked attempt reads the denial itself.
		s.set("none", "server")
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("state = %+v, want failed denied", st)
		}
		if n := len(admPresent(s.h, admDoc)); n != 0 {
			t.Fatalf("present = %d, want 0", n)
		}
		doc.Release()
		must(t, s.h.Close(background))
	})

	t.Run("ends a lingering object without holders when its decision changes", func(t *testing.T) {
		built := &admLog{}
		linger := 60 * time.Second
		s := admServed(t, admDocCode{Built: built, Linger: &linger}, admOptions{Mode: "read"})
		mustGet(t, s.h, admDoc, jsonObject("id", "a")).Release()
		s.delivered()
		if n := admLingering(s.h); n != 1 {
			t.Fatalf("lingering = %v, want 1", n)
		}
		s.set("write", "server")
		s.delivered()
		// Ended, not rebuilt for nobody.
		if n := admLingering(s.h); n != 0 {
			t.Fatalf("lingering = %v, want 0", n)
		}
		expectStrings(t, built.Items(), "a:read")
		must(t, s.h.Close(background))
	})

	t.Run("reports no parked_long for a denial, of the object or of a dependent parked on it", func(t *testing.T) {
		loop := newScopeLoop()
		ready := 50 * time.Millisecond
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{ReadyTimeout: &ready, Loop: loop})
		doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
		panel := mustHold(t, s.h, admPanel, jsonObject("id", "a"))
		advanceHost(loop, s.h, 10)
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("doc = %+v, want failed denied", st)
		}
		if st := stateOfRef(panel); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("panel = %+v, want failed denied", st)
		}
		advanceHost(loop, s.h, 500)
		if reports := failures(s.errors.All()); len(reports) != 0 {
			t.Fatalf("reports = %v, want none", reports)
		}
		doc.Release()
		panel.Release()
		must(t, s.h.Close(background))
	})

}

// admLingering is `host.activity.value.lingering`.
func admLingering(h Host) float64 {
	var value delta.JsonValue
	h.(*chordHost).enter(func() { value, _ = h.Activity().Value() })
	n, _ := value.(*delta.JsonObject).Value("lingering").(float64)
	return n
}

// --- declaration errors (Pi 799-864) ------------------------------------------------------------------------------------

func TestAdmissionDeclarationErrors(t *testing.T) {
	// refused is the errors of a load of one facet.
	refused := func(t *testing.T, setup func(host FacetHost) error) []string {
		t.Helper()
		address := Server.Address(nil)
		h := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: func(HostError) {}})
		report, err := h.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error { return setup(host) })}}, background)
		must(t, err)
		must(t, h.Close(background))
		messages := make([]string, 0, len(report.Failed))
		for _, failed := range report.Failed {
			messages = append(messages, failed.Error.Error())
		}
		return messages
	}

	t.Run("refuse an admission without admit, admit without an admission, and either on a local type", func(t *testing.T) {
		plain := mustContract(contracts.Remote("test.plain", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{},
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
			Variants:    []contracts.VariantEntry{contracts.Var("only", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
		}))
		factory := func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "only", Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(state_constant(1.0))}}}, nil
		}
		admit := func(*delta.JsonObject, data.Principal, AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			return Expose(state_constant(Allow(jsonObject("mode", "read")))), nil
		}
		expectStrings(t, refused(t, func(host FacetHost) error { return host.Serve(admDoc, factory, ServeOptions{}) }),
			`plugin "@test/app": test.doc: its contract declares an admission: serve it with admit`)
		expectStrings(t, refused(t, func(host FacetHost) error { return host.Serve(plain, factory, ServeOptions{Admit: admit}) }),
			`plugin "@test/app": test.plain: admit needs the contract to declare its admission`)
		expectStrings(t, refused(t, func(host FacetHost) error {
			return host.Serve(admShare, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"open": Expose(state_constant("yes"))}}}, nil
			}, ServeOptions{Admit: admit})
		}), `plugin "@test/app": test.share: only a remote type takes an admission (admit)`)
	})

	t.Run("break the attempt: a job in an admitted factory, a declaration outside admit's four, a value that is no decision", func(t *testing.T) {
		job := admServed(t, admDocCode{Built: &admLog{}, Declare: func(self Self, _ string) {
			self.Job(admWork, JobImplementation{Run: func(any, JobCtx) (any, error) { return 1.0, nil }})
		}}, admOptions{Mode: "read"})
		_, err := job.h.Get(background, admDoc, jsonObject("id", "a"), GetOptions{})
		if message := told(err)[2]; !strings.Contains(message, "test.doc(a): self.job is not declared in an admitted object: work for a person lives in a local store") {
			t.Fatalf("message = %q", message)
		}
		must(t, job.h.Close(background))

		// NARROWED: Pi's second case calls `self.writable(1)` on admit's self (a cast past the type) and expects "writable is not a
		// function". In Go AdmitSelf has the four methods only; the type states it (a *Decision has no Writable: this
		// asserts it, which is what the cast reaches).
		if _, has := any((*Decision)(nil)).(interface{ Writable(any) state.Writable[any] }); has {
			t.Fatal("an admission's self must declare nothing but get, registry, derive and liveQuery")
		}

		noDecision := func(*delta.JsonObject, data.Principal, AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			return Expose(state_constant(jsonObject("kind", "maybe"))), nil
		}
		value := admServed(t, admDocCode{Built: &admLog{}, Admit: noDecision}, admOptions{})
		_, err = value.h.Get(background, admDoc, jsonObject("id", "a"), GetOptions{})
		if message := told(err)[2]; message != "test.doc(a): its admission's value is neither allow(value) nor deny(reason)" {
			t.Fatalf("message = %q", message)
		}
		value.set("read", "server")
		// NARROWED (type fact): Pi's `plain` returns allow(...) itself, not a watchable. In Go the admit returns a Chord watchable
		// by type; the refusal of a result that is none (`resolveWatchable` nil) is reached by an exposed value that is no source.
		plain := func(*delta.JsonObject, data.Principal, AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			return Expose("not a source"), nil
		}
		_, err = value.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: &admLog{}, Admit: plain}, admDoc, nil)})
		must(t, err)
		_, err = value.h.Get(background, admDoc, jsonObject("id", "b"), GetOptions{})
		if message := told(err)[2]; !strings.Contains(message, "test.doc(b): admit returns a JSON watchable of allow(value) or deny(reason)") {
			t.Fatalf("message = %q", message)
		}
		must(t, value.h.Close(background))
	})
}

// --- over a link (Pi 866-1021) -------------------------------------------------------------------------------------------

// admStart is `ref.<method>(args, context)` started in one section: the call, not yet awaited (Pi's promise).
func admStart(t testing.TB, ref *Reference, method string, args delta.JsonValue) *parkedCallResult {
	t.Helper()
	b := ref.binding
	var result *parkedCallResult
	var err error
	b.loop.Run(func() { result, err = b.call(method, args, background) })
	if err != nil {
		failed := newParkedCallResult()
		failed.settle(nil, err)
		return failed
	}
	return result
}

// admRejection is `await rejection(call)`: the call's error, which must exist.
func admRejection(t testing.TB, result *parkedCallResult) error {
	t.Helper()
	_, err := result.wait(background)
	if err == nil {
		t.Fatal("the call resolved, want a rejection")
	}
	return err
}

func TestAdmissionOverALink(t *testing.T) {
	t.Run("a call still waiting for a call-window slot at a denial is rejected forbidden/revoked, as one in flight, and never sent", func(t *testing.T) {
		window := 1
		address := Server.Address(nil)
		server := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), CallWindow: &window, OnError: func(HostError) {}})
		ran := &admLog{}
		facets := []HostFacet{
			{Name: "@test/access", Facet: admAccessFacet(admAccessCode{}, admAccess, nil)},
			{Name: "@test/doc", Facet: admDocFacet(admDocCode{Built: &admLog{}, Gate: make(chan struct{}), Write: func(text string, _ Self) error { ran.Append(text); return nil }}, admDoc, nil)},
		}
		mustLoad(t)(server.Load(facets, background))
		client := newHost(t, HostOptions{OnError: func(HostError) {}})
		mustLoad(t)(client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		access := mustGet(t, server, admAccess, jsonObject())
		_, err := access.Call("set", jsonObject("who", "owner", "mode", "write"), background)
		must(t, err)
		doc := mustGet(t, client, admDoc, jsonObject("id", "a"))
		// The server announced a window of one call: the others wait at the consumer. The running call's answer frees
		// its slot, so the second is sent and crosses the `failed` on the wire; the third still waits when it arrives.
		running := admStart(t, doc, "write", "running")
		crossing := admStart(t, doc, "write", "crossing")
		waiting := admStart(t, doc, "write", "waiting")
		deliveredOn(server, client)
		_, err = access.Call("set", jsonObject("who", "owner", "mode", "none"), background)
		must(t, err)
		revokedCall := [3]string{"forbidden", "revoked", "test.doc(a): no access"}
		for name, call := range map[string]*parkedCallResult{"running": running, "crossing": crossing, "waiting": waiting} {
			if got := told(admRejection(t, call)); got != revokedCall {
				t.Fatalf("%s = %v, want %v", name, got, revokedCall)
			}
		}
		deliveredOn(server, client)
		if n := ran.Len(); n != 0 {
			t.Fatalf("ran = %v, want none", ran.Items())
		}
		// A call first made after the denial meets the denied reference: its plain `forbidden`.
		if got := told(admRejection(t, admStart(t, doc, "write", "late"))); got != [3]string{"forbidden", "", "no access"} {
			t.Fatalf("late = %v", got)
		}
		must(t, client.Close(background))
		must(t, server.Close(background))
	})

}

// admActivity is `host.activity.value[field]`.
func admActivity(h Host, field string) float64 {
	var value delta.JsonValue
	h.(*chordHost).enter(func() { value, _ = h.Activity().Value() })
	n, _ := value.(*delta.JsonObject).Value(field).(float64)
	return n
}

// admChanges is `changesOf(await ref.<member>.watch(context))`: the calls its watch delivers.
func admChanges(t testing.TB, ref *Reference, member string) *nexttest.Collector[chord.WatchCall[delta.JsonValue]] {
	t.Helper()
	var watch chord.WatchHandle[delta.JsonValue]
	var err error
	ref.binding.loop.Run(func() { watch, err = ref.Member(member).Watch(background) })
	must(t, err)
	return nexttest.ChangesOf(watch)
}

// failedFrame is Pi's `{ failed: message }` element of valuesOf: a failed call of a watch.
type failedFrame string

// expectValues is `expect(valuesOf(frames)).toEqual([...])`: a delivered value, or failedFrame for a failed call.
func expectValues(t testing.TB, calls []chord.WatchCall[delta.JsonValue], want ...any) {
	t.Helper()
	got := make([]any, len(calls))
	for i, call := range calls {
		if call.Kind == chord.WatchCallFailed {
			got[i] = failedFrame(call.Error.Error())
			continue
		}
		got[i] = call.Value
	}
	if len(got) != len(want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("values = %v, want %v", got, want)
		}
	}
}

// --- a reload of an admission's source (Pi 1023-1111; "test 20") -------------------------------------------------------

func admWaiting(t *testing.T, doc *Reference) *RefState {
	t.Helper()
	st := stateOfRef(doc)
	if st.Kind != RefStateFailed || st.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", st)
	}
	return st
}

func TestAdmissionAReloadOfAnAdmissionsSource(t *testing.T) {
	t.Run("whose successor parks seals its readers, which rebuild once it is back", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		_, err := s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{NeedsMissing: true}, admAccess, nil)})
		must(t, err)
		s.delivered()
		if message := admWaiting(t, doc).Cause.Error.Message; message != "waiting for test.access: test.missing is not served here" {
			t.Fatalf("message = %q", message)
		}
		_, err = s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{}, admAccess, nil)})
		must(t, err)
		s.delivered()
		admLive(t, doc)
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		must(t, s.h.Close(background))
	})

}

// --- a reload of the admitted type (Pi 1113-1288; "2.5, test 20") ---------------------------------------------------------

// admReports is Pi's `reports(errors)`: `activation_failed: <message>` for an activation failure, the kind for the rest.
func admReports(errs *hostErrors) []string {
	out := []string{}
	for _, e := range errs.All() {
		if e.Kind == ErrActivationFailed {
			out = append(out, string(e.Kind)+": "+e.Failure.Error.Message)
			continue
		}
		out = append(out, string(e.Kind))
	}
	return out
}

func TestAdmissionAReloadOfTheAdmittedType(t *testing.T) {
	t.Run("records the new admit without effects and rehearses the new factory with an equal admission fingerprint", func(t *testing.T) {
		built, shares := &admLog{}, &admLog{}
		s := admServed(t, admDocCode{Built: built}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		rehearsed := &admLog{}
		failing := admDocFacet(admDocCode{Built: rehearsed, Admit: admitBy(true), Declare: func(Self, string) { panic(errors.New("the new factory throws")) }}, admDoc, shares)
		report, err := s.reload(map[string]Facet{"doc": failing})
		must(t, err)
		// Rehearsed with the object's current value: its throw keeps the plugin on its old code.
		expectStrings(t, rehearsed.Items(), "a:read")
		if len(report.Failed) != 1 || report.Failed[0].Plugin != "@test/doc" || report.Failed[0].Error.Error() != `plugin "@test/doc", test.doc(a): the new factory throws` {
			t.Fatalf("report.failed = %+v", report.Failed)
		}
		// The recorded `admit` declared a share: nothing was made for it.
		expectStrings(t, shares.Items())
		admLive(t, doc)
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read")
		must(t, s.h.Close(background))
	})

}
