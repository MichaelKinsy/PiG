package host

import (
	"context"
	"errors"
	"maps"
	"slices"
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
	"github.com/MichaelKinsy/PiG/chord/next/internal/admitseam"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
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
						if _, err := chordctx.Receive(ctx, code.Opened); err != nil {
							return nil, err
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
	// Drain: what a `write` rejected at its attempt's seal waits for before it returns: its attempt's drain ends with it (nil: it
	// returns at once, as Pi's does).
	Drain chan struct{}
	// Opened: a native that opens once this closes; its opens and closes are logged in Natives.
	Opened  chan struct{}
	Natives *admLog
	// Closing: what the native's close waits for.
	Closing chan struct{}
	// OpenIgnoresAbandon: the native's open waits for Opened alone, as Pi's (it takes no context): an attempt abandoned while it
	// opens still gets its open to the end, then closes the native. Without it the open also ends with its context.
	OpenIgnoresAbandon bool
	Linger             *time.Duration
	Admit              Admit
	// Declare: a declaration only the factory makes (a job: refused; a throw).
	Declare func(self Self, mode string)
	// Write: what `write` does before it answers.
	Write func(text string, self Self) error
	// NoShare: the facet does not serve Share (a plugin of its own does: admShareFacet).
	NoShare bool
	// WriteIn: what `write` does before it answers, given the handler's ctx: a handler that waits for another goroutine's work
	// (Pi `await h.root(...)`) suspends first (chordctx.Suspend), as every wait of a Go handler does.
	WriteIn func(text string, ctx context.Context) error
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
					if code.OpenIgnoresAbandon {
						// Pi's open takes no context: `await code.opened`, an abandoned attempt's open still ends.
						_, _ = chordctx.Receive(context.WithoutCancel(ctx), code.Opened)
					} else if _, err := chordctx.Receive(ctx, code.Opened); err != nil {
						return nil, err
					}
					code.Natives.Append("open")
					return nil, nil
				}, func(_ any, _ NativeCloseReason, ctx context.Context) error {
					if code.Closing != nil {
						// `await code.closing`: parked through the host's primitive (the join treats the close's task as idle).
						_, _ = chordctx.Receive(context.WithoutCancel(ctx), code.Closing)
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
						// Pi's rejection ends the handler, and so its attempt's drain, within microtasks of the seal: nothing a
						// test's goroutine can stand between. The handler that holds the drain parks here until the test lets it end,
						// through the host's own primitive on a context nothing cancels, so the moment between a flush that sealed
						// the attempt and the end of its drain is the test's to look at.
						if code.Drain != nil {
							_, _ = chordctx.Receive(context.WithoutCancel(ctx), code.Drain)
						}
						return nil, err
					}
				}
				if code.Write != nil {
					if err := code.Write(next, self); err != nil {
						return nil, err
					}
				}
				if code.WriteIn != nil {
					if err := code.WriteIn(next, ctx); err != nil {
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
		if !code.NoShare {
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
	// Shares: the Share served by a plugin of its own, logging each of its factory runs (one id per run).
	Shares *admLog
}

func admServed(t *testing.T, code admDocCode, options admOptions) *admServer {
	t.Helper()
	errs := &hostErrors{}
	address := Server.Address(nil)
	h := newHost(t, HostOptions{
		Address: &address, Principal: serverPrincipal(), ReadyTimeout: options.ReadyTimeout, OnError: errs.push, loop: options.Loop,
		Telemetry: meterTelemetry(options.Meter),
	})
	s := &admServer{t: t, h: h, errors: errs, plugins: map[string]Facet{"access": admAccessFacet(options.Access, admAccess, nil)}}
	if options.Shares != nil {
		// Share in a plugin of its own, which a reload of Doc leaves alone: its factory runs are the objects started (Pi counts the
		// objects that started an attempt, which a restart of an object does not).
		code.NoShare = true
		s.plugins["share"] = admShareFacet(options.Shares)
	}
	s.plugins["doc"] = admDocFacet(code, admDoc, nil)
	_, err := h.Load(s.facets(), background)
	must(t, err)
	if options.Mode != "" {
		s.set(options.Mode, "server")
	}
	return s
}

// admShareFacet is the plugin that serves Share, logging each run of its factory.
func admShareFacet(shares *admLog) Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(admShare, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			shares.Append(params.Value("id").(string))
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"open": Expose(state_constant("yes"))},
			}}, nil
		}, ServeOptions{})
	})
}

