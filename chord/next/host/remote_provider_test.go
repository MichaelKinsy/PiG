package host

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports packages/chord/test/next/remote/provider.test.ts: the provider side of a link, driven by a raw consumer peer.

// holdSpec is what a `hold` message names of a type: its name, version and fingerprint (Pi spreads the contract and the
// override).
type holdSpec struct {
	Name        string
	Version     int
	Fingerprint string
}

func specOf(typ *contracts.Contract) holdSpec {
	return holdSpec{Name: typ.Name, Version: typ.Version, Fingerprint: typ.Fingerprint}
}

func mustJSONPart(t testing.TB, value any) link.Part {
	t.Helper()
	part, err := link.JsonPart(value)
	must(t, err)
	return part
}

// originOf is Pi's optional `origin` argument.
func originOf(origin []int) link.Optional[int] {
	if len(origin) == 0 {
		return link.Optional[int]{}
	}
	return link.Some(origin[0])
}

// provHold sends a hold of spec (a Type's own version and fingerprint unless overridden), of origin when given.
func provHold(t testing.TB, peer *RawPeer, id int, spec holdSpec, params any, origin ...int) {
	t.Helper()
	peer.Send(&link.HoldMessage{
		Origin: originOf(origin), Hold: id, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
		Params: mustJSONPart(t, params), Address: serverWireAddress,
	})
}

// provCall sends a call of the method at index method of the bound variant, with args (none when nil).
func provCall(t testing.TB, peer *RawPeer, id, holdID, method int, args any, origin ...int) {
	t.Helper()
	message := &link.CallMessage{Origin: originOf(origin), Call: id, Hold: holdID, Method: method}
	if args != nil {
		message.Args = mustJSONPart(t, args)
	}
	peer.Send(message)
}

// settle is `await transported()` over the two ends of the raw peer's link.
func settle(peer *RawPeer) {
	end := peer.link.(*link.MemoryLink)
	transported(end)
}

// eventLog is a mutex-guarded list of strings that factories and natives push to (off the loop) and a test awaits.
type eventLog struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []string
}

func newEventLog() *eventLog {
	log := &eventLog{}
	log.cond = sync.NewCond(&log.mu)
	return log
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
	l.cond.Broadcast()
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.events...)
}

// await blocks until event is in the log.
func (l *eventLog) await(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for !slices.Contains(l.events, event) {
		l.cond.Wait()
	}
}

func requireStrings(t testing.TB, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// chordWire is the code and reason of a wire error of kind `chord`.
func chordWire(t testing.TB, wire link.WireError) (code, reason, message string) {
	t.Helper()
	if kind, _ := wire.Value("kind").(string); kind != "chord" {
		t.Fatalf("wire error kind = %q, want chord", kind)
	}
	inner := wire.Value("error").(*delta.JsonObject)
	code, _ = inner.Value("code").(string)
	reason, _ = inner.Value("reason").(string)
	message, _ = inner.Value("message").(string)
	return code, reason, message
}

func requireChordWire(t testing.TB, wire link.WireError, wantCode, wantReason string) {
	t.Helper()
	code, reason, _ := chordWire(t, wire)
	if code != wantCode || (wantReason != "" && reason != wantReason) {
		t.Fatalf("wire error = %s/%s, want %s/%s", code, reason, wantCode, wantReason)
	}
}

// protocolErrors are the messages of the reported protocol errors.
func protocolErrors(errs *hostErrors) []string {
	var messages []string
	for _, err := range errs.All() {
		if err.Kind == ErrProtocolError {
			messages = append(messages, err.Error.Error())
		} else {
			messages = append(messages, "unexpected "+string(err.Kind))
		}
	}
	return messages
}

func sortedNames(named map[string]any) []string {
	keys := make([]string, 0, len(named))
	for key := range named {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// stateMessages are the `state` messages received and not taken.
func stateMessages(peer *RawPeer) []*link.StateMessage {
	var states []*link.StateMessage
	for _, message := range peer.Received() {
		if state, ok := message.(*link.StateMessage); ok {
			states = append(states, state)
		}
	}
	return states
}

// windowedKinds are the `bound` and `state` messages received and not taken, by kind.
func windowedKinds(peer *RawPeer) []string {
	var kinds []string
	for _, kind := range peer.Kinds() {
		if kind == "bound" || kind == "state" {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// providerKept is a remote type kept per id.
var providerKept = mustContract(contracts.Remote("app.kept", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("id", contracts.Int())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{
		contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}}),
	},
}))

// remoteOneVariant declares a remote type of the server with the one variant v.
func remoteOneVariant(name string, params contracts.Params, variant contracts.VariantDeclaration) *contracts.Contract {
	return mustContract(contracts.Remote(name, contracts.RemoteDeclaration{
		Version:     1,
		Params:      params,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", variant)},
	}))
}

func constantNumber(value float64) chord.Watchable[delta.JsonValue] {
	return Expose(state.NewConstant(value))
}

// notesServerRaw is a server serving Notes and a raw consumer peer stamped child.
type notesServerRaw struct {
	*rawLinkedHost
	Probe *notesProbe
}

func notesServer(t testing.TB, child data.Principal) *notesServerRaw {
	t.Helper()
	var probe *notesProbe
	linkedHost := rawLinked(t, func(host FacetHost) error {
		var err error
		probe, err = serveNotes(host, nil)
		return err
	}, rawLinkedOptions{Child: child})
	return &notesServerRaw{rawLinkedHost: linkedHost, Probe: probe}
}

func TestProviderHoldsArrivingOnALink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		reader := wireNames(t, Notes, "reader")

		// Ports provider.test.ts "holds arriving on a link › bind as the link's principal: bound with the variant and the snapshot".
		t.Run("bind as the link's principal: bound with the variant and the snapshot", func(t *testing.T) {
			s := notesServer(t, guestPrincipal("S1"))
			addNote(t, s.Server, "S1", "a")
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			bound := s.Peer.Next("bound").(*link.BoundMessage).Holds[0]
			if bound.Hold != 0 || bound.Variant != reader.Variant || len(bound.Failed) != 0 {
				t.Fatalf("bound = %+v, want hold 0, the reader variant, nothing failed", bound)
			}
			nexttest.RequireJSONEqual(t, reader.NamedBound(t, bound.Members),
				map[string]any{"notes": []any{jsonObject("text", "a")}, "count": 1.0})
		})

		// Ports "keep %s refused, failed { waiting }" (it.each over four cases).
		refused := []struct {
			name    string
			spec    holdSpec
			code    string
			message string
		}{
			{"a type not served here", holdSpec{Name: "app.nope", Version: Notes.Version, Fingerprint: Notes.Fingerprint},
				"unavailable", "app.nope is not served here"},
			{"a local type", holdSpec{Name: "app.note-store", Version: Notes.Version, Fingerprint: Notes.Fingerprint},
				"unavailable", "app.note-store is not served here"},
			{"another version", holdSpec{Name: Notes.Name, Version: 2, Fingerprint: Notes.Fingerprint},
				"contract_mismatch", "app.notes v2 does not match the served v1"},
			{"another fingerprint", holdSpec{Name: Notes.Name, Version: Notes.Version, Fingerprint: "0000000000000000"},
				"contract_mismatch", "app.notes v1 does not match the served v1"},
		}
		for _, each := range refused {
			t.Run("keep "+each.name+" refused, failed { waiting }", func(t *testing.T) {
				s := notesServer(t, nil)
				provHold(t, s.Peer, 0, each.spec, jsonObject("session", "S1"))
				failed := s.Peer.Next("failed").(*link.FailedMessage)
				if failed.Hold != 0 || failed.Cause.Kind != "waiting" {
					t.Fatalf("failed = %+v, want hold 0, waiting", failed)
				}
				code, _, message := chordWire(t, failed.Cause.Error)
				if code != each.code || message != each.message {
					t.Fatalf("error = %s %q, want %s %q", code, message, each.code, each.message)
				}
				// No backoff window: an event (a publication) wakes it.
				if failed.RetryIn.Ok {
					t.Fatalf("retryIn = %d, want none", failed.RetryIn.Value)
				}
			})
		}

		// Ports "refuse invalid params with a broken failed".
		t.Run("refuse invalid params with a broken failed", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", 1.0))
			failed := s.Peer.Next("failed").(*link.FailedMessage)
			if failed.Cause.Kind != "broken" {
				t.Fatalf("cause kind = %s, want broken", failed.Cause.Kind)
			}
			requireChordWire(t, failed.Cause.Error, "invalid", "params")
		})

		// Ports "arriving before the publication wait for it, and bind before the start barrier is over".
		t.Run("arriving before the publication wait for it, and bind before the start barrier is over", func(t *testing.T) {
			server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
			a, b := link.MemoryLinkPair()
			must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			peer := newRawPeer(t, b, rawPeerOptions{})
			provHold(t, peer, 0, specOf(Notes), jsonObject("session", "S1"))
			settle(peer)
			requireStrings(t, peer.Kinds(), []string{"hello"})
			must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
				_, err := serveNotes(h, nil)
				return err
			})}}, background))
			bound := peer.Next("bound").(*link.BoundMessage).Holds[0]
			if bound.Hold != 0 || bound.Variant != editorWire(t).Variant {
				t.Fatalf("bound = %+v, want hold 0 of the editor variant", bound)
			}
		})
	})
}

