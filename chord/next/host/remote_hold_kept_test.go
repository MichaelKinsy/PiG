package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/test/remote/hold.test.ts
// Ports the describe 'calls in flight across a `loading` unbind (decision "Calls in flight survive a `loading` unbind")' of
// hold.test.ts (Pi 6515a7977): a raw peer holds `Kept` as hold 0 and sends its calls by the names it was bound with, as a
// consumer does before it learns of a `loading`. `await transported()` is rhSync (a ping the host answers after every
// message sent before it).

// keptContract is `Kept`: a remote object on the server: variant `a` has `bump` and `ping`, variant `b` only `ping`.
var keptContract = func() *contracts.Contract {
	label := rhProp("label", contracts.Str())
	return mustContract(declareRemote("app.kept", contracts.RemoteDeclaration{
		Version: 1, HostAddress: exServerAddress,
		Variants: []contracts.VariantEntry{
			contracts.Var("a", contracts.VariantDeclaration{State: []contracts.Property{label}, Methods: []contracts.MethodEntry{
				contracts.Meth("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
				contracts.Meth("ping", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
			}}),
			contracts.Var("b", contracts.VariantDeclaration{State: []contracts.Property{label}, Methods: []contracts.MethodEntry{
				contracts.Meth("ping", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
			}}),
		},
	}))
}()

// keptV2 is `Kept` v2: its label became an object, which v1 consumers are not mapped to (their version is dropped).
var keptV2 = mustContract(contracts.Evolve(keptContract, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("a", "label", contracts.Obj(contracts.Prop("text", contracts.Str())))
}))

// keptBroken is a local type whose factory throws (with params: no host-lifetime object): one needing it parks on its failure.
var keptBroken = mustContract(declareLocal("app.kept-broken", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

// keptRan records the calls that ran on a code, by method.
type keptRan struct {
	mu   sync.Mutex
	list []string
}

func (r *keptRan) push(method string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, method)
}

func (r *keptRan) all() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.list...)
}

// keptCode is one code of `Kept`.
type keptCode struct {
	// n is what `bump` and `ping` answer.
	n int
	// variant is "a" (the default) or "b".
	variant string
	// native is whether it opens a native (a reload then restarts it), which opens once opened fires.
	native bool
	opened signal
	// gate is what `bump` waits for first.
	gate signal
	// broken is whether it needs `Broken` (it parks on a failed dependency).
	broken bool
	ran    *keptRan
}

// keptFacet is `Kept` as code (with `Broken` served beside it).
func keptFacet(t testing.TB, code keptCode) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(keptBroken, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("broken factory")
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(keptContract, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			if code.native {
				self.Open(func(ctx context.Context) (any, error) {
					if code.opened != nil {
						code.opened.await(t, ctx)
					}
					return nil, nil
				}, func(any, NativeCloseReason, context.Context) error { return nil })
			}
			if code.broken {
				self.Get(keptBroken, jsonObject("id", 1.0))
			}
			ping := func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				code.ran.push("ping")
				return float64(code.n), nil
			}
			bump := func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				if code.gate != nil {
					code.gate.await(t, ctx)
				}
				code.ran.push("bump")
				return float64(code.n), nil
			}
			label := constant("v" + string(rune('0'+code.n)))
			if code.variant == "b" {
				return contracts.Implementation{Variant: "b", Members: contracts.VariantImplementation{
					State:   map[string]chord.Watchable[delta.JsonValue]{"label": label},
					Methods: map[string]contracts.MethodFunction{"ping": ping},
				}}, nil
			}
			return contracts.Implementation{Variant: "a", Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": label},
				Methods: map[string]contracts.MethodFunction{"bump": bump, "ping": ping},
			}}, nil
		}, ServeOptions{})
	})
}

// keptRig is a server serving a code and a raw peer holding its `Kept` as hold 0, bound.
type keptRig struct {
	t      testing.TB
	server Host
	peer   *RawPeer
	ends   [2]*link.MemoryLink
	names  wireIndexes
}

type keptOptions struct {
	closeTimeout, readyTimeout time.Duration
	noAck                      bool
	revokedBy                  RevokedBy[delta.JsonValue]
}