func (s *admServer) facets() []HostFacet {
	facets := []HostFacet{{Name: "@test/access", Facet: s.plugins["access"]}, {Name: "@test/doc", Facet: s.plugins["doc"]}}
	if share, ok := s.plugins["share"]; ok {
		facets = append(facets, HostFacet{Name: "@test/share", Facet: share})
	}
	return facets
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
	maps.Copy(s.plugins, next)
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
		s.delivered()
		if got := told(admRejection(t, crossed)); got != [3]string{"invalid", "member", `test.doc(a) (variant reader) has no method "write"`} {
			t.Fatalf("write rejected with %v", got)
		}
		s.set("write", "server")
		if got, err := admStart(t, doc, "write", "z").wait(background); err != nil || got != "write:z" {
			t.Fatalf("write = %v, %v, want write:z", got, err)
		}
		must(t, s.h.Close(background))
	})

	t.Run("another allowed value seals the attempt: its calls reject unavailable, its holders see loading, then the new variant", func(t *testing.T) {
		built, drain := &admLog{}, make(chan struct{})
		// Pi's assertion after `await set` runs before the successor's activation, by microtask depth. A Go test goroutine cannot
		// stand between the two, so the sealed attempt's drain is held (Drain): its successor waits for it, loading.
		s := admServed(t, admDocCode{Built: built, Gate: make(chan struct{}), Drain: drain}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		watch, err := refStateWatch(doc, background)
		must(t, err)
		states := statesOf(t, watch)
		frames := admChanges(t, doc, "text")
		writing := admStart(t, doc, "write", "x")
		s.set("read", "server")
		// In the flush of the change: sealed, and loading with the last value readable.
		if kind, text := stateOfRef(doc).Kind, mustValue(t, doc, "text"); kind != RefStateLoading || text != "t" {
			t.Fatalf("[%v %v], want [loading t]", kind, text)
		}
		if got := told(admRejection(t, writing)); got != [3]string{"unavailable", "", "test.doc(a): its admission changed"} {
			t.Fatalf("write rejected with %v", got)
		}
		// The drain is held: still loading. Letting it end lets the successor admit and activate.
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLoading {
			t.Fatalf("state = %v, want loading while the drain is held", kind)
		}
		close(drain)
		s.delivered()
		s.delivered()
		if kind, variant := stateOfRef(doc).Kind, admVariant(doc); kind != RefStateLive || variant != "reader" {
			t.Fatalf("[%v %v], want [live reader]", kind, variant)
		}
		expectStrings(t, built.Items(), "a:write", "a:read")
		expectStrings(t, states.All(), "loading", "live")
		// The same text on the new instance: no frame.
		expectValues(t, frames.Items())
		expectNoErrors(t, s.errors)
		must(t, s.h.Close(background))
	})

	t.Run("a source that fails live seals its readers, which park on it and rebuild when it is back", func(t *testing.T) {
		built, selves := &admLog{}, &attemptList{}
		s := admServed(t, admDocCode{Built: built}, admOptions{Mode: "read", Access: admAccessCode{Selves: selves}})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		// Pi's `failLive` and the assertion after it are one synchronous block of its event loop: no microtask runs between them.
		// Here they are one section of the host's loop (as failLiveThen is), where the successor has not admitted yet.
		var shown *RefState
		failLiveThen(s.h, selves.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "the store broke"}, nil), func() {
			shown = stateOfRef(doc)
		})
		if shown.Kind != RefStateLoading {
			t.Fatalf("state = %v, want loading", shown.Kind)
		}
		s.delivered()
		if message := admWaiting(t, doc).Cause.Error.Message; message != "waiting for test.access: the store broke" {
			t.Fatalf("message = %q", message)
		}
		// The failed store is retried by its own getter; its modes are its object's documents.
		access := mustHold(t, s.h, admAccess, jsonObject())
		access.Retry()
		s.delivered()
		s.set("read", "server")
		s.delivered()
		expectStrings(t, built.Items(), "a:read", "a:read")
		access.Release()
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
		if kind, variant := stateOfRef(doc).Kind, admVariant(doc); kind != RefStateLive || variant != "reader" {
			t.Fatalf("[%v %v], want [live reader]", kind, variant)
		}
		expectStrings(t, built.Items(), "a:write", "a:read")
		doc.Release()
		must(t, s.h.Close(background))
	})

	t.Run("that failed live follows a denial: failed { denied }, no value, out of presence, its factory not run again", func(t *testing.T) {
		built, selves, decisions := &admLog{}, &attemptList{}, &admDecisions{}
		s := admServed(t, admDocCode{Built: built, Declare: func(self Self, _ string) { selves.add(self) }, Admit: decisions.counting(admitBy(false))}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		failLive(selves.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "broke live"}, nil))
		s.delivered()
		// Failed: its last value stays readable, its holder listed.
		if kind, text, present := stateOfRef(doc).Kind, mustValue(t, doc, "text"), len(admPresent(s.h, admDoc)); kind != RefStateFailed || text != "t" || present != 1 {
			t.Fatalf("[%v %v %v], want [failed t 1]", kind, text, present)
		}
		made := decisions.len()
		s.set("none", "server")
		// In the denial's flush, with no new attempt.
		admDenied(t, stateOfRef(doc), "no access")
		if _, err := admValue(doc, "text"); err == nil || told(err)[2] != "no access" {
			t.Fatalf("text.value threw %v, want no access", err)
		}
		if variant, present := admVariant(doc), admPresent(s.h, admDoc); variant != "" || len(present) != 0 {
			t.Fatalf("[%q %v], want [none []]", variant, present)
		}
		s.delivered()
		expectStrings(t, built.Items(), "a:read")
		if n := decisions.len() - made; n != 0 {
			t.Fatalf("%d decisions made after the denial, want 0", n)
		}
		// Allowed again: another decision than the denial, so the object is retried.
		s.set("read", "server")
		s.delivered()
		if kind, text := stateOfRef(doc).Kind, mustValue(t, doc, "text"); kind != RefStateLive || text != "t" {
			t.Fatalf("[%v %v], want [live t]", kind, text)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		// Pi: `errors.map(({ kind }) => kind)`: every report's kind (failures() lists activation failures only).
		if kinds := reportKinds(s.errors.All()); len(kinds) != 1 || kinds[0] != ErrObjectFailed {
			t.Fatalf("errors = %v, want [object_failed]", kinds)
		}
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

	t.Run("are kept across a rebuild until the successor's bound: a source held only by them never ends", func(t *testing.T) {
		shares := &admLog{}
		s := admServed(t, admDocCode{Built: &admLog{}, Admit: admitBy(true)}, admOptions{Mode: "read", Shares: shares})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		s.set("write", "server")
		s.delivered()
		s.set("none", "server")
		s.delivered()
		s.set("read", "server")
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		// One `Share(a)` through three rebuilds and a denial (it has no linger: a gap would have ended it).
		expectStrings(t, shares.Items(), "a")
		doc.Release()
		s.delivered()
		// Released with the object: the next holder's admission makes a new one.
		mustGet(t, s.h, admDoc, jsonObject("id", "a")).Release()
		expectStrings(t, shares.Items(), "a", "a")
		expectNoErrors(t, s.errors)
		must(t, s.h.Close(background))
	})

	t.Run("stay with a sealed attempt while no later admission bound: a retry finds its source still held", func(t *testing.T) {
		shares := &admLog{}
		s := admServed(t, admDocCode{Built: &admLog{}, Admit: admitBy(true)}, admOptions{Mode: "read", Shares: shares})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "kept"))
		runs := 0
		// Staging records the first run; the successor's own run throws before it declares anything.
		flaky := func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			runs++
			if runs == 2 {
				return nil, errors.New("admit threw")
			}
			return admitBy(true)(params, principal, self)
		}
		report, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: &admLog{}, Admit: flaky, NoShare: true}, admDoc, nil)})
		must(t, err)
		if len(report.Failed) != 0 {
			t.Fatalf("reload failed %v, want none", report.Failed)
		}
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureBroken {
			t.Fatalf("state = %+v, want failed broken", st)
		}
		doc.Retry()
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		// The share was held by the sealed attempt's admission throughout: it never ended, so no second object (Pi counts the
		// objects started with the label "test.share(kept)": one).
		expectStrings(t, shares.Items(), "kept")
		must(t, s.h.Close(background))
	})

	// Own test: Pi's two cases above never see a source end; this is the other half of the rule (a sealed attempt's admission is
	// let go once a later one bound its requests, so a source only the sealed one held ends then).
	t.Run("let go the source only a sealed attempt's admission held once a later admission bound without it", func(t *testing.T) {
		shares, wants := &admLog{}, &admFlag{}
		wants.set(true)
		admit := func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			return admitBy(wants.get())(params, principal, self)
		}
		s := admServed(t, admDocCode{Built: &admLog{}, Admit: admit}, admOptions{Mode: "read", Shares: shares})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		expectStrings(t, shares.Items(), "a")
		wants.set(false)
		s.set("write", "server")
		s.delivered()
		// The successor's admission asks for no share: the first's is let go, and the share ends with it.
		wants.set(true)
		s.set("read", "server")
		s.delivered()
		expectStrings(t, shares.Items(), "a", "a")
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

	t.Run("ref.retry() on a denied object starts nothing; on one parked on a failed source it passes on", func(t *testing.T) {
		built, decisions, broken := &admLog{}, &admDecisions{}, &admFlag{}
		s := admServed(t, admDocCode{Built: built, Admit: decisions.counting(admitBy(false))}, admOptions{})
		doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
		s.delivered()
		made := decisions.len()
		doc.Retry()
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("state = %+v, want failed denied", st)
		}
		if n := decisions.len(); n != made {
			t.Fatalf("decisions = %d, want %d (a retry of a denied object starts nothing)", n, made)
		}
		// The source fails (its new code's native does not open): the allowed object waits on it instead.
		s.set("read", "server")
		s.delivered()
		broken.set(true)
		_, err := s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{Broken: broken}, admAccess, nil)})
		must(t, err)
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureWaiting {
			t.Fatalf("state = %+v, want failed waiting", st)
		}
		broken.set(false)
		doc.Retry()
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		doc.Release()
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