func TestProviderCallsArrivingOnALink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		reader := wireNames(t, Notes, "reader")
		add, failMethod, wait := editor.Methods["add"], editor.Methods["fail"], editor.Methods["wait"]

		// Ports "calls arriving on a link › run as the link's principal with the child's incarnation; results and errors go back".
		t.Run("run as the link's principal with the child's incarnation; results and errors go back", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, add, jsonObject("text", "x"))
			nexttest.RequireJSONEqual(t, partValue(t, s.Peer.Next("result").(*link.ResultMessage).Value), 1.0)
			provCall(t, s.Peer, 1, 0, failMethod, jsonObject("how", "plugin"))
			nexttest.RequireJSONEqual(t, s.Peer.Next("error").(*link.ErrorMessage).Error,
				jsonObject("kind", "plugin", "code", "quota", "message", "too many", "details", jsonObject("limit", 1.0)))
			provCall(t, s.Peer, 2, 0, wait, nil)
			waited := s.Probe.AwaitWaits(t, 1)[0].(Ctx)
			if principalKind(waited.Principal()) != "owner" || waited.Incarnation() != rawPeerIncarnation {
				t.Fatalf("handler ctx = %v as %s, want the owner with the raw peer's incarnation", waited.Principal(), waited.Incarnation())
			}
		})

		// Ports "outside the binding's variant cannot be named: an index past its methods, forged at the wire, closes the link".
		t.Run("outside the binding's variant cannot be named: an index past its methods, forged at the wire, closes the link", func(t *testing.T) {
			s := notesServer(t, guestPrincipal("S1"))
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			// The reader variant has one method, `wait`; the editor's `add` has no index in it.
			provCall(t, s.Peer, 0, 0, len(reader.Methods), jsonObject("text", "x"))
			settle(s.Peer)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
			requireStrings(t, protocolErrors(s.Errors), []string{"hold 0: no method 1"})
		})

		// Ports "check their arguments".
		t.Run("check their arguments", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, add, jsonObject("text", "x", "extra", 1.0))
			requireChordWire(t, s.Peer.Next("error").(*link.ErrorMessage).Error, "invalid", "args")
		})

		// Ports "before bound are refused not_ready (race 3)".
		t.Run("before bound are refused not_ready (race 3)", func(t *testing.T) {
			Slow := remoteOneVariant("app.slow", contracts.Params{}, contracts.VariantDeclaration{
				State: []contracts.Property{contracts.Prop("n", contracts.Int())},
			})
			s := rawLinked(t, func(h FacetHost) error {
				return h.Serve(Slow, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					self.Open(func(ctx context.Context) (any, error) {
						<-ctx.Done()
						return nil, context.Cause(ctx)
					}, func(any, NativeCloseReason, context.Context) error { return nil })
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": constantNumber(1)},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(Slow), jsonObject())
			provCall(t, s.Peer, 0, 0, 0, nil)
			requireChordWire(t, s.Peer.Next("error").(*link.ErrorMessage).Error, "not_ready", "")
		})

		// Ports "cancelled are aborted and get no reply; a result crossing the cancel is never sent (race 1)".
		t.Run("cancelled are aborted and get no reply; a result crossing the cancel is never sent (race 1)", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, wait, nil)
			waited := s.Probe.AwaitWaits(t, 1)[0]
			s.Peer.Send(&link.AbortMessage{Call: 0})
			settle(s.Peer)
			<-waited.Done()
			requireStrings(t, s.Peer.Kinds(), []string{"hello"})
		})
	})
}

func TestProviderIDs(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		add, wait := editor.Methods["add"], editor.Methods["wait"]

		// Ports "ids › a link ignores ids it does not know and messages for the other direction".
		t.Run("a link ignores ids it does not know and messages for the other direction", func(t *testing.T) {
			s := notesServer(t, nil)
			s.Peer.Send(&link.ReleaseMessage{Hold: 9})
			s.Peer.Send(&link.AbortMessage{Call: 9})
			provCall(t, s.Peer, 0, 9, add, jsonObject("text", "x"))
			s.Peer.Send(&link.GoneMessage{Hold: 9, Error: link.WireErrorOf(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "m"}, nil))})
			settle(s.Peer)
			if s.Peer.Closed() {
				t.Fatal("the link closed")
			}
			requireStrings(t, s.Peer.Kinds(), []string{"hello"})
			if s.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", s.Errors.All())
			}
		})

		// Ports "a hold reusing a live id closes the link".
		t.Run("a hold reusing a live id closes the link", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			settle(s.Peer)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
			requireStrings(t, protocolErrors(s.Errors), []string{"hold 0: the id is in use"})
		})

		// Ports "a call reusing a live id closes the link".
		t.Run("a call reusing a live id closes the link", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, wait, nil)
			provCall(t, s.Peer, 0, 0, wait, nil)
			settle(s.Peer)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
		})

		// Ports "a message the codec cannot decode closes the link".
		t.Run("a message the codec cannot decode closes the link", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			s.Peer.SendBytes([]byte{0xff, 0xff})
			settle(s.Peer)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
		})
	})
}