// keptServer is a server serving code and a raw peer holding its `Kept` as hold 0, bound.
func keptServer(t testing.TB, code keptCode, options keptOptions) *keptRig {
	t.Helper()
	host := HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), RevokedBy: options.revokedBy, OnError: func(HostError) {}}
	if options.closeTimeout != 0 {
		host.CloseTimeout = &options.closeTimeout
	}
	if options.readyTimeout != 0 {
		host.ReadyTimeout = &options.readyTimeout
	}
	server := newHost(t, host)
	mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: keptFacet(t, code)}}, background))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{NoAck: options.noAck})
	peer.Next("hello")
	rhHold(peer, 0, keptContract)
	peer.Next("bound")
	return &keptRig{t: t, server: server, peer: peer, ends: [2]*link.MemoryLink{a, b}, names: wireNames(t, keptContract, "a")}
}

// reload reloads the server's plugin with code.
func (k *keptRig) reload(code keptCode) ReloadReport {
	k.t.Helper()
	return k.reloadFacet(keptFacet(k.t, code))
}

func (k *keptRig) reloadFacet(facet Facet) ReloadReport {
	k.t.Helper()
	return reloadMust(k.t)(k.server.Reload([]HostFacet{{Name: "@test/app", Facet: facet}}, background, ReloadOptions{}))
}

// call sends call `id` of method (an index of `Kept`'s variant `a`) on hold 0.
func (k *keptRig) call(id int, method string) {
	k.peer.Send(&link.CallMessage{Call: id, Hold: 0, Method: k.names.Methods[method]})
}

// answersTo are the answers (`result`, `error`) the peer received for call id, taken.
func (k *keptRig) answersTo(id int) []link.Message {
	k.peer.mu.Lock()
	defer k.peer.mu.Unlock()
	var answers, rest []link.Message
	for _, message := range k.peer.received {
		switch answer := message.(type) {
		case *link.ResultMessage:
			if answer.Call == id {
				answers = append(answers, message)
				continue
			}
		case *link.ErrorMessage:
			if answer.Call == id {
				answers = append(answers, message)
				continue
			}
		}
		rest = append(rest, message)
	}
	k.peer.received = rest
	return answers
}

// sync is Pi's `await transported()`.
func (k *keptRig) sync() { rhSync(k.t, k.peer) }

// result is the `result` message next received: its call and number.
func (k *keptRig) result() (int, any) {
	k.t.Helper()
	message := k.peer.Next("result").(*link.ResultMessage)
	return message.Call, partValue(k.t, message.Value)
}

// keptError is the `error` message next received: its call, code, reason and message.
func (k *keptRig) error() (call int, code, reason, text string) {
	k.t.Helper()
	message := k.peer.Next("error").(*link.ErrorMessage)
	inner, _ := message.Error.Value("error").(*delta.JsonObject)
	if inner == nil {
		k.t.Fatalf("error %s is no chord error", rhWire(k.t, message.Error))
	}
	code, _ = inner.Value("code").(string)
	reason, _ = inner.Value("reason").(string)
	text, _ = inner.Value("message").(string)
	return message.Call, code, reason, text
}

func (k *keptRig) none(id int) {
	k.t.Helper()
	if got := k.answersTo(id); len(got) != 0 {
		k.t.Fatalf("call %d answered: %v", id, got)
	}
}

func keptWant(t testing.TB, what string, got, want any) {
	t.Helper()
	rhEqual(t, what, got, want)
}