// admRejection is `await rejection(call)`: the call's error, which must exist. The caller has settled the host (`delivered`)
// before: a call still unsettled then was never rejected, which the test reports (Errorf: a test that holds a commit goes on to
// release it) where a wait would hang until the binary's timeout; the error returned then is that report.
func admRejection(t testing.TB, result *parkedCallResult) error {
	t.Helper()
	result.mu.Lock()
	settled := result.settled
	result.mu.Unlock()
	if !settled {
		t.Errorf("the call is not settled, want a rejection")
		return errors.New("the call is not settled")
	}
	_, err := result.wait(background)
	if err == nil {
		t.Fatal("the call resolved, want a rejection")
	}
	return err
}

// admOverLink is Pi's `overLink()`: a server of Access and Doc, a client linked to it (the link's caller is the `owner`), and
// `set` makes `access.set({ who: "owner", mode })` on the server.
type admOverLink struct {
	*linkedHosts
	set func(mode string)
}

func admLinked(t *testing.T, code admDocCode) *admOverLink {
	t.Helper()
	hosts := linked(t, func(host FacetHost) error {
		if err := admAccessFacet(admAccessCode{}, admAccess, nil).definition.setup(host, nil); err != nil {
			return err
		}
		return admDocFacet(code, admDoc, nil).definition.setup(host, nil)
	}, linkedOptions{})
	access := mustGet(t, hosts.Server, admAccess, jsonObject())
	return &admOverLink{linkedHosts: hosts, set: func(mode string) {
		_, err := access.Call("set", jsonObject("who", "owner", "mode", mode), background)
		must(t, err)
	}}
}