func TestProviderOneMessagePerLinkPerCommit(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "one message per link per commit (W1) › three holds on one link: one state message, one part per hold, one encode each".
		t.Run("three holds on one link: one state message, one part per hold, one encode each", func(t *testing.T) {
			s := notesServer(t, nil)
			for id := range 3 {
				provHold(t, s.Peer, id, specOf(Notes), jsonObject("session", "S1"))
			}
			for range 3 {
				s.Peer.Next("bound")
			}
			meter := nexttest.Metered(t)
			store, err := s.Server.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			_, err = store.Call("add", jsonObject("text", "a"), meter.Context)
			must(t, err)
			settle(s.Peer)
			states := stateMessages(s.Peer)
			if len(states) != 1 {
				t.Fatalf("state messages = %d, want 1", len(states))
			}
			var holds []int
			for _, entry := range states[0].Holds {
				holds = append(holds, entry.Hold)
			}
			if !slices.Equal(holds, []int{0, 1, 2}) {
				t.Fatalf("state holds = %v, want [0 1 2]", holds)
			}
			counts := meter.Counts()
			if counts["chord.link.encodes"] != 3 {
				t.Fatalf("encodes = %v, want 3", counts["chord.link.encodes"])
			}
			// No validation or walk of state on the provider: only the argument checks of `add` and the store's `add`.
			if counts["chord.data.validations"] != 2 {
				t.Fatalf("validations = %v, want 2", counts["chord.data.validations"])
			}
			if walks, found := counts["chord.data.walks"]; found {
				t.Fatalf("walks = %v, want none", walks)
			}
		})

		// Ports "a derive travels with its source in the same part".
		t.Run("a derive travels with its source in the same part", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			addNote(t, s.Server, "S1", "a")
			state := s.Peer.Next("state").(*link.StateMessage)
			keys := sortedNames(editorWire(t).NamedState(t, state.Holds[0].Members))
			requireStrings(t, keys, []string{"count", "notes"})
		})

		// Ports "two links: one message each; when one closes, the other keeps updating (race 8)".
		t.Run("two links: one message each; when one closes, the other keeps updating (race 8)", func(t *testing.T) {
			s := notesServer(t, nil)
			a, b := link.MemoryLinkPair()
			must(t, s.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			other := newRawPeer(t, b, rawPeerOptions{})
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			provHold(t, other, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			other.Next("bound")
			addNote(t, s.Server, "S1", "a")
			s.Peer.Next("state")
			other.Next("state")
			s.Peer.Close()
			settle(s.Peer)
			addNote(t, s.Server, "S1", "b")
			next := other.Next("state").(*link.StateMessage)
			keys := sortedNames(editorWire(t).NamedState(t, next.Holds[0].Members))
			requireStrings(t, keys, []string{"count", "notes"})
		})
	})
}

// serveKept serves providerKept, recording each factory run and each native close.
func serveKept(host FacetHost, events *eventLog) error {
	return host.Serve(providerKept, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		id := params.Value("id").(float64)
		events.add("factory " + formatNumber(id))
		self.Open(func(context.Context) (any, error) { return nil, nil },
			func(_ any, reason NativeCloseReason, _ context.Context) error {
				events.add("close " + string(reason.Kind))
				return nil
			})
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"n": constantNumber(id)},
		}}, nil
	}, ServeOptions{})
}