func TestHoldCallsInFlightAcrossALoadingUnbind(t *testing.T) {
	// hold.test.ts: calls in flight across a `loading` unbind › a restart: the running call drains on the old code, one arriving during the drain runs on the new code
	t.Run("a restart: the running call drains on the old code, one arriving during the drain runs on the new code", func(t *testing.T) {
		gate, opened := newSignal(), newSignal()
		t.Cleanup(gate.fire)
		t.Cleanup(opened.fire)
		ran := &keptRan{}
		k := keptServer(t, keptCode{n: 1, native: true, gate: gate, ran: ran}, keptOptions{})
		k.call(0, "bump")
		k.sync()
		report := k.reload(keptCode{n: 2, native: true, opened: opened, ran: ran})
		reloadWantObjects(t, report, ReloadedObject{Object: "app.kept", Kind: ReloadRestarted})
		// The hold stays bound while the old instance drains: a call sent now waits at the gate.
		k.call(1, "bump")
		k.sync()
		gate.fire()
		if call, value := k.result(); call != 0 || value != 1.0 {
			t.Fatalf("first result = %d %v, want 0 1", call, value)
		}
		k.peer.Next("loading")
		k.sync()
		// Kept across the `loading`: no answer until the new attempt is live.
		k.none(1)
		opened.fire()
		k.peer.Next("bound")
		if call, value := k.result(); call != 1 || value != 2.0 {
			t.Fatalf("second result = %d %v, want 1 2", call, value)
		}
		keptWant(t, "ran", ran.all(), []string{"bump", "bump"})
		for _, kind := range k.peer.Kinds() {
			if kind == "error" {
				t.Fatalf("an error was sent: %v", k.peer.Kinds())
			}
		}
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a call stuck on the old code past closeTimeout is answered released at the seal, once
	t.Run("a call stuck on the old code past closeTimeout is answered released at the seal, once", func(t *testing.T) {
		stuck, opened := newSignal(), newSignal()
		t.Cleanup(stuck.fire)
		t.Cleanup(opened.fire)
		k := keptServer(t, keptCode{n: 1, native: true, gate: stuck}, keptOptions{closeTimeout: 30 * time.Millisecond})
		k.call(0, "bump")
		k.sync()
		k.reload(keptCode{n: 2, native: true, opened: opened})
		k.peer.Next("loading")
		if call, code, _, _ := k.error(); call != 0 || code != "released" {
			t.Fatalf("error = call %d %s, want call 0 released", call, code)
		}
		opened.fire()
		k.peer.Next("bound")
		k.sync()
		k.none(0)
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a kept call takes the gate's rejection: the parked cause, or unavailable after readyTimeout
	t.Run("a kept call takes the gate's rejection: the parked cause, or unavailable after readyTimeout", func(t *testing.T) {
		parked := keptServer(t, keptCode{n: 1, native: true}, keptOptions{})
		opened := newSignal()
		t.Cleanup(opened.fire)
		parked.reload(keptCode{n: 2, native: true, opened: opened, broken: true})
		parked.call(0, "ping")
		opened.fire()
		failed := parked.peer.Next("failed").(*link.FailedMessage)
		if failed.Cause.Kind != "waiting" {
			t.Fatalf("cause = %+v, want waiting", failed.Cause)
		}
		if call, _, _, text := parked.error(); call != 0 || !strings.Contains(text, "broken factory") {
			t.Fatalf("error = call %d %q, want call 0 with broken factory", call, text)
		}

		timed := keptServer(t, keptCode{n: 1, native: true}, keptOptions{readyTimeout: 60 * time.Millisecond})
		never := newSignal()
		t.Cleanup(never.fire)
		timed.reload(keptCode{n: 2, native: true, opened: never})
		timed.call(0, "ping")
		timed.peer.Next("loading")
		// Bounded by readyTimeout (here the new attempt's own deadline fails it first).
		if call, code, _, text := timed.error(); call != 0 || code != "unavailable" || !strings.Contains(text, "60 ms") {
			t.Fatalf("error = call %d %s %q, want call 0 unavailable 60 ms", call, code, text)
		}
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a kept call aborted or released by its consumer gets no answer and never runs
	t.Run("a kept call aborted or released by its consumer gets no answer and never runs", func(t *testing.T) {
		ran := &keptRan{}
		opened, second := newSignal(), newSignal()
		t.Cleanup(opened.fire)
		t.Cleanup(second.fire)
		k := keptServer(t, keptCode{n: 1, native: true, ran: ran}, keptOptions{})
		k.reload(keptCode{n: 2, native: true, opened: opened, ran: ran})
		k.call(0, "ping")
		k.call(1, "ping")
		k.peer.Next("loading")
		k.peer.Send(&link.AbortMessage{Call: 0})
		k.sync()
		opened.fire()
		k.peer.Next("bound")
		if call, _ := k.result(); call != 1 {
			t.Fatalf("result call = %d, want 1", call)
		}
		k.sync()
		k.none(0)
		keptWant(t, "ran", ran.all(), []string{"ping"})
		// A release while kept: the call goes with the hold.
		k.reload(keptCode{n: 3, native: true, opened: second, ran: ran})
		k.call(2, "ping")
		k.peer.Next("loading")
		k.peer.Send(&link.ReleaseMessage{Hold: 0})
		k.sync()
		second.fire()
		k.sync()
		k.none(2)
		keptWant(t, "ran", ran.all(), []string{"ping"})
	})

	// hold.test.ts: calls in flight across a `loading` unbind › across a variant change a kept call runs by name: one the new variant lacks fails invalid/member
	t.Run("across a variant change a kept call runs by name: one the new variant lacks fails invalid/member", func(t *testing.T) {
		opened := newSignal()
		t.Cleanup(opened.fire)
		k := keptServer(t, keptCode{n: 1, native: true}, keptOptions{})
		k.reload(keptCode{n: 2, variant: "b", native: true, opened: opened})
		k.call(0, "bump")
		k.call(1, "ping")
		k.peer.Next("loading")
		opened.fire()
		fresh := k.peer.Next("bound").(*link.BoundMessage)
		if fresh.Holds[0].Variant != wireNames(t, keptContract, "b").Variant {
			t.Fatalf("variant = %d, want b's", fresh.Holds[0].Variant)
		}
		if call, code, reason, _ := k.error(); call != 0 || code != "invalid" || reason != "member" {
			t.Fatalf("error = call %d %s/%s, want call 0 invalid/member", call, code, reason)
		}
		if call, value := k.result(); call != 1 || value != 2.0 {
			t.Fatalf("result = %d %v, want 1 2", call, value)
		}
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a kept call of a version the new code drops is rejected contract_mismatch with the hold's failed
	t.Run("a kept call of a version the new code drops is rejected contract_mismatch with the hold's failed", func(t *testing.T) {
		opened := newSignal()
		t.Cleanup(opened.fire)
		ran := &keptRan{}
		k := keptServer(t, keptCode{n: 1, native: true}, keptOptions{})
		k.reloadFacet(DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(keptV2, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				self.Open(func(ctx context.Context) (any, error) { opened.await(t, ctx); return nil, nil },
					func(any, NativeCloseReason, context.Context) error { return nil })
				ping := func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					ran.push("ping")
					return 2.0, nil
				}
				return contracts.Implementation{Variant: "a", Members: contracts.VariantImplementation{
					State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant(jsonObject("text", "v2"))},
					Methods: map[string]contracts.MethodFunction{"bump": ping, "ping": ping},
				}}, nil
			}, ServeOptions{})
		}))
		k.call(0, "ping")
		k.peer.Next("loading")
		opened.fire()
		failed := k.peer.Next("failed").(*link.FailedMessage)
		if failed.Cause.Kind != "waiting" || !strings.Contains(rhWire(t, failed.Cause.Error), "contract_mismatch") {
			t.Fatalf("cause = %s, want waiting contract_mismatch", rhJSON(t, failed.Cause))
		}
		if call, code, _, _ := k.error(); call != 0 || code != "contract_mismatch" {
			t.Fatalf("error = call %d %s, want call 0 contract_mismatch", call, code)
		}
		keptWant(t, "ran", ran.all(), []string(nil))
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a call by the old names crossing the loading on the wire waits at the gate, never not_ready
	t.Run("a call by the old names crossing the loading on the wire waits at the gate, never not_ready", func(t *testing.T) {
		opened := newSignal()
		t.Cleanup(opened.fire)
		k := keptServer(t, keptCode{n: 1, native: true}, keptOptions{})
		k.reload(keptCode{n: 2, native: true, opened: opened})
		// Sent as a consumer sends it before it learns of the `loading`: by the names it was bound with.
		k.peer.Next("loading")
		k.call(0, "bump")
		k.sync()
		k.none(0)
		opened.fire()
		k.peer.Next("bound")
		if call, value := k.result(); call != 0 || value != 2.0 {
			t.Fatalf("result = %d %v, want 0 2", call, value)
		}
	})

	// hold.test.ts: calls in flight across a `loading` unbind › binding with the previous names, then a restart: the call running on the swapped-in instance drains to its result
	t.Run("binding with the previous names, then a restart: the call running on the swapped-in instance drains to its result", func(t *testing.T) {
		gate, opened := newSignal(), newSignal()
		t.Cleanup(gate.fire)
		t.Cleanup(opened.fire)
		k := keptServer(t, keptCode{n: 1}, keptOptions{noAck: true})
		// A second `bound` in flight fills the window (two); the next swap in place leaves its fresh `bound` waiting.
		k.reload(keptCode{n: 2})
		k.peer.Next("bound")
		k.reload(keptCode{n: 3, gate: gate})
		k.sync()
		keptWant(t, "kinds", k.peer.Kinds(), []string{})
		k.call(0, "bump")
		k.sync()
		// A restart unbinds the binding hold at once: the call runs on the live (unsealed) instance and stays.
		k.reload(keptCode{n: 4, native: true, opened: opened})
		k.peer.Next("loading")
		k.sync()
		k.none(0)
		gate.fire()
		if call, value := k.result(); call != 0 || value != 3.0 {
			t.Fatalf("result = %d %v, want 0 3", call, value)
		}
		opened.fire()
		k.peer.Ack(link.Optional[int]{})
		k.peer.Ack(link.Optional[int]{})
		k.peer.Next("bound")
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a revocation racing the rebind ends the kept call with the link: no result
	t.Run("a revocation racing the rebind ends the kept call with the link: no result", func(t *testing.T) {
		ran := &keptRan{}
		never := newSignal()
		t.Cleanup(never.fire)
		revokedBy := RevokedBy[delta.JsonValue](func(principal data.Principal) ([]delta.JsonValue, error) {
			if principal.Value("kind") == "owner" {
				return []delta.JsonValue{"owner"}, nil
			}
			return nil, nil
		})
		k := keptServer(t, keptCode{n: 1, native: true, ran: ran}, keptOptions{revokedBy: revokedBy})
		k.reload(keptCode{n: 2, native: true, opened: never, ran: ran})
		k.call(0, "ping")
		k.peer.Next("loading")
		must(t, k.server.Revoke("owner", background))
		transported(k.ends[0], k.ends[1])
		if !k.peer.Closed() {
			t.Fatalf("the link stayed open")
		}
		for _, answer := range k.answersTo(0) {
			if answer.Kind() == "result" {
				t.Fatalf("a result was sent: %v", answer)
			}
		}
		keptWant(t, "ran", ran.all(), []string(nil))
	})

	// hold.test.ts: calls in flight across a `loading` unbind › a failed unbind still rejects: a live failure answers its calls with the failure, later ones not_ready
	t.Run("a failed unbind still rejects: a live failure answers its calls with the failure, later ones not_ready", func(t *testing.T) {
		gate := newSignal()
		t.Cleanup(gate.fire)
		var self Self
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
		facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(keptContract, func(_ *delta.JsonObject, _ data.Principal, declared Self) (contracts.Implementation, error) {
				self = declared
				ping := func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					gate.await(t, ctx)
					return 1.0, nil
				}
				return contracts.Implementation{Variant: "a", Members: contracts.VariantImplementation{
					State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("v1")},
					Methods: map[string]contracts.MethodFunction{"bump": ping, "ping": ping},
				}}, nil
			}, ServeOptions{})
		})
		mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: facet}}, background))
		a, b := link.MemoryLinkPair()
		must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		peer := newRawPeer(t, b, rawPeerOptions{})
		peer.Next("hello")
		rhHold(peer, 0, keptContract)
		peer.Next("bound")
		k := &keptRig{t: t, server: server, peer: peer, ends: [2]*link.MemoryLink{a, b}, names: wireNames(t, keptContract, "a")}
		k.call(0, "ping")
		k.sync()
		failLive(self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "it broke"}, nil))
		if call, _, _, text := k.error(); call != 0 || text != "it broke" {
			t.Fatalf("error = call %d %q, want call 0 it broke", call, text)
		}
		peer.Next("failed")
		// Told a failure, the hold takes no calls by the old names.
		k.call(1, "ping")
		if call, code, _, _ := k.error(); call != 1 || code != "not_ready" {
			t.Fatalf("error = call %d %s, want call 1 not_ready", call, code)
		}
	})
}