// admLinkDelivered is Pi's `delivered()` over a link, in the order the work flows: the server's work to its next wait (its rebuilds),
// its frames across the link (Pi's `transported()`: the memory link's one task per hop), then the client's.
func admLinkDelivered(l *linkedHosts) {
	deliveredOn(l.Server)
	settleLinked(l)
	deliveredOn(l.Client)
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
		deliveredOn(server)
		transported(a, b)
		deliveredOn(client)
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

	t.Run("a second reference joins a denied hold and shows the denial; both go live with the allow", func(t *testing.T) {
		l := admLinked(t, admDocCode{Built: &admLog{}})
		first := mustHold(t, l.Client, admDoc, jsonObject("id", "a"))
		settleLinked(l.linkedHosts)
		deliveredOn(l.Server, l.Client)
		second := mustHold(t, l.Client, admDoc, jsonObject("id", "a"))
		if st := stateOfRef(second); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("second = %+v, want failed denied", st)
		}
		l.set("read")
		settleLinked(l.linkedHosts)
		deliveredOn(l.Server, l.Client)
		if a, b := stateOfRef(first).Kind, stateOfRef(second).Kind; a != RefStateLive || b != RefStateLive {
			t.Fatalf("[%v %v], want [live live]", a, b)
		}
		// One wire hold: one object, one presence entry.
		if n := len(admPresent(l.Server, admDoc)); n != 1 {
			t.Errorf("presence = %d, want 1", n)
		}
		must(t, l.Client.Close(background))
		must(t, l.Server.Close(background))
	})

	t.Run("host.revoke ends a denied principal's object: its admission's requests go with it", func(t *testing.T) {
		errs, decisions := &hostErrors{}, &admDecisions{}
		address := Server.Address(nil)
		server := newHost(t, HostOptions{
			Address: &address, Principal: serverPrincipal(), OnError: errs.push,
			RevokedBy: RevokedBy[delta.JsonValue](func(principal data.Principal) ([]delta.JsonValue, error) {
				if principalKind(principal) == "owner" {
					return []delta.JsonValue{"owner"}, nil
				}
				return nil, nil
			}),
		})
		facets := []HostFacet{
			{Name: "@test/access", Facet: admAccessFacet(admAccessCode{}, admAccess, nil)},
			{Name: "@test/doc", Facet: admDocFacet(admDocCode{Built: &admLog{}, Admit: decisions.counting(admitBy(false))}, admDoc, nil)},
		}
		mustLoad(t)(server.Load(facets, background))
		client := newHost(t, HostOptions{OnError: func(HostError) {}})
		mustLoad(t)(client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		before := decisions.len()
		doc := mustHold(t, client, admDoc, jsonObject("id", "a"))
		transported(a, b)
		deliveredOn(server, client)
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("doc = %+v, want failed denied", st)
		}
		if n := admActivity(server, "references"); n != 1 {
			t.Fatalf("activity.references = %v, want 1", n)
		}
		must(t, server.Revoke("owner", background))
		transported(a, b)
		deliveredOn(server, client)
		if n := admActivity(server, "references"); n != 0 {
			t.Errorf("activity.references = %v, want 0", n)
		}
		expectCollected(t, decisions.since(before)...)
		expectNoErrors(t, errs)
		must(t, client.Close(background))
		must(t, server.Close(background))
	})
	t.Run("a denied hold is failed { denied }; a denial of a bound one drops its replicas, an allow binds it again", func(t *testing.T) {
		l := admLinked(t, admDocCode{Built: &admLog{}})
		// Acquire the state watch in the same section as Hold so the memory link's denial cannot arrive before Watch
		// (Pi: `client.hold(...)` then `await doc.state.watch(context)` in one task).
		doc, states := admHoldStates(t, l.Client, admDoc, jsonObject("id", "a"))
		admLinkDelivered(l.linkedHosts)
		admDenied(t, stateOfRef(doc), "no access")
		_, err := l.Client.Get(background, admDoc, jsonObject("id", "a"), GetOptions{})
		if got := told(err); got != [3]string{"forbidden", "", "no access"} {
			t.Fatalf("getter rejected with %v", got)
		}
		l.set("write")
		admLinkDelivered(l.linkedHosts)
		if kind, variant, text := stateOfRef(doc).Kind, admVariant(doc), mustValue(t, doc, "text"); kind != RefStateLive || variant != "writer" || text != "t" {
			t.Fatalf("[%v %v %v], want [live writer t]", kind, variant, text)
		}
		frames := admChanges(t, doc, "text")
		l.set("none")
		admLinkDelivered(l.linkedHosts)
		if variant := admVariant(doc); variant != "" {
			t.Fatalf("variant = %q, want none", variant)
		}
		if _, err := admValue(doc, "text"); told(err) != [3]string{"forbidden", "", "no access"} {
			t.Fatalf("text.value threw %v", err)
		}
		expectValues(t, frames.Items(), failedFrame("no access"))
		l.set("read")
		admLinkDelivered(l.linkedHosts)
		if kind, variant, text := stateOfRef(doc).Kind, admVariant(doc), mustValue(t, doc, "text"); kind != RefStateLive || variant != "reader" || text != "t" {
			t.Fatalf("[%v %v %v], want [live reader t]", kind, variant, text)
		}
		expectValues(t, frames.Items(), failedFrame("no access"), "t")
		expectStrings(t, states.All(), "failed: no access", "loading", "live", "failed: no access", "loading", "live")
		doc.Release()
		must(t, l.Client.Close(background))
		must(t, l.Server.Close(background))
	})

	t.Run("a running call is answered forbidden/revoked by a denial, unavailable by another value", func(t *testing.T) {
		l := admLinked(t, admDocCode{Built: &admLog{}, Gate: make(chan struct{})})
		l.set("write")
		doc := mustGet(t, l.Client, admDoc, jsonObject("id", "a"))
		// Pi: `doc.variant === "writer" ? doc.write("x", context) : Promise.resolve()`, and `rejection` of the resolved one fails the case.
		writing := func() *parkedCallResult {
			t.Helper()
			if variant := admVariant(doc); variant != "writer" {
				t.Fatalf("variant = %q, want writer", variant)
			}
			return admStart(t, doc, "write", "x")
		}
		first := writing()
		admLinkDelivered(l.linkedHosts)
		l.set("none")
		admLinkDelivered(l.linkedHosts)
		if got := told(admRejection(t, first)); got[0] != "forbidden" || got[1] != "revoked" {
			t.Fatalf("first = %v, want forbidden/revoked", got)
		}
		l.set("write")
		admLinkDelivered(l.linkedHosts)
		second := writing()
		admLinkDelivered(l.linkedHosts)
		l.set("read")
		admLinkDelivered(l.linkedHosts)
		if got := told(admRejection(t, second)); got[0] != "unavailable" {
			t.Fatalf("second = %v, want unavailable", got)
		}
		must(t, l.Client.Close(background))
		must(t, l.Server.Close(background))
	})

}

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
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		must(t, s.h.Close(background))
	})

	t.Run("to an equal decision: the running call completes, nothing is rebuilt, the decision is kept while the source loads", func(t *testing.T) {
		built, natives := &admLog{}, &admLog{}
		gate, opened := make(chan struct{}), make(chan struct{})
		readyNow := make(chan struct{})
		close(readyNow)
		code := admDocCode{Built: built, Gate: gate, Opened: readyNow, Natives: natives}
		s := admServed(t, code, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		watch, err := refStateWatch(doc, background)
		must(t, err)
		states := statesOf(t, watch)
		writing := admStart(t, doc, "write", "x")
		// The source's successor stays loading until its native opens: the last decision is kept meanwhile.
		report, err := s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{Opened: opened}, admAccess, nil)})
		must(t, err)
		if len(report.Objects) != 1 || report.Objects[0].Object != "test.access" || report.Objects[0].Kind != ReloadRestarted {
			t.Fatalf("report.objects = %+v, want [test.access restarted]", report.Objects)
		}
		s.delivered()
		access := mustHold(t, s.h, admAccess, jsonObject())
		if kind := stateOfRef(access).Kind; kind != RefStateLoading {
			t.Fatalf("access = %v, want loading", kind)
		}
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("doc = %v, want live", kind)
		}
		close(gate)
		if value, err := writing.wait(background); err != nil || value != "write:x" {
			t.Fatalf("write = %v, %v, want write:x", value, err)
		}
		close(opened)
		s.delivered()
		if kind := stateOfRef(access).Kind; kind != RefStateLive {
			t.Fatalf("access = %v, want live", kind)
		}
		// The modes are the store's documents: the same decision, no new attempt, no native reopened.
		expectStrings(t, built.Items(), "a:write")
		expectStrings(t, natives.Items(), "open")
		expectStrings(t, states.All())
		expectNoErrors(t, s.errors)
		access.Release()
		must(t, s.h.Close(background))
	})

	t.Run("whose contract is bumped without accepts seals its readers until their code follows; with accepts it seals nothing", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		// Version 1 still bound through `accepts` (an added member needs no mapping): the reader is untouched.
		_, err := s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{}, admAccessV2, admAccepts1())})
		must(t, err)
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read")
		// Without `accepts` the reader's version is refused: failed, not loading, so it fails closed.
		_, err = s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{}, admAccessV2, nil)})
		must(t, err)
		s.delivered()
		if message := admWaiting(t, doc).Cause.Error.Message; !strings.Contains(message, "test.access v1") {
			t.Fatalf("message = %q, want it to contain test.access v1", message)
		}
		_, err = s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{}, admAccessV2, admAccepts1())})
		must(t, err)
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		must(t, s.h.Close(background))
	})

	t.Run("that removes it parks its readers on it; serving it again rebuilds them", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: built}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		none := DefineFacet(func(FacetHost, FacetSelf) error { return nil })
		_, err := s.reload(map[string]Facet{"access": none})
		must(t, err)
		s.delivered()
		admWaiting(t, doc)
		_, err = s.reload(map[string]Facet{"access": admAccessFacet(admAccessCode{}, admAccess, nil)})
		must(t, err)
		// A new store object: its modes start over, and the reader is denied until it is allowed again.
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("state = %+v, want failed denied", st)
		}
		s.set("read", "server")
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		must(t, s.h.Close(background))
	})

}