func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func TestProviderRaces(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "races at the provider › a new hold and the old one's release in one turn hand the object over without ending it (R18)".
		t.Run("a new hold and the old one's release in one turn hand the object over without ending it (R18)", func(t *testing.T) {
			events := newEventLog()
			s := rawLinked(t, func(h FacetHost) error { return serveKept(h, events) }, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(providerKept), jsonObject("id", 1.0))
			s.Peer.Next("bound")
			provHold(t, s.Peer, 1, specOf(providerKept), jsonObject("id", 1.0))
			s.Peer.Send(&link.ReleaseMessage{Hold: 0})
			if bound := s.Peer.Next("bound").(*link.BoundMessage).Holds[0]; bound.Hold != 1 {
				t.Fatalf("bound hold = %d, want 1", bound.Hold)
			}
			requireStrings(t, events.all(), []string{"factory 1"})
		})

		// Ports "a hold released right after it was sent keeps no object".
		t.Run("a hold released right after it was sent keeps no object", func(t *testing.T) {
			events := newEventLog()
			s := rawLinked(t, func(h FacetHost) error { return serveKept(h, events) }, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(providerKept), jsonObject("id", 1.0))
			// Pi lets one microtask turn pass: the hold arrived and started its activation.
			events.await("factory 1")
			s.Peer.Send(&link.ReleaseMessage{Hold: 0})
			settle(s.Peer)
			// The object ended: a new hold makes a new one.
			provHold(t, s.Peer, 1, specOf(providerKept), jsonObject("id", 1.0))
			// Hold 0 may have been bound before its release arrived (its activation does not wait for a microtask turn as Pi's
			// does): only hold 1's binding counts.
			for s.Peer.Next("bound").(*link.BoundMessage).Holds[0].Hold != 1 {
			}
			var factories []string
			for _, event := range events.all() {
				if strings.HasPrefix(event, "factory") {
					factories = append(factories, event)
				}
			}
			requireStrings(t, factories, []string{"factory 1", "factory 1"})
		})

		// Ports "a hold released while it waits for load takes nothing once load resolves".
		t.Run("a hold released while it waits for load takes nothing once load resolves", func(t *testing.T) {
			events := newEventLog()
			server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
			a, b := link.MemoryLinkPair()
			must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			peer := newRawPeer(t, b, rawPeerOptions{})
			settle(peer)
			provHold(t, peer, 0, specOf(providerKept), jsonObject("id", 1.0))
			settle(peer)
			peer.Send(&link.ReleaseMessage{Hold: 0})
			settle(peer)
			must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
				return serveKept(h, events)
			})}}, background))
			settle(peer)
			requireStrings(t, events.all(), []string{})
		})

		// Ports "a release while activating unholds; bound is never sent (race 6)".
		t.Run("a release while activating unholds; bound is never sent (race 6)", func(t *testing.T) {
			Slow := remoteOneVariant("app.slow", contracts.Params{contracts.Prop("id", contracts.Int())}, contracts.VariantDeclaration{
				State: []contracts.Property{contracts.Prop("n", contracts.Int())},
			})
			aborted := newEventLog()
			started := newEventLog()
			s := rawLinked(t, func(h FacetHost) error {
				return h.Serve(Slow, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					id := formatNumber(params.Value("id").(float64))
					self.Open(func(ctx context.Context) (any, error) {
						started.add(id)
						<-ctx.Done()
						aborted.add(id)
						return nil, context.Cause(ctx)
					}, func(any, NativeCloseReason, context.Context) error { return nil })
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": constantNumber(1)},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(Slow), jsonObject("id", 1.0))
			started.await("1")
			s.Peer.Send(&link.ReleaseMessage{Hold: 0})
			aborted.await("1")
			settle(s.Peer)
			requireStrings(t, aborted.all(), []string{"1"})
			requireStrings(t, s.Peer.Kinds(), []string{"hello"})
		})

		// Ports "a hold while the previous instance of its identity tears down waits for it (race 7)".
		t.Run("a hold while the previous instance of its identity tears down waits for it (race 7)", func(t *testing.T) {
			Closing := remoteOneVariant("app.closing", contracts.Params{contracts.Prop("id", contracts.Int())}, contracts.VariantDeclaration{
				State: []contracts.Property{contracts.Prop("n", contracts.Int())},
			})
			events := newEventLog()
			finish := make(chan struct{})
			s := rawLinked(t, func(h FacetHost) error {
				return h.Serve(Closing, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					self.Open(func(context.Context) (any, error) {
						events.add("open")
						return nil, nil
					}, func(any, NativeCloseReason, context.Context) error {
						events.add("closing")
						<-finish
						events.add("closed")
						return nil
					})
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": constantNumber(params.Value("id").(float64))},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(Closing), jsonObject("id", 1.0))
			s.Peer.Next("bound")
			s.Peer.Send(&link.ReleaseMessage{Hold: 0})
			provHold(t, s.Peer, 1, specOf(Closing), jsonObject("id", 1.0))
			events.await("closing")
			settle(s.Peer)
			requireStrings(t, events.all(), []string{"open", "closing"})
			close(finish)
			s.Peer.Next("bound")
			requireStrings(t, events.all(), []string{"open", "closing", "closed", "open"})
		})

		// Ports "a commit right after the binding continues exactly from the snapshot (race 2)".
		t.Run("a commit right after the binding continues exactly from the snapshot (race 2)", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			adding := make(chan error, 1)
			go func() {
				store, err := s.Server.Get(NoteStore, jsonObject("session", "S1"), background, GetOptions{})
				if err == nil {
					_, err = store.Call("add", jsonObject("text", "a"), background)
				}
				adding <- err
			}()
			bound := s.Peer.Next("bound").(*link.BoundMessage).Holds[0]
			must(t, <-adding)
			settle(s.Peer)
			editor := editorWire(t)
			snapshot, _ := editor.NamedBound(t, bound.Members)["notes"].([]any)
			added := 0
			for _, message := range stateMessages(s.Peer) {
				notes := editor.NamedState(t, message.Holds[0].Members)["notes"]
				if ops, ok := notes.([]any); ok {
					added += len(ops)
				}
			}
			// The snapshot has the note, or one state message does: never both, never neither.
			if len(snapshot) == 0 && added == 0 {
				t.Fatal("the note reached neither the snapshot nor a state message")
			}
			if len(snapshot) > 0 && added > 0 {
				t.Fatal("the note reached both the snapshot and a state message")
			}
		})

		// Ports "closing a link during a batch drops its state message and releases its holds (race 9)".
		t.Run("closing a link during a batch drops its state message and releases its holds (race 9)", func(t *testing.T) {
			Counter := remoteOneVariant("app.counter", contracts.Params{contracts.Prop("id", contracts.Int())}, contracts.VariantDeclaration{
				State:   []contracts.Property{contracts.Prop("n", contracts.Int()), contracts.Prop("seen", contracts.Int())},
				Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod())},
			})
			N, err := documents.DefineState("n", documents.StateOptions{
				Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
				Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
			})
			must(t, err)
			// Called inside the batch, from a derive the hold follows.
			var during atomic.Pointer[func()]
			noop := func() {}
			during.Store(&noop)
			ends := newEventLog()
			s := rawLinked(t, func(h FacetHost) error {
				return h.Serve(Counter, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					id := formatNumber(params.Value("id").(float64))
					n := self.State(N).(*documents.Singleton)
					self.Open(func(context.Context) (any, error) { return nil, nil },
						func(any, NativeCloseReason, context.Context) error {
							ends.add("counter " + id + " ended")
							return nil
						})
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"n": self.Derive([]any{n}, func(values []any) (delta.JsonValue, error) {
								return values[0].(*delta.JsonObject).Value("n"), nil
							}),
							"seen": self.Derive([]any{n}, func(values []any) (delta.JsonValue, error) {
								value := values[0].(*delta.JsonObject).Value("n")
								if value.(float64) > 0 {
									(*during.Load())()
								}
								return value, nil
							}),
						},
						Methods: map[string]contracts.MethodFunction{
							"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								_, err := n.Change(func(draft *delta.Object) (any, error) {
									return nil, draft.Set("n", draft.Get("n").(float64)+1)
								}, ctx)
								return nil, err
							},
						},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			a, b := link.MemoryLinkPair()
			must(t, s.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			peer := newRawPeer(t, b, rawPeerOptions{})
			provHold(t, peer, 0, specOf(Counter), jsonObject("id", 1.0))
			peer.Next("bound")
			closeLink := func() { a.Close() }
			during.Store(&closeLink)
			provCall(t, peer, 0, 0, 0, nil)
			ends.await("counter 1 ended")
			settle(peer)
			requireStrings(t, peer.Kinds(), []string{"hello"})
			requireStrings(t, ends.all(), []string{"counter 1 ended"})
			if s.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", s.Errors.All())
			}
		})
	})
}

// Ports "local types over a link › never cross one: a hold naming one is not served (failed { waiting })".
func TestProviderLocalTypesOverALinkNeverCrossOne(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		Store := mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
		}))
		s := rawLinked(t, func(h FacetHost) error {
			return h.Serve(Store, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": constantNumber(1)},
				}}, nil
			}, ServeOptions{})
		}, rawLinkedOptions{})
		provHold(t, s.Peer, 0, specOf(Store), jsonObject())
		failed := s.Peer.Next("failed").(*link.FailedMessage)
		if failed.Cause.Kind != "waiting" {
			t.Fatalf("cause kind = %s, want waiting", failed.Cause.Kind)
		}
		requireChordWire(t, failed.Cause.Error, "unavailable", "")
	})
}