// keptPlaced is `Kept` placed on a session's worker.
var keptPlaced = mustContract(declareRemote("app.placed-kept", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())}, HostAddress: exSessionAddress,
	Variants: []contracts.VariantEntry{contracts.Var("a", contracts.VariantDeclaration{
		State:   []contracts.Property{rhProp("label", contracts.Str())},
		Methods: []contracts.MethodEntry{contracts.Meth("ping", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
	})},
}))

// keptServePlaced serves `PlacedKept` answering n (recorded in ran), with a native that opens once opened fires.
func keptServePlaced(t testing.TB, h FacetHost, n int, ran *keptRan, opened signal) error {
	return h.Serve(keptPlaced, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		self.Open(func(ctx context.Context) (any, error) {
			if opened != nil {
				opened.await(t, ctx)
			}
			return nil, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		return contracts.Implementation{Variant: "a", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("v" + string(rune('0'+n)))},
			Methods: map[string]contracts.MethodFunction{"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				ran.push(string(rune('0' + n)))
				return float64(n), nil
			}},
		}}, nil
	}, ServeOptions{})
}

func TestHoldCallsInFlightAcrossALoadingUnbindThroughARelay(t *testing.T) {
	key := Session.Address(jsonObject("id", "S1")).Key
	start := func(t *testing.T, ran *keptRan) *relayed {
		rel := relay(t, relayedOptions{Worker: func(h FacetHost) error { return keptServePlaced(t, h, 1, ran, nil) }})
		return rel
	}
	reloadWorker := func(rel *relayed, n int, ran *keptRan, opened signal) {
		rel.WorkerLoaded(t, key)
		_, err := rel.Worker(key).Reload([]HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if _, err := serveNotes(h, PlacedNotes); err != nil {
				return err
			}
			return keptServePlaced(t, h, n, ran, opened)
		})}}, background, ReloadOptions{})
		must(t, err)
	}

	// hold.test.ts: calls in flight across a `loading` unbind › through a relay › a worker's restart: the tab's kept call crosses the forwarded loading and is answered by the new code
	t.Run("a worker's restart: the tab's kept call crosses the forwarded loading and is answered by the new code", func(t *testing.T) {
		ran := &keptRan{}
		rel := start(t, ran)
		kept := refGet(t, rel.Tab, keptPlaced, jsonObject("session", "S1"))
		watch, err := refStateWatch(kept, background)
		must(t, err)
		states := statesOf(t, watch)
		opened := newSignal()
		t.Cleanup(opened.fire)
		reloadWorker(rel, 2, ran, opened)
		// Still live during the drain: the call goes out and waits at the worker's gate.
		answer := startReloadCall(kept, "ping", nil)
		rel.Transported()
		rhWaitFor(t, func() bool { return len(states.All()) >= 1 })
		rhEqual(t, "states", states.All(), []string{"loading"})
		opened.fire()
		if got := answer.wait(t); got != 2.0 {
			t.Fatalf("ping = %v, want 2", got)
		}
		rhWaitFor(t, func() bool { rel.Transported(); return len(states.All()) >= 2 })
		rhEqual(t, "states", states.All(), []string{"loading", "live"})
		keptWant(t, "ran", ran.all(), []string{"2"})
	})

	// hold.test.ts: calls in flight across a `loading` unbind › through a relay › a downstream drop while the call is kept aborts it on the worker: it never runs
	t.Run("a downstream drop while the call is kept aborts it on the worker: it never runs", func(t *testing.T) {
		ran := &keptRan{}
		rel := start(t, ran)
		kept := refGet(t, rel.Tab, keptPlaced, jsonObject("session", "S1"))
		opened := newSignal()
		t.Cleanup(opened.fire)
		reloadWorker(rel, 2, ran, opened)
		answer := startReloadCall(kept, "ping", nil)
		rel.Transported()
		rel.TabLink.Close()
		err := answer.rejected(t)
		if _, ok := data.IsChordErrorCode(err, data.CodeUnavailable); !ok {
			t.Fatalf("ping error = %v, want unavailable", err)
		}
		rel.Transported()
		opened.fire()
		rel.Transported()
		keptWant(t, "ran", ran.all(), []string(nil))
	})
}