// --- a reload of the admitted type (Pi 1113-1288; "2.5, test 20") ---------------------------------------------------------

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
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read")
		must(t, s.h.Close(background))
	})

	// In these three the old attempt's drain is the seam Pi's assertions sit in: it ends within microtasks of the seal, and the
	// shown state Pi asserts "right after `await set`" is the one its flush left, before that end wakes the successor. A Go test
	// goroutine cannot stand between the two, so the call that holds the drain is held (Drain): the test looks at the flush's
	// state, lets the drain end, and then looks at the state it settles in (Pi's, measured at 7cc21c24f6b5).
	t.Run("a decision change while its old attempt drains seals that attempt at once", func(t *testing.T) {
		built, gate := &admLog{}, make(chan struct{})
		s := admServed(t, admDocCode{Built: &admLog{}, Gate: gate}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		writing := admStart(t, doc, "write", "x")
		_, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: built}, admDoc, nil)})
		must(t, err)
		// The old attempt drains its call; its successor waits for it.
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		if n := len(admPresent(s.h, admDoc)); n != 1 {
			t.Fatalf("present = %d, want 1", n)
		}
		s.set("none", "server")
		// The cut is the denial's flush, not the successor's later admission: no value, no presence, from here on.
		admDenied(t, stateOfRef(doc), "no access")
		if _, err := admValue(doc, "text"); told(err)[2] != "no access" {
			t.Fatalf("text.value threw %v", err)
		}
		if n := len(admPresent(s.h, admDoc)); n != 0 {
			t.Fatalf("present = %d, want 0", n)
		}
		if got := told(admRejection(t, writing)); got[0] != "forbidden" || got[1] != "revoked" {
			t.Fatalf("write = %v, want forbidden/revoked", got)
		}
		s.delivered()
		admDenied(t, stateOfRef(doc), "no access")
		expectStrings(t, built.Items(), "a:write")
		// Nothing failed in the flush that cut it (a reference released there would have).
		expectNoErrors(t, s.errors)
		close(gate)
		must(t, s.h.Close(background))
	})

	t.Run("a denial while its old attempt drains is shown by that attempt's own decision, whatever its successor's admission waits for", func(t *testing.T) {
		gate, drain := make(chan struct{}), make(chan struct{})
		s := admServed(t, admDocCode{Built: &admLog{}, Gate: gate, Drain: drain}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		writing := admStart(t, doc, "write", "x")
		// The new admission also needs a type nobody serves: the successor parks on it and decides nothing.
		waits := func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			self.Get(admMissing, jsonObject())
			return admitBy(false)(params, principal, self)
		}
		_, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: &admLog{}, Admit: waits}, admDoc, nil)})
		must(t, err)
		s.delivered()
		admWaiting(t, doc)
		if text := mustValue(t, doc, "text"); text != "t" {
			t.Fatalf("text = %v, want t", text)
		}
		s.set("none", "server")
		admDenied(t, stateOfRef(doc), "no access")
		if _, err := admValue(doc, "text"); told(err)[2] != "no access" {
			t.Fatalf("text.value threw %v", err)
		}
		if got := told(admRejection(t, writing)); got[0] != "forbidden" || got[1] != "revoked" {
			t.Fatalf("write = %v, want forbidden/revoked", got)
		}
		// The old drain is held: nothing wakes the successor's check, the denial stays (Pi's assertions, by microtask depth).
		s.delivered()
		admDenied(t, stateOfRef(doc), "no access")
		// The drain ends: `endOld` lets the old admission's requests go, which wakes the successor's check, and the object
		// shows what that parks on. Pi settles there too (probed: waiting after one `delivered()`, for good), its text still
		// denied.
		close(drain)
		s.delivered()
		if message := admWaiting(t, doc).Cause.Error.Message; !strings.Contains(message, "test.missing") {
			t.Fatalf("message = %q, want it to name test.missing", message)
		}
		if _, err := admValue(doc, "text"); told(err)[2] != "no access" {
			t.Fatalf("text.value threw %v", err)
		}
		close(gate)
		must(t, s.h.Close(background))
	})

	t.Run("another value while its old attempt drains seals that attempt: its call is rejected unavailable, its holders load", func(t *testing.T) {
		built, gate, drain := &admLog{}, make(chan struct{}), make(chan struct{})
		s := admServed(t, admDocCode{Built: &admLog{}, Gate: gate, Drain: drain}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		writing := admStart(t, doc, "write", "x")
		_, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: built}, admDoc, nil)})
		must(t, err)
		s.set("read", "server")
		if kind, text := stateOfRef(doc).Kind, mustValue(t, doc, "text"); kind != RefStateLoading || text != "t" {
			t.Fatalf("[%v %v], want [loading t]", kind, text)
		}
		if got := told(admRejection(t, writing)); got[0] != "unavailable" {
			t.Fatalf("write = %v, want unavailable", got)
		}
		// The drain is held: its successor still waits for it, loading.
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLoading {
			t.Fatalf("state = %v, want loading while the drain is held", kind)
		}
		close(gate)
		close(drain)
		s.delivered()
		if kind, variant := stateOfRef(doc).Kind, admVariant(doc); kind != RefStateLive || variant != "reader" {
			t.Fatalf("[%v %v], want [live reader]", kind, variant)
		}
		expectNoErrors(t, s.errors)
		must(t, s.h.Close(background))
	})

	t.Run("a decision change while the attempt opens a native abandons it: the native closes, the next attempt admits", func(t *testing.T) {
		built, natives, opened := &admLog{}, &admLog{}, make(chan struct{})
		s := admServed(t, admDocCode{Built: built, Opened: opened, Natives: natives, OpenIgnoresAbandon: true}, admOptions{Mode: "read"})
		doc := mustHold(t, s.h, admDoc, jsonObject("id", "a"))
		s.delivered()
		expectStrings(t, built.Items(), "a:read")
		if kind := stateOfRef(doc).Kind; kind != RefStateLoading {
			t.Fatalf("state = %v, want loading", kind)
		}
		s.set("none", "server")
		admDenied(t, stateOfRef(doc), "no access")
		close(opened)
		s.delivered()
		// Opened after its attempt was abandoned: closed at once, and nothing went live.
		expectStrings(t, natives.Items(), "open", "close")
		admDenied(t, stateOfRef(doc), "no access")
		doc.Release()
		must(t, s.h.Close(background))
	})

	t.Run("a denial between staging and activation: the new factory never runs", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		// Pi's order: the reload stages and commits, the test's `set("none")` is made before the microtask that runs the new
		// attempt's admission. A Go transaction of a source lands on a later hop than the host's microtask, so the test holds
		// that hop (internal/admitseam) until the denial has landed; the reload waits for the new attempt's first outcome
		// and runs on a goroutine. What waits: the hop being handed over (the commit started the attempt).
		var mu sync.Mutex
		var starts []func()
		handed := make(chan struct{}, 8)
		admitseam.Hold(s.h, func(start func()) {
			mu.Lock()
			starts = append(starts, start)
			mu.Unlock()
			handed <- struct{}{}
		})
		done := make(chan error, 1)
		go func() {
			_, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: built}, admDoc, nil)})
			done <- err
		}()
		receive(t, handed)
		s.set("none", "server")
		admitseam.Hold(s.h, nil)
		mu.Lock()
		held := slices.Clone(starts)
		mu.Unlock()
		for _, start := range held {
			start()
		}
		must(t, <-done)
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Errorf("state = %+v, want failed denied", st)
		}
		// Staging rehearsed it once; no attempt ran it.
		expectStrings(t, built.Items(), "a:read")
		must(t, s.h.Close(background))
	})

	// Go only: the other order a caller can get. The denial lands after the new attempt's factory ran: the attempt is sealed
	// and the object shows the denial; the rehearsal and the run were both built (Pi's "a decision change while settled").
	t.Run("a denial made after the new factory ran seals its attempt: the rehearsal and the run are both built", func(t *testing.T) {
		built := &admLog{}
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		_, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: built}, admDoc, nil)})
		must(t, err)
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		s.set("none", "server")
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("state = %+v, want failed denied", st)
		}
		expectStrings(t, built.Items(), "a:read", "a:read")
		must(t, s.h.Close(background))
	})
	t.Run("does not rehearse across another admission fingerprint, or without a value: the factory's error is its activation's", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		rehearsed := &admLog{}
		job := func(self Self, _ string) {
			self.Job(admWork, JobImplementation{Run: func(any, JobCtx) (any, error) { return 1.0, nil }})
		}
		// Another fingerprint: the plugin applies, and the refused job fails the restarted object.
		// Pi's order: the new attempt's admission runs a microtask after the commit: its factory has not run when the reload
		// returns, so it holds only what staging rehearsed.
		held := admHoldHops(s.h)
		type reloaded struct {
			report ReloadReport
			err    error
		}
		done := make(chan reloaded, 1)
		go func() {
			report, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: rehearsed, Declare: job}, admDocV2, nil)})
			done <- reloaded{report, err}
		}()
		receive(t, held.handed)
		if rehearsed.Len() != 0 {
			t.Errorf("rehearsed = %v, want none", rehearsed.Items())
		}
		held.release(s.h)
		other := receive(t, done)
		err := other.err
		must(t, err)
		if len(other.report.Failed) != 0 {
			t.Fatalf("failed = %v, want none", other.report.Failed)
		}
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureBroken {
			t.Fatalf("state = %+v, want failed broken", st)
		}
		if n := admJobCount(admReports(s.errors)); n != 1 {
			t.Fatalf("job reports = %d, want 1", n)
		}
		// No value (the object is denied): not rehearsed either, reported when it is allowed.
		_, err = s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: &admLog{}}, admDoc, nil)})
		must(t, err)
		s.set("none", "server")
		s.delivered()
		if st := stateOfRef(doc); st.Kind != RefStateFailed || st.Cause.Kind != data.FailureDenied {
			t.Fatalf("state = %+v, want failed denied", st)
		}
		mark := s.errors.Len() // Pi `errors.length = 0`: the reports below are those after the mark
		ran := &admLog{}
		denied, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: ran, Declare: job}, admDoc, nil)})
		must(t, err)
		if len(denied.Failed) != 0 || ran.Len() != 0 || len(admReports(s.errors)[mark:]) != 0 {
			t.Fatalf("failed = %v, ran = %v, reports = %v, want none", denied.Failed, ran.Items(), admReports(s.errors)[mark:])
		}
		s.set("read", "server")
		s.delivered()
		expectStrings(t, ran.Items(), "a:read")
		if n := admJobCount(admReports(s.errors)[mark:]); n != 1 {
			t.Fatalf("job reports = %d, want 1", n)
		}
		must(t, s.h.Close(background))
	})

	t.Run("a dependency only the newly admitted branch declares parks the object after the commit", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		declare := func(self Self, mode string) {
			if mode == "write" {
				self.Get(admMissing, jsonObject())
			}
		}
		// Rehearsed as a reader: the writer's request is not recorded.
		report, err := s.reload(map[string]Facet{"doc": admDocFacet(admDocCode{Built: &admLog{}, Declare: declare}, admDoc, nil)})
		must(t, err)
		if len(report.Failed) != 0 {
			t.Fatalf("failed = %v", report.Failed)
		}
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		s.set("write", "server")
		s.delivered()
		admWaiting(t, doc)
		must(t, s.h.Close(background))
	})

}