// Grid is a remote type whose `fill` sets `cells[0..n)` to a value in one commit: `n` leaf ops on distinct paths.
var Grid = remoteOneVariant("app.grid", contracts.Params{}, contracts.VariantDeclaration{
	State: []contracts.Property{
		contracts.Prop("grid", contracts.Obj(contracts.Prop("cells", contracts.Map(contracts.Int())))),
		contracts.Prop("one", contracts.Int()),
	},
	Methods: []contracts.MethodEntry{contracts.Meth("fill", contracts.NewMethod(
		contracts.Obj(contracts.Prop("n", contracts.Int()), contracts.Prop("value", contracts.Int()))))},
})

var gridDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("grid", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("cells", contracts.Map(contracts.Int()))),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("cells", jsonObject()) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

func serveGrid(host FacetHost) error {
	return host.Serve(Grid, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		grid := self.State(gridDoc).(*documents.Singleton)
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"grid": Expose(grid), "one": constantNumber(1)},
			Methods: map[string]contracts.MethodFunction{
				"fill": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					fill := args.(*delta.JsonObject)
					count, value := int(fill.Value("n").(float64)), fill.Value("value")
					_, err := grid.Change(func(draft *delta.Object) (any, error) {
						cells := draft.Object("cells")
						for cell := range count {
							if err := cells.Set("c"+strconv.Itoa(cell), value); err != nil {
								return nil, err
							}
						}
						return nil, nil
					}, ctx)
					return nil, err
				},
			},
		}}, nil
	}, ServeOptions{})
}

// slowConsumerHost is a server serving Notes and Grid and a raw peer that acks only when told, the link's context counting.
type slowConsumerHost struct {
	Server Host
	Peer   *RawPeer
	Meter  *nexttest.Meter
}

func slowConsumer(t testing.TB) *slowConsumerHost {
	t.Helper()
	meter := nexttest.Metered(t)
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if _, err := serveNotes(h, nil); err != nil {
			return err
		}
		return serveGrid(h)
	})}}, background))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, meter.Context))
	peer := newRawPeer(t, b, rawPeerOptions{NoAck: true})
	settle(peer)
	return &slowConsumerHost{Server: server, Peer: peer, Meter: meter}
}

func TestProviderWindow(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		grid := wireNames(t, Grid, "v")

		// Ports "the window at the provider › holds at most two unacknowledged bound and state messages; what waits goes out folded, encoded once".
		t.Run("holds at most two unacknowledged bound and state messages; what waits goes out folded, encoded once", func(t *testing.T) {
			s := slowConsumer(t)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			for _, text := range []string{"a", "b", "c", "d"} {
				addNote(t, s.Server, "S1", text)
			}
			settle(s.Peer)
			requireStrings(t, windowedKinds(s.Peer), []string{"state"})
			before := s.Meter.Counts()["chord.link.encodes"]
			s.Peer.Ack(link.Optional[int]{})
			settle(s.Peer)
			states := stateMessages(s.Peer)
			if len(states) < 2 {
				t.Fatalf("state messages = %d, want 2", len(states))
			}
			pending := states[1]
			// Three commits waited: one message, one entry, `count`'s three replacements folded into the last.
			nexttest.RequireJSONEqual(t, editor.NamedState(t, pending.Holds[0].Members)["count"], []any{[]any{"r", 4.0}})
			if encodes := s.Meter.Counts()["chord.link.encodes"] - before; encodes != 1 {
				t.Fatalf("encodes while folding = %v, want 1", encodes)
			}
			requireStrings(t, windowedKinds(s.Peer), []string{"state", "state"})
			addNote(t, s.Server, "S1", "e")
			settle(s.Peer)
			requireStrings(t, windowedKinds(s.Peer), []string{"state", "state"})
		})

		// Ports "ignores acks that cross nothing: they never open the window past two (race 18)".
		t.Run("ignores acks that cross nothing: they never open the window past two (race 18)", func(t *testing.T) {
			s := slowConsumer(t)
			for range 3 {
				s.Peer.Ack(link.Optional[int]{})
			}
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			for _, text := range []string{"a", "b", "c"} {
				addNote(t, s.Server, "S1", text)
			}
			settle(s.Peer)
			requireStrings(t, windowedKinds(s.Peer), []string{"state"})
		})

		// Ports "lets a hold that binds while the window is full wait: the waiting state first, its bound at the next slot".
		t.Run("lets a hold that binds while the window is full wait: the waiting state first, its bound at the next slot", func(t *testing.T) {
			s := slowConsumer(t)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			addNote(t, s.Server, "S1", "a")
			addNote(t, s.Server, "S1", "b")
			provHold(t, s.Peer, 1, specOf(Notes), jsonObject("session", "S1"))
			settle(s.Peer)
			requireStrings(t, windowedKinds(s.Peer), []string{"state"})
			s.Peer.Ack(link.Optional[int]{})
			settle(s.Peer)
			requireStrings(t, windowedKinds(s.Peer), []string{"state", "state"})
			addNote(t, s.Server, "S1", "c")
			s.Peer.Ack(link.Optional[int]{})
			settle(s.Peer)
			// Hold 1's snapshot is taken now, with every commit so far.
			var bound *link.BoundMessage
			for _, message := range s.Peer.Received() {
				if candidate, ok := message.(*link.BoundMessage); ok {
					bound = candidate
				}
			}
			if bound == nil || len(bound.Holds) != 1 || bound.Holds[0].Hold != 1 {
				t.Fatalf("bound = %+v, want one entry for hold 1", bound)
			}
			nexttest.RequireJSONEqual(t, editor.NamedBound(t, bound.Holds[0].Members)["count"], 3.0)
		})

		// Ports "overflows past 10,000 pending ops to a replacement with the current value (race 23)".
		t.Run("overflows past 10,000 pending ops to a replacement with the current value (race 23)", func(t *testing.T) {
			s := slowConsumer(t)
			provHold(t, s.Peer, 0, specOf(Grid), jsonObject())
			s.Peer.Next("bound")
			for id, value := range []float64{1, 2, 3} {
				provCall(t, s.Peer, id, 0, grid.Methods["fill"], jsonObject("n", 6000.0, "value", value))
			}
			for range 3 {
				s.Peer.Next("result")
			}
			// The first fill took the second slot; the next two overflowed the queue.
			s.Peer.Ack(link.Optional[int]{})
			settle(s.Peer)
			states := stateMessages(s.Peer)
			last := grid.NamedState(t, states[len(states)-1].Holds[0].Members)["grid"].([]any)
			first := last[0].([]any)
			if first[0] != "r" {
				t.Fatalf("first op = %v, want a replacement", first[0])
			}
			if cells := first[1].(*delta.JsonObject).Value("cells").(*delta.JsonObject); cells.Len() != 6000 {
				t.Fatalf("cells = %d, want 6000", cells.Len())
			}
		})
	})
}

func TestProviderOrigins(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		add, wait := editor.Methods["add"], editor.Methods["wait"]

		// Ports "origins at the provider › key holds and calls by (origin, id): the same ids on two origins are two holds; answers carry the origin".
		t.Run("key holds and calls by (origin, id): the same ids on two origins are two holds; answers carry the origin", func(t *testing.T) {
			s := slowConsumer(t)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S2"), 3)
			own, relayed := s.Peer.Next("bound").(*link.BoundMessage), s.Peer.Next("bound").(*link.BoundMessage)
			if own.Origin.Ok || !relayed.Origin.Ok || relayed.Origin.Value != 3 {
				t.Fatalf("origins = %+v and %+v, want none and 3", own.Origin, relayed.Origin)
			}
			provCall(t, s.Peer, 0, 0, add, jsonObject("text", "a"))
			provCall(t, s.Peer, 0, 0, add, jsonObject("text", "b"), 3)
			results := []*link.ResultMessage{s.Peer.Next("result").(*link.ResultMessage), s.Peer.Next("result").(*link.ResultMessage)}
			byOrigin := map[bool]link.Optional[int]{}
			for _, result := range results {
				if result.Call != 0 {
					t.Fatalf("result call = %d, want 0", result.Call)
				}
				byOrigin[result.Origin.Ok] = result.Origin
			}
			if len(byOrigin) != 2 || byOrigin[true].Value != 3 {
				t.Fatalf("result origins = %+v, want one without and one with origin 3", byOrigin)
			}
		})

		// Ports "keep a window per origin: a stalled origin folds its commits, another keeps receiving each".
		t.Run("keep a window per origin: a stalled origin folds its commits, another keeps receiving each", func(t *testing.T) {
			s := slowConsumer(t)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"), 1)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			for _, text := range []string{"a", "b", "c", "d"} {
				addNote(t, s.Server, "S1", text)
				// Only origin 0 acknowledges.
				s.Peer.Ack(link.Optional[int]{})
			}
			settle(s.Peer)
			countOrigin := func(origin link.Optional[int]) int {
				count := 0
				for _, state := range stateMessages(s.Peer) {
					if state.Origin == origin {
						count++
					}
				}
				return count
			}
			if got := countOrigin(link.Optional[int]{}); got != 4 {
				t.Fatalf("states of the own origin = %d, want 4", got)
			}
			// Origin 1: its bound and one state fill its window; three commits wait, folded into its next state.
			if got := countOrigin(link.Some(1)); got != 1 {
				t.Fatalf("states of origin 1 = %d, want 1", got)
			}
			s.Peer.Ack(link.Some(1))
			s.Peer.Ack(link.Some(1))
			settle(s.Peer)
			var folded *link.StateMessage
			for _, state := range stateMessages(s.Peer) {
				if state.Origin == link.Some(1) {
					folded = state
				}
			}
			nexttest.RequireJSONEqual(t, editor.NamedState(t, folded.Holds[0].Members)["count"], []any{[]any{"r", 4.0}})
		})

		// Ports "count calls per origin against N: 256 on each of two origins run, one more on one closes the link".
		t.Run("count calls per origin against N: 256 on each of two origins run, one more on one closes the link", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"), 1)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			for id := range 256 {
				provCall(t, s.Peer, id, 0, wait, nil)
				provCall(t, s.Peer, id, 0, wait, nil, 1)
			}
			settle(s.Peer)
			if s.Peer.Closed() {
				t.Fatal("the link closed within the limit")
			}
			provCall(t, s.Peer, 256, 0, wait, nil, 1)
			settle(s.Peer)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open past the limit")
			}
			requireStrings(t, protocolErrors(s.Errors), []string{"call 256: more than 256 calls"})
		})

		// Ports "ignore a call on a hold they do not know before counting it (Q1)".
		t.Run("ignore a call on a hold they do not know before counting it (Q1)", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			for id := range 256 {
				provCall(t, s.Peer, id, 0, wait, nil)
			}
			provCall(t, s.Peer, 256, 9, wait, nil)
			provCall(t, s.Peer, 257, 0, wait, nil, 4)
			settle(s.Peer)
			if s.Peer.Closed() {
				t.Fatal("the link closed")
			}
			if s.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", s.Errors.All())
			}
		})

		// Ports "closed releases the origin's holds and aborts its calls; another origin keeps its own".
		t.Run("closed releases the origin's holds and aborts its calls; another origin keeps its own", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S2"), 2)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, wait, nil, 2)
			waiting := s.Probe.AwaitWaits(t, 1)[0]
			s.Peer.Send(&link.ClosedMessage{Origin: 2})
			settle(s.Peer)
			<-waiting.Done()
			// Its ids are free: nothing answers them, a new hold of origin 2 starts over.
			provCall(t, s.Peer, 1, 0, add, jsonObject("text", "x"), 2)
			addNote(t, s.Server, "S1", "a")
			settle(s.Peer)
			for _, kind := range s.Peer.Kinds() {
				if kind == "result" || kind == "error" {
					t.Fatalf("received a %s, want no reply", kind)
				}
			}
			states := stateMessages(s.Peer)
			if len(states) != 1 || states[0].Origin.Ok {
				t.Fatalf("states = %+v, want one of the own origin", states)
			}
		})

		// Ports "carry constants and failed members, and an empty variant's binding, in a first bound (race 24)".
		t.Run("carry constants and failed members, and an empty variant's binding, in a first bound (race 24)", func(t *testing.T) {
			Empty := remoteOneVariant("app.empty", contracts.Params{}, contracts.VariantDeclaration{})
			N, err := documents.DefineState("n", documents.StateOptions{
				Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
				Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
			})
			must(t, err)
			halves := wireNames(t, Halves, "v")
			s := rawLinked(t, func(h FacetHost) error {
				if err := h.Serve(Empty, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Variant: "v"}, nil
				}, ServeOptions{}); err != nil {
					return err
				}
				return h.Serve(Halves, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					n := self.State(N).(*documents.Singleton)
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"one": constantNumber(1),
							"half": self.Derive([]any{n}, func(values []any) (delta.JsonValue, error) {
								value := values[0].(*delta.JsonObject).Value("n").(float64)
								if value == 2 {
									return nil, errors.New("half threw")
								}
								return value / 2, nil
							}),
						},
						Methods: map[string]contracts.MethodFunction{
							"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								_, err := n.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", args) }, ctx)
								return nil, err
							},
						},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(Halves), jsonObject())
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, halves.Methods["set"], 2.0)
			s.Peer.Next("result")
			provHold(t, s.Peer, 1, specOf(Empty), jsonObject())
			provHold(t, s.Peer, 2, specOf(Halves), jsonObject())
			bounds := []*link.BoundMessage{s.Peer.Next("bound").(*link.BoundMessage), s.Peer.Next("bound").(*link.BoundMessage)}
			find := func(id int) link.BoundEntry {
				for _, bound := range bounds {
					if bound.Holds[0].Hold == id {
						return bound.Holds[0]
					}
				}
				t.Fatalf("no bound for hold %d", id)
				return link.BoundEntry{}
			}
			empty, withHalves := find(1), find(2)
			if empty.Variant != 0 || len(empty.Members) != 0 || len(empty.Failed) != 0 {
				t.Fatalf("empty binding = %+v, want variant 0 with no members", empty)
			}
			if withHalves.Variant != 0 || len(withHalves.Members) != 1 || withHalves.Members[0].Member != halves.State["one"] {
				t.Fatalf("halves members = %+v, want only `one`", withHalves.Members)
			}
			if len(withHalves.Failed) != 1 || withHalves.Failed[0].Member != halves.State["half"] {
				t.Fatalf("halves failed = %+v, want `half`", withHalves.Failed)
			}
			nexttest.RequireJSONEqual(t, withHalves.Failed[0].Error, jsonObject("kind", "plugin", "code", "internal", "message", "half threw"))
			nexttest.RequireJSONEqual(t, halves.NamedBound(t, withHalves.Members), map[string]any{"one": 1.0})
		})
	})
}