func TestAdmissionWhatADenialRemoves(t *testing.T) {
	t.Run("references hold no value, a watch gets one failed call and keeps what it delivered, a derive over a member fails until the next allow", func(t *testing.T) {
		s := admServed(t, admDocCode{Built: &admLog{}}, admOptions{Mode: "write"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		panel := mustGet(t, s.h, admPanel, jsonObject("id", "a"))
		var watch chord.WatchHandle[delta.JsonValue]
		var err error
		doc.binding.loop.Run(func() { watch, err = doc.Member("text").Watch(background) }) // WAITS-API: ReferenceMember.Watch
		must(t, err)
		frames := nexttest.ChangesOf(watch)
		shouts := admChanges(t, panel, "shout")
		s.set("none", "server")
		s.delivered()
		if _, err := admValue(doc, "text"); err == nil || told(err) != [3]string{"forbidden", "", "no access"} {
			t.Fatalf("text.value threw %v, want forbidden no access", err)
		}
		if variant := admVariant(doc); variant != "" {
			t.Fatalf("variant = %q, want none", variant)
		}
		expectValues(t, frames.Items(), failedFrame("no access"))
		// Data already delivered to application code is not retracted.
		if value := watch.Value(); value != "t" {
			t.Fatalf("watch.value = %v, want t", value)
		}
		// The dependent stays live (R18); its derive over the member fails as a member.
		if kind := stateOfRef(panel).Kind; kind != RefStateLive {
			t.Fatalf("panel = %v, want live", kind)
		}
		expectValues(t, shouts.Items(), failedFrame("no access"))
		if _, err := admValue(panel, "shout"); err == nil || told(err) != [3]string{"forbidden", "", "no access"} {
			t.Fatalf("shout.value threw %v, want forbidden no access", err)
		}
		s.set("read", "server")
		s.delivered()
		s.delivered()
		expectValues(t, frames.Items(), failedFrame("no access"), "t")
		expectValues(t, shouts.Items(), failedFrame("no access"), "T")
		must(t, s.h.Close(background))
	})

	t.Run("shows the denial before the sealed attempt's teardown finished", func(t *testing.T) {
		natives, closing, opened := &admLog{}, make(chan struct{}), make(chan struct{})
		close(opened)
		s := admServed(t, admDocCode{Built: &admLog{}, Opened: opened, Natives: natives, Closing: closing}, admOptions{Mode: "read"})
		doc := mustGet(t, s.h, admDoc, jsonObject("id", "a"))
		s.set("none", "server")
		admDenied(t, stateOfRef(doc), "no access")
		s.delivered()
		expectStrings(t, natives.Items(), "open")
		// Allowed again while the old native still closes: the factory waits for that teardown, the admission did not.
		s.set("read", "server")
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLoading {
			t.Fatalf("state = %v, want loading", kind)
		}
		close(closing)
		s.delivered()
		if kind := stateOfRef(doc).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		expectStrings(t, natives.Items(), "open", "close", "open")
		must(t, s.h.Close(background))
	})
}

// admTable is a store table of one string column that is its key.
func admTable(name, column string) *store.TableDefinition {
	definition, err := store.DefineTable(name, store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col(column, store.Str())}, Key: []string{column}})
	if err != nil {
		panic(err)
	}
	return definition
}