// Halves is a constant, and a member that fails once `set(2)`.
var Halves = remoteOneVariant("app.halves", contracts.Params{}, contracts.VariantDeclaration{
	State:   []contracts.Property{contracts.Prop("one", contracts.Int()), contracts.Prop("half", contracts.Int())},
	Methods: []contracts.MethodEntry{contracts.Meth("set", contracts.NewMethod(contracts.Int()))},
})

func TestProviderCallLimit(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		wait := editorWire(t).Methods["wait"]

		// Ports "the call limit › closes a link whose peer sends more calls than this side announced".
		t.Run("closes a link whose peer sends more calls than this side announced", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			for id := range 257 {
				provCall(t, s.Peer, id, 0, wait, nil)
			}
			settle(s.Peer)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
			requireStrings(t, protocolErrors(s.Errors), []string{"call 256: more than 256 calls"})
		})

		// Ports "frees a slot per reply, cancel and release".
		t.Run("frees a slot per reply, cancel and release", func(t *testing.T) {
			s := notesServer(t, nil)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			for id := range 256 {
				provCall(t, s.Peer, id, 0, wait, nil)
			}
			s.Peer.Send(&link.AbortMessage{Call: 0})
			provCall(t, s.Peer, 256, 0, wait, nil)
			s.Peer.Send(&link.ReleaseMessage{Hold: 0})
			provHold(t, s.Peer, 1, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			for id := 300; id < 556; id++ {
				provCall(t, s.Peer, id, 1, wait, nil)
			}
			settle(s.Peer)
			if s.Peer.Closed() {
				t.Fatal("the link closed")
			}
			if s.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", s.Errors.All())
			}
		})
	})
}

// limitedHost is a server serving Notes behind a raw consumer whose link has limits, compressing with compression; the
// raw peer is its child, or its parent (only a parent may send compressed arguments).
func limitedHost(t testing.TB, limits link.PartLimits, compression link.Compression, parent bool) *rawLinkedHost {
	t.Helper()
	return rawLinked(t, func(h FacetHost) error {
		_, err := serveNotes(h, nil)
		return err
	}, rawLinkedOptions{Limits: limits, Compression: compression, Parent: parent})
}

// deflatedText is deflated JSON of `{ text }` with length characters.
func deflatedText(length int) *link.DeflatedPart {
	return link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte(`{"text":"` + strings.Repeat("x", length) + `"}`)))
}

// notesHold is a hold of Notes for S1 as a message.
func notesHold(t testing.TB, id int) *link.HoldMessage {
	t.Helper()
	spec := specOf(Notes)
	return &link.HoldMessage{
		Hold: id, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
		Params: mustJSONPart(t, jsonObject("session", "S1")), Address: serverWireAddress,
	}
}