var admLogTable = admTable("log", "line")

// admJournal is Journal (Pi's `journal` local type of the commit-in-flight case): `append` puts a line into the `log` table in
// one transaction, `has` is 1 once the line is in it.
var admJournal = mustContract(contracts.Local("test.journal", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("append", contracts.NewMethod(contracts.Str())),
		contracts.Mem("has", contracts.NewMethod(contracts.Str(), contracts.Int())),
	},
}))

func admJournalServe(host FacetHost) error {
	return host.Serve(admJournal, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
		self := plain.(StoreSelf)
		log := self.Table(admLogTable)
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, tx.Put(log, jsonObject("line", args)) }, ctx)
				return nil, err
			},
			"has": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				row, err := log.Get(ctx, jsonObject("line", args))
				if err != nil || row == nil {
					return 0.0, err
				}
				return 1.0, nil
			},
		}}}, nil
	}, ServeOptions{})
}

func TestAdmissionACommitInFlightAtTheCut(t *testing.T) {
	t.Run("stays durable; the sealed attempt publishes nothing after it", func(t *testing.T) {
		chordStore, _, holdNext := faultyStore(t)
		errs := &hostErrors{}
		address := Server.Address(nil)
		h := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), Store: chordStore, OnError: errs.push})
		// Pi `await h.root(..., context)` inside the handler: Host.Root with a ctx that has the handler's parking signal (it
		// suspends the handler where it waits: chord/next/DESIGN-go-layout.md, "The handler author's contract") and not its
		// cancellation (the test's `context`: the seal of the attempt does not abort the root; the handler ran on past the cut).
		write := func(text string, ctx context.Context) error {
			_, err := h.Root(func(root Ctx) (any, error) {
				journal, err := root.Get(admJournal, jsonObject(), GetOptions{})
				if err != nil {
					return nil, err
				}
				return journal.Call("append", text, root)
			}, context.WithoutCancel(ctx), RootOptions{})
			return err
		}
		journal := DefineFacet(func(host FacetHost, _ FacetSelf) error { return admJournalServe(host) })
		mustLoad(t)(h.Load([]HostFacet{
			{Name: "@test/access", Facet: admAccessFacet(admAccessCode{}, admAccess, nil)},
			{Name: "@test/doc", Facet: admDocFacet(admDocCode{Built: &admLog{}, WriteIn: write}, admDoc, nil)},
			{Name: "@test/journal", Facet: journal},
		}, background))
		access := mustGet(t, h, admAccess, jsonObject())
		set := func(mode string) {
			_, err := access.Call("set", jsonObject("who", "server", "mode", mode), background)
			must(t, err)
		}
		set("write")
		doc := mustGet(t, h, admDoc, jsonObject("id", "a"))
		frames := admChanges(t, doc, "text")
		held := holdNext()
		// Released at the test's end whatever failed: a Fatalf before the release below (the `set`) leaves the commit held, and the
		// host's close, a cleanup, waits for it.
		t.Cleanup(held.Release)
		writing := admStart(t, doc, "write", "kept")
		receive(t, held.Entered())
		// The cut, with the journal's commit admitted and its step running.
		set("none")
		// Errorf, not Fatalf: the held commit must be released below whatever the answer was (a failed assertion must not hang).
		if got := told(admRejection(t, writing)); got[0] != "forbidden" || got[1] != "revoked" {
			t.Errorf("write = %v, want forbidden/revoked", got)
		}
		held.Release()
		deliveredOn(h)
		journalRef := mustGet(t, h, admJournal, jsonObject())
		if has, err := journalRef.Call("has", "kept", background); err != nil || has != 1.0 {
			t.Fatalf("has(kept) = %v, %v, want 1", has, err)
		}
		// The handler ran on past the commit; its own change was refused, and the denied holder got the denial alone.
		expectValues(t, frames.Items(), failedFrame("no access"))
		must(t, h.Close(background))
		must(t, chordStore.Close(background))
	})
}

func admAccepts1() contracts.AcceptsTable {
	return contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{}}}
}

func TestAdmissionANamedQuery(t *testing.T) {
	t.Run("parked on a read that failed is its attempt's first check: a reload of its type reports it and does not wait for the read", func(t *testing.T) {
		failing := &admFlag{}
		chordStore := memoryStore(t)
		address := Server.Address(nil)
		h := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), Store: chordStore, OnError: func(HostError) {}})
		shelf := admShelfFacet(failing)
		plugins := func(reader Facet) []HostFacet {
			return []HostFacet{{Name: "@test/shelf", Facet: shelf}, {Name: "@test/reader", Facet: reader}}
		}
		mustLoad(t)(h.Load(plugins(admReaderFacet()), background))
		api := mustGet(t, h, admShelf, jsonObject())
		put := func(id string) {
			_, err := api.Call("put", id, background)
			must(t, err)
		}
		put("a")
		held := mustGet(t, h, admReader, jsonObject())
		// The read's next run fails: the decision cannot be read, so the object is sealed and parks on the read.
		failing.set(true)
		put("b")
		deliveredOn(h)
		admWaiting(t, held)
		// Pi races the reload against a 200 ms timer: it must settle without waiting for the read. Go: the reload runs on a
		// goroutine and must be done once its first check ran (it cannot wait for the read: the read recovers only below).
		reported := make(chan ReloadReport, 1)
		go func() {
			report, err := h.Reload(plugins(admReaderFacet()), background, ReloadOptions{})
			must(t, err)
			reported <- report
		}()
		report := receive(t, reported) // receive: the host support's bounded wait (hangGuard), no sleep
		if len(report.Objects) != 1 || report.Objects[0].Object != "test.reader" || report.Objects[0].Kind != ReloadParked ||
			report.Objects[0].Cause.Kind != data.FailureWaiting {
			t.Fatalf("report.objects = %+v, want [test.reader parked waiting]", report.Objects)
		}
		// The read recovers: the parked attempt goes on.
		failing.set(false)
		put("c")
		deliveredOn(h)
		if kind := stateOfRef(held).Kind; kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		must(t, h.Close(background))
		must(t, chordStore.Close(background))
	})
}

func admJobCount(reports []string) int {
	n := 0
	for _, r := range reports {
		if strings.Contains(r, "self.job") {
			n++
		}
	}
	return n
}

var (
	admItemsTable = admTable("items", "id")
	admItem       = contracts.Obj(contracts.Prop("id", contracts.Str()))
	admShelf      = mustContract(contracts.Local("test.shelf", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("items", contracts.Queries(admItem, contracts.Params{contracts.Prop("all", contracts.Obj())})),
			contracts.Mem("put", contracts.NewMethod(contracts.Str())),
		},
	}))
	admReader = mustContract(contracts.Remote("test.reader", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Admission:   func() *contracts.Schema { s := contracts.Str(); return &s }(),
		Variants:    []contracts.VariantEntry{contracts.Var("one", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("label", contracts.Str())}})},
	}))
)

// admShelfFacet is a facet serving Shelf: its `items` queries the rows ordered by id (the mapping fails while `failing` is set), its
// `put` writes a row.
func admShelfFacet(failing *admFlag) Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(admShelf, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			self := plain.(StoreSelf)
			rows := self.Table(admItemsTable)
			mapped := func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
				if failing.get() {
					return nil, errors.New("the mapping is unavailable")
				}
				return row, nil
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Queries: map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]{"items": self.Queries(rows, QueriesOptions{Map: mapped},
					map[string]QueryBuilder{"all": {Build: func(delta.JsonValue) *store.Query {
						return store.NewQuery(admItemsTable).OrderBy(func(e, _ store.ColumnRefs) *store.ColumnRef { return e["id"] }, "")
					}}})},
				Methods: map[string]contracts.MethodFunction{"put": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, tx.Put(rows, jsonObject("id", args)) }, ctx)
					return nil, err
				}},
			}}, nil
		}, ServeOptions{})
	})
}

// admReaderFacet is a facet serving Reader, whose admission reads the shelf's items live.
func admReaderFacet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(admReader, func(*delta.JsonObject, data.Principal, delta.JsonValue, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "one", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": Expose(state_constant("x"))},
			}}, nil
		}, ServeOptions{Admit: func(_ *delta.JsonObject, _ data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error) {
			items := self.LiveQuery(self.Get(admShelf, jsonObject()).Queries("items")["all"], jsonObject(), 3)
			return self.Derive([]any{items}, func([]any) (delta.JsonValue, error) { return Allow("yes"), nil }), nil
		}})
	})
}

// admHeldHops holds the admission hop of every attempt that starts (internal/admitseam): Pi's order of microtasks, where what a
// reload's commit starts has not run its admission when the commit returns.
type admHeldHops struct {
	mu     sync.Mutex
	starts []func()
	handed chan struct{}
}

func admHoldHops(h Host) *admHeldHops {
	x := &admHeldHops{handed: make(chan struct{}, 8)}
	admitseam.Hold(h, func(start func()) {
		x.mu.Lock()
		x.starts = append(x.starts, start)
		x.mu.Unlock()
		x.handed <- struct{}{}
	})
	return x
}

// release ends the seam and starts the held hops.
func (x *admHeldHops) release(h Host) {
	admitseam.Hold(h, nil)
	x.mu.Lock()
	held := slices.Clone(x.starts)
	x.mu.Unlock()
	for _, start := range held {
		start()
	}
}

// admActivity is the host's activity.<field> count, read inside a section of the host.
func admActivity(h Host, field string) float64 {
	var value delta.JsonValue
	h.(*chordHost).enter(func() { value, _ = h.Activity().Value() })
	n, _ := value.(*delta.JsonObject).Value(field).(float64)
	return n
}

// admDocV2: Doc with another admission schema (the same wire fingerprint): a reload does not rehearse across it.
var admDocV2 = mustContract(contracts.Evolve(admDoc, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.Admission(contracts.Obj(contracts.Prop("mode", contracts.OneOf("read", "write")), contracts.Prop("since", contracts.Int())))
}))

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