func TestProviderLimitsPerPart(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		add := editorWire(t).Methods["add"]

		// Ports "limits per part at the provider › fails a call whose argument part is past the wire limit with too_large/part; the link stays".
		t.Run("fails a call whose argument part is past the wire limit with too_large/part; the link stays", func(t *testing.T) {
			s := limitedHost(t, link.PartLimits{Part: new(100.0)}, nil, false)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, add, jsonObject("text", strings.Repeat("x", 200)))
			refused := s.Peer.Next("error").(*link.ErrorMessage)
			if refused.Call != 0 {
				t.Fatalf("error call = %d, want 0", refused.Call)
			}
			requireChordWire(t, refused.Error, "too_large", "part")
			provCall(t, s.Peer, 1, 0, add, jsonObject("text", "fits"))
			nexttest.RequireJSONEqual(t, partValue(t, s.Peer.Next("result").(*link.ResultMessage).Value), 1.0)
			if s.Peer.Closed() {
				t.Fatal("the link closed")
			}
		})

		// Ports "fails a hold whose params are past the limit broken too_large/part".
		t.Run("fails a hold whose params are past the limit broken too_large/part", func(t *testing.T) {
			s := limitedHost(t, link.PartLimits{Part: new(30.0)}, nil, false)
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", strings.Repeat("s", 40)))
			failed := s.Peer.Next("failed").(*link.FailedMessage)
			if failed.Hold != 0 || failed.Cause.Kind != "broken" {
				t.Fatalf("failed = %+v, want hold 0, broken", failed)
			}
			requireChordWire(t, failed.Cause.Error, "too_large", "part")
		})

		// Ports "fails a call whose deflated argument inflates past the decoded limit, on both inflate paths".
		t.Run("fails a call whose deflated argument inflates past the decoded limit, on both inflate paths", func(t *testing.T) {
			for _, compression := range []link.Compression{nextnode.NodeCompression, nil} {
				s := limitedHost(t, link.PartLimits{Decoded: new(10_000.0)}, compression, true)
				provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
				s.Peer.Next("bound")
				s.Peer.Send(&link.CallMessage{Call: 0, Hold: 0, Method: add, Args: deflatedText(50_000)})
				requireChordWire(t, s.Peer.Next("error").(*link.ErrorMessage).Error, "too_large", "")
				provCall(t, s.Peer, 1, 0, add, jsonObject("text", "fits"))
				if call := s.Peer.Next("result").(*link.ResultMessage).Call; call != 1 {
					t.Fatalf("result call = %d, want 1", call)
				}
			}
		})

		// Ports "inflates a deflated argument within the hold's stamped decoded limit, a bomb included (both paths)".
		// Go form: the byte count of Pi's patched DecompressionStream covers the synchronous compression only (see
		// countingCompression); the asynchronous path's bound is link.InflateAsync's own, tested in package link.
		t.Run("inflates a deflated argument within the hold's stamped decoded limit, a bomb included (both paths)", func(t *testing.T) {
			bomb := link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte(`"` + strings.Repeat("x", 64<<20) + `"`)))
			counting := newCountingCompression()
			for _, compression := range []link.Compression{counting, nil} {
				// The stamped limit is smaller than the link's own: it bounds the inflate.
				s := limitedHost(t, link.PartLimits{}, compression, true)
				message := notesHold(t, 0)
				message.Limit = link.Some(1000)
				s.Peer.Send(message)
				s.Peer.Next("bound")
				s.Peer.Send(&link.CallMessage{Call: 0, Hold: 0, Method: add, Args: bomb})
				requireChordWire(t, s.Peer.Next("error").(*link.ErrorMessage).Error, "too_large", "")
				s.Peer.Send(&link.CallMessage{Call: 1, Hold: 0, Method: add, Args: deflatedText(500)})
				if call := s.Peer.Next("result").(*link.ResultMessage).Call; call != 1 {
					t.Fatalf("result call = %d, want 1", call)
				}
			}
			// The inflate stopped within a chunk of the stamped limit, never near the bomb's 64 MiB.
			if counting.Bytes() >= 1024*1024 {
				t.Fatalf("inflated %d bytes, want under 1 MiB", counting.Bytes())
			}
		})

		// Ports "checks a deflated argument's wire size before inflating it (both paths)".
		t.Run("checks a deflated argument's wire size before inflating it (both paths)", func(t *testing.T) {
			for _, compression := range []link.Compression{nextnode.NodeCompression, nil} {
				s := limitedHost(t, link.PartLimits{Part: new(18.0)}, compression, true)
				provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
				s.Peer.Next("bound")
				// Corrupt data past the wire limit: refused by its size, never inflated (no protocol error).
				s.Peer.Send(&link.CallMessage{Call: 0, Hold: 0, Method: add, Args: link.NewDeflatedPart(make([]byte, 20))})
				requireChordWire(t, s.Peer.Next("error").(*link.ErrorMessage).Error, "too_large", "")
				if s.Peer.Closed() {
					t.Fatal("the link closed")
				}
				if s.Errors.Len() != 0 {
					t.Fatalf("errors = %v, want none", s.Errors.All())
				}
			}
		})

		// Ports "inflates nothing for a call it ignores: an unknown hold (Q1), a refused one (both paths)".
		t.Run("inflates nothing for a call it ignores: an unknown hold (Q1), a refused one (both paths)", func(t *testing.T) {
			for _, compression := range []link.Compression{nextnode.NodeCompression, nil} {
				s := limitedHost(t, link.PartLimits{}, compression, true)
				corrupt := func() *link.DeflatedPart { return link.NewDeflatedPart([]byte{255, 255, 255}) }
				s.Peer.Send(&link.CallMessage{Call: 0, Hold: 9, Method: add, Args: corrupt()})
				refusedHold := notesHold(t, 0)
				refusedHold.Type = "app.kept"
				s.Peer.Send(refusedHold)
				s.Peer.Next("failed")
				s.Peer.Send(&link.CallMessage{Call: 1, Hold: 0, Method: 0, Args: corrupt()})
				settle(s.Peer)
				if s.Peer.Closed() {
					t.Fatal("the link closed")
				}
				if s.Errors.Len() != 0 {
					t.Fatalf("errors = %v, want none", s.Errors.All())
				}
			}
		})

		// Ports "refuses compressed arguments from a child, and compressed params on any link: protocol errors".
		t.Run("refuses compressed arguments from a child, and compressed params on any link: protocol errors", func(t *testing.T) {
			child := limitedHost(t, link.PartLimits{}, nil, false)
			provHold(t, child.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			child.Peer.Next("bound")
			child.Peer.Send(&link.CallMessage{Call: 0, Hold: 0, Method: add, Args: deflatedText(10)})
			settle(child.Peer)
			if !child.Peer.Closed() {
				t.Fatal("the child's link stayed open")
			}
			if got := protocolErrors(child.Errors); len(got) != 1 || strings.HasPrefix(got[0], "unexpected") {
				t.Fatalf("child errors = %q, want one protocol error", got)
			}
			parent := limitedHost(t, link.PartLimits{}, nil, true)
			message := notesHold(t, 0)
			message.Params = link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte("{}")))
			parent.Peer.Send(message)
			settle(parent.Peer)
			if !parent.Peer.Closed() {
				t.Fatal("the parent's link stayed open")
			}
			if got := protocolErrors(parent.Errors); len(got) != 1 || strings.HasPrefix(got[0], "unexpected") {
				t.Fatalf("parent errors = %q, want one protocol error", got)
			}
		})

		// Ports "honours a stamped decoded limit only on a parent link: a child's is ignored (race 12)".
		t.Run("honours a stamped decoded limit only on a parent link: a child's is ignored (race 12)", func(t *testing.T) {
			s := limitedHost(t, link.PartLimits{}, nil, false)
			message := notesHold(t, 0)
			message.Limit = link.Some(10)
			s.Peer.Send(message)
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, add, jsonObject("text", "longer than ten bytes"))
			if call := s.Peer.Next("result").(*link.ResultMessage).Call; call != 0 {
				t.Fatalf("result call = %d, want 0", call)
			}
		})
	})
}

// servedHoldsOf are the holds served on origin of a host's links now, weakly.
func servedHoldsOf(host Host, origin int) []weak.Pointer[hold] {
	var refs []weak.Pointer[hold]
	h := host.(*chordHost)
	h.loop().Run(func() {
		for _, each := range h.links {
			side, found := each.providers.get(origin)
			if !found {
				continue
			}
			for _, entry := range side.entries.values() {
				if served, ok := entry.(*hold); ok {
					refs = append(refs, weak.Make(served))
				}
			}
		}
	})
	return refs
}

// Ports provider.test.ts "GC of the window › a closed origin drops its pending queue's ops: nothing keeps them". Pi
// patches PendingOps.prototype.push to watch the ops it queues; Go has no such hook, so the holds that own the pending
// queues (and the ops in them) are watched instead: a closed origin must leave nothing of them.
func TestProviderGCOfTheWindowAClosedOriginDropsItsPendingQueuesOps(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		s := slowConsumer(t)
		provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"), 1)
		s.Peer.Next("bound")
		for _, text := range []string{"a", "b", "c"} {
			addNote(t, s.Server, "S1", text)
		}
		refs := servedHoldsOf(s.Server, 1)
		if len(refs) != 1 {
			t.Fatalf("served holds of origin 1 = %d, want 1", len(refs))
		}
		s.Peer.Send(&link.ClosedMessage{Origin: 1})
		settle(s.Peer)
		nexttest.ExpectCollected(t, refs...)
		if s.Peer.Closed() {
			t.Fatal("the link closed")
		}
	})
}
