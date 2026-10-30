package host

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports packages/chord/test/next/remote/consumer.test.ts: the consumer side of a link, against a server host or a raw
// provider peer.

// gotReference is what a Get that runs on a goroutine of its own returned.
type gotReference struct {
	ref *Reference
	err error
}

// asyncGet is `host.get(...)` started and not awaited: its result arrives on the channel.
func asyncGet(host Host, typ *contracts.Contract, params any, ctx context.Context, options GetOptions) <-chan gotReference {
	result := make(chan gotReference, 1)
	go func() {
		ref, err := host.Get(typ, params, ctx, options)
		result <- gotReference{ref, err}
	}()
	return result
}

// await is `await got`: the reference, the test failing on a rejection.
func awaitReference(t testing.TB, got <-chan gotReference) *Reference {
	t.Helper()
	result := <-got
	must(t, result.err)
	return result.ref
}

// startCall is `reference.method(args, ctx)`: the call starts in the caller's section, as a Promise-returning call does,
// and the result is awaited later (result.wait()).
func startCall(t testing.TB, ref *Reference, method string, args delta.JsonValue, ctx context.Context) *parkedCallResult {
	t.Helper()
	var result *parkedCallResult
	var err error
	ref.binding.loop.Run(func() { result, err = ref.binding.call(method, args, ctx) })
	must(t, err)
	return result
}

// chordOf is the ChordError err is; the test fails otherwise.
func chordOf(t testing.TB, err error) *data.ChordError {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("error = %v (%T), want a ChordError", err, err)
	}
	return chordErr
}

func requireChordError(t testing.TB, err error, code data.ChordErrorCode, reason data.ChordErrorReason) *data.ChordError {
	t.Helper()
	chordErr := chordOf(t, err)
	if chordErr.Code != code || (reason != "" && chordErr.Reason != reason) {
		t.Fatalf("error = %s/%s %q, want %s/%s", chordErr.Code, chordErr.Reason, chordErr.Message, code, reason)
	}
	return chordErr
}

// memberValue is a reference member's value; the test fails on its error.
func memberValue(t testing.TB, ref *Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(member).Value()
	must(t, err)
	return value
}

func refStateKind(ref *Reference) RefStateKind { return ref.State().Value().Kind }

// settleLinked is `await transported()` over the links of `linked`.
func settleLinked(l *linkedHosts) { transported(l.Links[0], l.Links[1]) }

func requireKinds(t testing.TB, peer *RawPeer, want ...string) {
	t.Helper()
	requireStrings(t, append([]string{}, peer.Kinds()...), append([]string{}, want...))
}

func TestConsumerReferencesOverALink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports consumer.test.ts "references over a link › are live and hydrated when got, with the variant of the link's principal".
		t.Run("are live and hydrated when got, with the variant of the link's principal", func(t *testing.T) {
			l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{Child: guestPrincipal("S1")})
			addNote(t, l.Server, "S1", "a")
			notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			if variant, _, _ := notes.Variant(); variant != "reader" {
				t.Fatalf("variant = %q, want reader", variant)
			}
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "notes"), []any{jsonObject("text", "a")})
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 1.0)
			watch, err := notes.Member("notes").Watch(background)
			must(t, err)
			frames := nexttest.FramesOf(watch)
			addNote(t, l.Server, "S1", "b")
			settleLinked(l)
			nexttest.Delivered()
			got := frames.Items()
			if len(got) != 1 {
				t.Fatalf("frames = %d, want 1", len(got))
			}
			nexttest.RequireJSONEqual(t, got[0].Value, []any{jsonObject("text", "a"), jsonObject("text", "b")})
			nexttest.RequireJSONEqual(t, jsonOps(got[0].Ops), []any{[]any{"p", []any{}, 1.0, 0.0, []any{jsonObject("text", "b")}}})
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 2.0)
		})

		// Ports "apply one state message as one batch: a derive over two members never sees half a commit".
		t.Run("apply one state message as one batch: a derive over two members never sees half a commit", func(t *testing.T) {
			View := mustContract(contracts.Local("app.view", contracts.LocalDeclaration{
				Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("pair", contracts.Str())},
			}))
			l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
			seen := newEventLog()
			client := newHost(t, HostOptions{OnError: func(HostError) {}})
			// Connected first (W6): the view's factory references a server type.
			a, b := link.MemoryLinkPair()
			l.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background)
			client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background)
			must(t, client.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
				return h.Serve(View, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					notes := self.Get(Notes, jsonObject("session", "S1"))
					return contracts.Implementation{Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"pair": self.Derive([]any{notes.Member("notes"), notes.Member("count")}, func(values []any) (delta.JsonValue, error) {
								pair := formatNumber(float64(len(values[0].([]any)))) + "/" + formatNumber(values[1].(float64))
								seen.add(pair)
								return pair, nil
							}),
						},
					}}, nil
				}, ServeOptions{})
			})}}, background))
			view, err := client.Get(View, jsonObject(), background, GetOptions{})
			must(t, err)
			watch, err := view.Member("pair").Watch(background)
			must(t, err)
			frames := nexttest.FramesOf(watch)
			addNote(t, l.Server, "S1", "a")
			addNote(t, l.Server, "S1", "b")
			transported(a, b)
			nexttest.Delivered()
			var values []any
			for _, frame := range frames.Items() {
				values = append(values, frame.Value)
			}
			nexttest.RequireJSONEqual(t, values, []any{"1/1", "2/2"})
			for _, pair := range seen.all() {
				if pair[:1] != pair[2:] {
					t.Fatalf("a derive saw half a commit: %s", pair)
				}
			}
		})

		// Ports "call methods: results, ChordErrors, PluginErrors and other throws".
		t.Run("call methods: results, ChordErrors, PluginErrors and other throws", func(t *testing.T) {
			l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
			notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			if variant, _, _ := notes.Variant(); variant != "editor" {
				t.Fatalf("variant = %q, want the editor", variant)
			}
			added, err := notes.Call("add", jsonObject("text", "x"), background)
			must(t, err)
			nexttest.RequireJSONEqual(t, added, 1.0)
			_, err = notes.Call("fail", jsonObject("how", "chord"), background)
			if chordErr := requireChordError(t, err, data.CodeForbidden, ""); chordErr.Message != "not yours" {
				t.Fatalf("message = %q, want not yours", chordErr.Message)
			}
			_, err = notes.Call("fail", jsonObject("how", "plugin"), background)
			plugin, ok := err.(*data.PluginError) //nolint:errorlint // upstream's instanceof
			if !ok || plugin.Code != "quota" || plugin.Message != "too many" {
				t.Fatalf("error = %v (%T), want the quota PluginError", err, err)
			}
			nexttest.RequireJSONEqual(t, plugin.Details, jsonObject("limit", 1.0))
			_, err = notes.Call("fail", jsonObject("how", "plain"), background)
			if plain, ok := err.(*data.PluginError); !ok || plain.Code != "internal" || plain.Message != "plain" { //nolint:errorlint // upstream's instanceof
				t.Fatalf("error = %v (%T), want the internal PluginError", err, err)
			}
			_, err = notes.Call("add", jsonObject("text", 1.0), background)
			requireChordError(t, err, data.CodeInvalid, data.ReasonArgs)
		})

		// Ports "carry bytes as raw segments both ways".
		t.Run("carry bytes as raw segments both ways", func(t *testing.T) {
			l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
			notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			echoed, err := notes.Call("echo", jsonObject("data", []byte{1, 2, 3}), background)
			must(t, err)
			bytes, ok := echoed.([]byte)
			if !ok || !slices.Equal(bytes, []byte{1, 2, 3}) {
				t.Fatalf("echoed = %v (%T), want the bytes 1 2 3", echoed, echoed)
			}
		})

		// Ports "cancel a call: the caller is rejected at once, the provider's handler aborted".
		t.Run("cancel a call: the caller is rejected at once, the provider's handler aborted", func(t *testing.T) {
			var probe *notesProbe
			l := linked(t, func(h FacetHost) error {
				var err error
				probe, err = serveNotes(h, nil)
				return err
			}, linkedOptions{})
			notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			cancellable, cancel := chordctx.WithCancel(background)
			call := startCall(t, notes, "wait", nil, cancellable)
			waited := probe.AwaitWaits(t, 1)[0]
			cancel(errors.New("user cancelled"))
			if _, err := call.wait(); err == nil || err.Error() != "user cancelled" {
				t.Fatalf("call error = %v, want user cancelled", err)
			}
			settleLinked(l)
			<-waited.Done()
		})

		// Ports "release: pending calls reject released, the provider aborts their handlers and lets the object go".
		t.Run("release: pending calls reject released, the provider aborts their handlers and lets the object go", func(t *testing.T) {
			var probe *notesProbe
			l := linked(t, func(h FacetHost) error {
				var err error
				probe, err = serveNotes(h, nil)
				return err
			}, linkedOptions{})
			notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			watch, err := notes.Member("notes").Watch(background)
			must(t, err)
			call := startCall(t, notes, "wait", nil, background)
			waited := probe.AwaitWaits(t, 1)[0]
			notes.Release()
			_, err = call.wait()
			if chordErr := requireChordError(t, err, data.CodeReleased, ""); chordErr.Message != "app.notes(S1): released" {
				t.Fatalf("message = %q, want app.notes(S1): released", chordErr.Message)
			}
			<-watch.Closed()
			if end := watch.End(); end.Kind != chord.WatchEnded {
				t.Fatalf("watch end = %+v, want ended", end)
			}
			settleLinked(l)
			<-waited.Done()
			// The object ended with its last reference: a new get runs the factory again.
			_, err = l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			requireStrings(t, probe.Factories(), []string{"S1 owner", "S1 owner"})
		})

		// Ports "a closing link rejects sent calls unavailable; references go loading, gets keep waiting".
		t.Run("a closing link rejects sent calls unavailable; references go loading, gets keep waiting", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			got := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{})
			bindNext(t, p.Peer)
			notes := awaitReference(t, got)
			call := startCall(t, notes, "wait", nil, background)
			pending := asyncGet(p.Client, Notes, jsonObject("session", "S2"), background, GetOptions{ReadyTimeout: new(50 * time.Millisecond)})
			p.Peer.Next("call")
			p.Peer.Close()
			_, err := call.wait()
			if chordErr := requireChordError(t, err, data.CodeUnavailable, ""); chordErr.Message != "the link closed" {
				t.Fatalf("message = %q, want the link closed", chordErr.Message)
			}
			if kind := refStateKind(notes); kind != RefStateLoading {
				t.Fatalf("state = %s, want loading", kind)
			}
			result := <-pending
			if chordErr := requireChordError(t, result.err, data.CodeUnavailable, ""); chordErr.Message != "app.notes(S2): did not become live within 50 ms" {
				t.Fatalf("message = %q, want the ready timeout", chordErr.Message)
			}
		})
	})
}

// boundMembers is `[notes, count]` of Notes's editor variant as members of a bound entry, the notes' JSON given as a part.
func boundMembers(t testing.TB, notes link.Part, count link.Part) []link.BoundMember {
	t.Helper()
	editor := editorWire(t)
	return []link.BoundMember{{Member: editor.State["notes"], Value: notes}, {Member: editor.State["count"], Value: count}}
}

// stateEntry is a state entry of hold with members and failed members.
func stateEntry(hold int, members []link.StateMember, failed []link.FailedMember) link.StateEntry {
	if failed == nil {
		failed = []link.FailedMember{}
	}
	return link.StateEntry{Hold: hold, Members: members, Failed: failed, Retired: []int{}}
}

func TestConsumerRaces(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)

		// Ports "races at the consumer › ignores a bound and a state for a hold it released, and a result crossing its cancel".
		t.Run("ignores a bound and a state for a hold it released, and a result crossing its cancel", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			late := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{ReadyTimeout: new(10 * time.Millisecond)})
			hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
			if result := <-late; result.err == nil || !containsText(result.err.Error(), "did not become live") {
				t.Fatalf("late get error = %v, want did not become live", result.err)
			}
			p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
				Hold: hold, Variant: editor.Variant, Members: boundMembers(t, link.Text("[]"), link.Text("0")), Failed: []link.FailedMember{},
			}}})
			p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{stateEntry(hold,
				[]link.StateMember{{Member: editor.State["count"], Ops: mustJSONPart(t, []any{[]any{"r", 1.0}})}}, nil)}})
			got := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{})
			bindNext(t, p.Peer)
			notes := awaitReference(t, got)
			cancellable, cancel := chordctx.WithCancel(background)
			call := startCall(t, notes, "wait", nil, cancellable)
			id := p.Peer.Next("call").(*link.CallMessage).Call
			cancel(errors.New("cancelled"))
			if _, err := call.wait(); err == nil || err.Error() != "cancelled" {
				t.Fatalf("call error = %v, want cancelled", err)
			}
			p.Peer.Next("abort")
			p.Peer.Send(&link.ResultMessage{Call: id})
			settle(p.Peer)
			if p.Peer.Closed() {
				t.Fatal("the link closed")
			}
			if p.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", p.Errors.All())
			}
		})
	})
}

func containsText(text, part string) bool {
	for index := 0; index+len(part) <= len(text); index++ {
		if text[index:index+len(part)] == part {
			return true
		}
	}
	return false
}

func TestConsumerProtocolErrors(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		count := editor.State["count"]
		stateCases := []struct {
			name    string
			members []link.StateMember
		}{
			{"wire ops failing Delta's checks", []link.StateMember{{Member: count, Ops: link.Text(`[["x"]]`)}}},
			{"a member index outside the variant", []link.StateMember{{Member: 9, Ops: link.Text(`[["r",1]]`)}}},
			{"a member twice", []link.StateMember{{Member: count, Ops: link.Text(`[["r",1]]`)}, {Member: count, Ops: link.Text(`[["r",2]]`)}}},
			{"a member's changes that are no list", []link.StateMember{{Member: count, Ops: link.Text("1")}}},
			{"ops that are no JSON", []link.StateMember{{Member: count, Ops: link.Text("[")}}},
			{"ops that do not apply to the member", []link.StateMember{{Member: count, Ops: link.Text(`[["s",["x"],1]]`)}}},
		}
		// Ports "protocol errors at the consumer › close the link: %s" (it.each over six cases).
		for _, each := range stateCases {
			t.Run("close the link: "+each.name, func(t *testing.T) {
				p := rawProvider(t, rawLinkedOptions{})
				got := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{})
				hold := bindNext(t, p.Peer)
				notes := awaitReference(t, got)
				p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{stateEntry(hold, each.members, nil)}})
				settle(p.Peer)
				if !p.Peer.Closed() {
					t.Fatal("the link stayed open")
				}
				equalHostErrorKinds(t, p.Errors, ErrProtocolError)
				// Nothing of the message was applied; the reference waits for the next link.
				if kind := refStateKind(notes); kind != RefStateLoading {
					t.Fatalf("state = %s, want loading", kind)
				}
				nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 0.0)
			})
		}

		replies := []struct {
			name  string
			reply link.Message
		}{
			{"a result that is no JSON", &link.ResultMessage{Call: 0, Value: link.Text("{")}},
			{"a $bytes reference without its segment", &link.ResultMessage{Call: 0, Value: link.Text(`{"$bytes":0}`), Segments: [][]byte{}}},
			{"a malformed ChordError envelope", &link.ErrorMessage{Call: 0, Error: jsonObject("kind", "chord", "error", jsonObject("code", 1.0))}},
		}
		// Ports "reject the call and close the link on a malformed reply: %s" (it.each over three cases).
		for _, each := range replies {
			t.Run("reject the call and close the link on a malformed reply: "+each.name, func(t *testing.T) {
				p := rawProvider(t, rawLinkedOptions{})
				got := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{})
				bindNext(t, p.Peer)
				notes := awaitReference(t, got)
				call := startCall(t, notes, "wait", nil, background)
				p.Peer.Next("call")
				p.Peer.Send(each.reply)
				// The text codec cannot even decode a result that is no JSON: the link closes and the call with it.
				_, err := call.wait()
				requireChordError(t, err, data.CodeUnavailable, "")
				settle(p.Peer)
				if !p.Peer.Closed() {
					t.Fatal("the link stayed open")
				}
				equalHostErrorKinds(t, p.Errors, ErrProtocolError)
			})
		}

		// Ports "ignore a gone for a hold it does not know before reading its error (Q1)".
		t.Run("ignore a gone for a hold it does not know before reading its error (Q1)", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			got := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{})
			bindNext(t, p.Peer)
			awaitReference(t, got)
			p.Peer.Send(&link.GoneMessage{Hold: 9, Error: jsonObject("kind", "chord", "error", jsonObject("code", "nope"))})
			settle(p.Peer)
			if p.Peer.Closed() {
				t.Fatal("the link closed")
			}
			if p.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", p.Errors.All())
			}
		})

		// Ports "close the link on a malformed error envelope".
		t.Run("close the link on a malformed error envelope", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
			p.Peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{
				Kind: "broken", Error: jsonObject("kind", "chord", "error", jsonObject("code", "nope", "message", "m")),
			}})
			settle(p.Peer)
			if !p.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
			if kind := refStateKind(notes); kind != RefStateLoading {
				t.Fatalf("state = %s, want loading", kind)
			}
		})
	})
}

func equalHostErrorKinds(t testing.TB, errs *hostErrors, want ...HostErrorKind) {
	t.Helper()
	var got []HostErrorKind
	for _, err := range errs.All() {
		got = append(got, err.Kind)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("host error kinds = %v, want %v", got, want)
	}
}

func TestConsumerWireFormatOfAHold(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "the wire format of a hold › is the snapshot by member, then each commit's changed members with their ops".
		t.Run("is the snapshot by member, then each commit's changed members with their ops", func(t *testing.T) {
			l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
			a, b := link.MemoryLinkPair()
			l.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background)
			peer := newRawPeer(t, b, rawPeerOptions{})
			spec := specOf(Notes)
			peer.Send(&link.HoldMessage{
				Hold: 7, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
				Params: mustJSONPart(t, jsonObject("session", "S1")), Address: serverWireAddress,
			})
			editor := editorWire(t)
			bound := peer.Next("bound").(*link.BoundMessage).Holds[0]
			if bound.Variant != editor.Variant {
				t.Fatalf("variant = %d, want %d", bound.Variant, editor.Variant)
			}
			nexttest.RequireJSONEqual(t, editor.NamedBound(t, bound.Members), map[string]any{"notes": []any{}, "count": 0.0})
			addNote(t, l.Server, "S1", "a")
			state := peer.Next("state").(*link.StateMessage)
			if len(state.Holds) != 1 || state.Holds[0].Hold != 7 || len(state.Holds[0].Failed) != 0 {
				t.Fatalf("state holds = %+v, want hold 7 with nothing failed", state.Holds)
			}
			nexttest.RequireJSONEqual(t, editor.NamedState(t, state.Holds[0].Members), map[string]any{
				"notes": []any{[]any{"p", []any{}, 0.0, 0.0, []any{jsonObject("text", "a")}}},
				"count": []any{[]any{"r", 1.0}},
			})
		})
	})
}

func TestConsumerCostGuards(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "cost guards at the consumer › applies state without validating, walking or copying it: unchanged parts stay the same objects".
		t.Run("applies state without validating, walking or copying it: unchanged parts stay the same objects", func(t *testing.T) {
			meter := nexttest.Metered(t)
			l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
			client := newHost(t, HostOptions{OnError: func(HostError) {}})
			must(t, client.Load(nil, background))
			a, b := link.MemoryLinkPair()
			l.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background)
			client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, meter.Context)
			addNote(t, l.Server, "S1", "a")
			notes, err := client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			first := memberValue(t, notes, "notes").([]any)[0]
			addNote(t, l.Server, "S1", "b")
			transported(a, b)
			list := memberValue(t, notes, "notes").([]any)
			nexttest.RequireJSONEqual(t, list, []any{jsonObject("text", "a"), jsonObject("text", "b")})
			if list[0] != first {
				t.Fatal("the unchanged note was copied")
			}
			counts := meter.Counts()
			if validations, found := counts["chord.data.validations"]; found {
				t.Fatalf("validations = %v, want none", validations)
			}
			if walks, found := counts["chord.data.walks"]; found {
				t.Fatalf("walks = %v, want none", walks)
			}
		})
	})
}

func TestConsumerGC(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "GC at the consumer › a closed link retained by the test, with its host, keeps no hold, replica or released reference".
		t.Run("a closed link retained by the test, with its host, keeps no hold, replica or released reference", func(t *testing.T) {
			var (
				notesRef weak.Pointer[Reference]
				replicaP weak.Pointer[replica]
				objectP  weak.Pointer[remoteObject]
			)
			p := rawProvider(t, rawLinkedOptions{})
			func() {
				got := asyncGet(p.Client, Notes, jsonObject("session", "S1"), background, GetOptions{})
				bindNext(t, p.Peer)
				notes := awaitReference(t, got)
				notesRef = weak.Make(notes)
				p.Client.(*chordHost).loop().Run(func() {
					object := notes.binding.state.target.(*remoteObject)
					objectP = weak.Make(object)
					replicaP = weak.Make(object.bound.replicas.byName["notes"])
				})
				p.Peer.Close()
				settle(p.Peer)
				// Across the drop the host keeps the reference's object for the next link; released, nothing stays.
				notes.Release()
			}()
			nexttest.ExpectCollected(t, notesRef)
			nexttest.ExpectCollected(t, replicaP)
			nexttest.ExpectCollected(t, objectP)
			// The host and the raw peer are retained until here.
			_ = p.Client
		})
	})
}

// pairHost is a client whose `Pair` derives over the counts of two holds (S1, S2), both bound by a raw provider.
type pairHost struct {
	Client Host
	Peer   *RawPeer
	Seen   *eventLog
	Frames *nexttestFrames
	Errors *hostErrors
	View   *Reference
}

type nexttestFrames = nexttest.Collector[nexttest.Frame[delta.JsonValue]]

func pairOf(t testing.TB) *pairHost {
	t.Helper()
	Pair := mustContract(contracts.Local("app.pair", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("pair", contracts.Str())},
	}))
	seen := newEventLog()
	errs := &hostErrors{}
	client := newHost(t, HostOptions{OnError: errs.push})
	a, b := link.MemoryLinkPair()
	client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background)
	peer := newRawPeer(t, b, rawPeerOptions{})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(Pair, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			one := self.Get(Notes, jsonObject("session", "S1"))
			two := self.Get(Notes, jsonObject("session", "S2"))
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"pair": self.Derive([]any{one.Member("count"), two.Member("count")}, func(values []any) (delta.JsonValue, error) {
						pair := formatNumber(values[0].(float64)) + "/" + formatNumber(values[1].(float64))
						seen.add(pair)
						return pair, nil
					}),
				},
			}}, nil
		}, ServeOptions{})
	})
	loading := make(chan error, 1)
	go func() { loading <- client.Load([]HostFacet{{Name: "@test/pair", Facet: facet}}, background) }()
	bindNext(t, peer)
	bindNext(t, peer)
	must(t, <-loading)
	view, err := client.Get(Pair, jsonObject(), background, GetOptions{})
	must(t, err)
	watch, err := view.Member("pair").Watch(background)
	must(t, err)
	return &pairHost{Client: client, Peer: peer, Seen: seen, Frames: nexttest.FramesOf(watch), Errors: errs, View: view}
}

func TestConsumerOneStateMessageOverSeveralHolds(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		countOps := func(ops string) []link.StateMember {
			return []link.StateMember{{Member: editor.State["count"], Ops: link.Text(ops)}}
		}

		// Ports "one state message over several holds › is applied as one batch: a derive over both holds never sees half of it, a failed member included".
		t.Run("is applied as one batch: a derive over both holds never sees half of it, a failed member included", func(t *testing.T) {
			p := pairOf(t)
			failed := []link.FailedMember{{Member: editor.State["notes"], Error: link.WireErrorOf(data.NewPluginError("derive", "notes threw"))}}
			p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{
				stateEntry(0, countOps(`[["r",1]]`), nil),
				stateEntry(1, countOps(`[["r",1]]`), failed),
			}})
			settle(p.Peer)
			nexttest.Delivered()
			var values []any
			for _, frame := range p.Frames.Items() {
				values = append(values, frame.Value)
			}
			nexttest.RequireJSONEqual(t, values, []any{"1/1"})
			requireStrings(t, p.Seen.all(), []string{"0/0", "1/1"})
			nexttest.RequireJSONEqual(t, memberValue(t, p.View, "pair"), "1/1")
		})

		// Ports "with a malformed later entry applies nothing and closes the link".
		t.Run("with a malformed later entry applies nothing and closes the link", func(t *testing.T) {
			p := pairOf(t)
			p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{
				stateEntry(0, countOps(`[["r",1]]`), nil),
				stateEntry(1, countOps(`[["x"]]`), nil),
			}})
			settle(p.Peer)
			nexttest.Delivered()
			if got := p.Frames.Items(); len(got) != 0 {
				t.Fatalf("frames = %d, want none", len(got))
			}
			requireStrings(t, p.Seen.all(), []string{"0/0"})
			if !p.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
			equalHostErrorKinds(t, p.Errors, ErrProtocolError)
		})
	})
}

func TestConsumerBytesOutsideMethodsWithBytes(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "$bytes outside methods with bytes › is plain data: {$bytes: 0} round-trips unchanged through a method without bytes".
		t.Run("is plain data: {$bytes: 0} round-trips unchanged through a method without bytes", func(t *testing.T) {
			Echo := remoteOneVariant("app.echo", contracts.Params{}, contracts.VariantDeclaration{
				Methods: []contracts.MethodEntry{contracts.Meth("echo", contracts.NewMethod(contracts.Map(contracts.Int()), contracts.Map(contracts.Int())))},
			})
			l := linked(t, func(h FacetHost) error {
				return h.Serve(Echo, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						Methods: map[string]contracts.MethodFunction{
							"echo": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return args, nil },
						},
					}}, nil
				}, ServeOptions{})
			}, linkedOptions{})
			echo, err := l.Client.Get(Echo, jsonObject(), background, GetOptions{})
			must(t, err)
			echoed, err := echo.Call("echo", jsonObject("$bytes", 0.0), background)
			must(t, err)
			nexttest.RequireJSONEqual(t, echoed, jsonObject("$bytes", 0.0))
		})
	})
}

// narrowHost is a client connected to a raw provider announcing one concurrent call, and two bound references.
type narrowHost struct {
	Peer     *RawPeer
	One, Two *Reference
	Holds    [2]int
}

func narrow(t testing.TB) *narrowHost {
	t.Helper()
	client := newHost(t, HostOptions{OnError: func(HostError) {}})
	must(t, client.Load(nil, background))
	a, b := link.MemoryLinkPair()
	client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background)
	peer := newRawPeer(t, b, rawPeerOptions{Calls: 1})
	one := asyncGet(client, Notes, jsonObject("session", "S1"), background, GetOptions{})
	first := bindNext(t, peer)
	two := asyncGet(client, Notes, jsonObject("session", "S2"), background, GetOptions{})
	second := bindNext(t, peer)
	return &narrowHost{Peer: peer, One: awaitReference(t, one), Two: awaitReference(t, two), Holds: [2]int{first, second}}
}

// callsOf are the calls the provider received, by hold.
func callsOf(peer *RawPeer) []int {
	holds := []int{}
	for _, message := range peer.Received() {
		if call, ok := message.(*link.CallMessage); ok {
			holds = append(holds, call.Hold)
		}
	}
	return holds
}

func TestConsumerCallWindow(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "the call window at the consumer › frees a slot once when a hold with a call in flight and one waiting is released".
		t.Run("frees a slot once when a hold with a call in flight and one waiting is released", func(t *testing.T) {
			n := narrow(t)
			sent := startCall(t, n.One, "wait", nil, background)
			waiting := startCall(t, n.One, "wait", nil, background)
			other := []*parkedCallResult{startCall(t, n.Two, "wait", nil, background), startCall(t, n.Two, "wait", nil, background)}
			settle(n.Peer)
			if got := callsOf(n.Peer); !slices.Equal(got, []int{n.Holds[0]}) {
				t.Fatalf("calls = %v, want [%d]", got, n.Holds[0])
			}
			n.One.Release()
			for _, call := range []*parkedCallResult{sent, waiting} {
				_, err := call.wait()
				requireChordError(t, err, data.CodeReleased, "")
			}
			settle(n.Peer)
			if got := callsOf(n.Peer); !slices.Equal(got, []int{n.Holds[0], n.Holds[1]}) {
				t.Fatalf("calls = %v, want [%d %d]", got, n.Holds[0], n.Holds[1])
			}
			if len(other) != 2 {
				t.Fatalf("other calls = %d, want 2", len(other))
			}
		})

		// Ports "frees a slot once on gone".
		t.Run("frees a slot once on gone", func(t *testing.T) {
			n := narrow(t)
			sent := startCall(t, n.One, "wait", nil, background)
			other := []*parkedCallResult{startCall(t, n.Two, "wait", nil, background), startCall(t, n.Two, "wait", nil, background)}
			settle(n.Peer)
			n.Peer.Send(&link.GoneMessage{Hold: n.Holds[0], Error: jsonObject("kind", "chord", "error",
				jsonObject("code", "unavailable", "message", "revoked"))})
			_, err := sent.wait()
			if chordErr := chordOf(t, err); chordErr.Message != "revoked" {
				t.Fatalf("message = %q, want revoked", chordErr.Message)
			}
			settle(n.Peer)
			if got := callsOf(n.Peer); !slices.Equal(got, []int{n.Holds[0], n.Holds[1]}) {
				t.Fatalf("calls = %v, want [%d %d]", got, n.Holds[0], n.Holds[1])
			}
			if len(other) != 2 {
				t.Fatalf("other calls = %d, want 2", len(other))
			}
		})

		// Ports "rejects a call still waiting when its hold rebinds: unavailable (race 26)".
		t.Run("rejects a call still waiting when its hold rebinds: unavailable (race 26)", func(t *testing.T) {
			n := narrow(t)
			sent := startCall(t, n.One, "wait", nil, background)
			waiting := startCall(t, n.One, "wait", nil, background)
			settle(n.Peer)
			n.Peer.Send(&link.LoadingMessage{Hold: n.Holds[0]})
			_, err := waiting.wait()
			if chordErr := requireChordError(t, err, data.CodeUnavailable, ""); chordErr.Message != "hold "+formatNumber(float64(n.Holds[0]))+": rebinding" {
				t.Fatalf("message = %q, want the rebinding message", chordErr.Message)
			}
			call := n.Peer.Next("call").(*link.CallMessage).Call
			n.Peer.Send(&link.ResultMessage{Call: call})
			result, err := sent.wait()
			must(t, err)
			if result != nil {
				t.Fatalf("result = %v, want null", result)
			}
		})
	})
}

func TestConsumerLimitsPerPart(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		part50 := rawLinkedOptions{Limits: link.PartLimits{Part: new(50.0)}}
		bigNotes := func() link.Part {
			return mustJSONPart(t, []any{jsonObject("text", string(repeatBytes('z', 60)))})
		}
		members := func(notes link.Part) []link.BoundMember { return boundMembers(t, notes, link.Text("1")) }

		// Ports "limits per part at the consumer › rejects a call whose result is past the link's limits with too_large/part; the link stays".
		t.Run("rejects a call whose result is past the link's limits with too_large/part; the link stays", func(t *testing.T) {
			p := rawProvider(t, part50)
			notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			bindNext(t, p.Peer)
			settle(p.Peer)
			added := startCall(t, notes, "add", jsonObject("text", "x"), background)
			call := p.Peer.Next("call").(*link.CallMessage).Call
			p.Peer.Send(&link.ResultMessage{Call: call, Value: mustJSONPart(t, string(repeatBytes('y', 60)))})
			_, err = added.wait()
			requireChordError(t, err, data.CodeTooLarge, data.ReasonPart)
			again := startCall(t, notes, "add", jsonObject("text", "x"), background)
			p.Peer.Send(&link.ResultMessage{Call: p.Peer.Next("call").(*link.CallMessage).Call, Value: link.Text("2")})
			result, err := again.wait()
			must(t, err)
			nexttest.RequireJSONEqual(t, result, 2.0)
		})

		// Ports "fails a hold whose snapshot is past the limits broken, releases it, and binds the message's others".
		t.Run("fails a hold whose snapshot is past the limits broken, releases it, and binds the message's others", func(t *testing.T) {
			p := rawProvider(t, part50)
			big, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			small, err := p.Client.Hold(Notes, jsonObject("session", "S2"), background, GetOptions{})
			must(t, err)
			one, two := p.Peer.Next("hold").(*link.HoldMessage), p.Peer.Next("hold").(*link.HoldMessage)
			p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{
				{Hold: one.Hold, Variant: editor.Variant, Members: members(bigNotes()), Failed: []link.FailedMember{}},
				{Hold: two.Hold, Variant: editor.Variant, Members: members(link.Text(`[{"text":"a"}]`)), Failed: []link.FailedMember{}},
			}})
			if released := p.Peer.Next("release").(*link.ReleaseMessage); released.Hold != one.Hold {
				t.Fatalf("released hold = %d, want %d", released.Hold, one.Hold)
			}
			if ack := p.Peer.Next("ack").(*link.AckMessage); ack.Origin.Ok {
				t.Fatalf("ack = %+v, want none origin", ack)
			}
			state := big.State().Value()
			if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || state.Cause.Error.Code != data.CodeTooLarge {
				t.Fatalf("big state = %+v, want failed broken too_large", state)
			}
			nexttest.RequireJSONEqual(t, memberValue(t, small, "notes"), []any{jsonObject("text", "a")})
			// No automatic re-hold; a retry holds again.
			settle(p.Peer)
			requireKinds(t, p.Peer)
			big.Retry()
			if kind := refStateKind(big); kind != RefStateLoading {
				t.Fatalf("state after retry = %s, want loading", kind)
			}
			if again := p.Peer.Next("hold").(*link.HoldMessage); again.Hold <= two.Hold {
				t.Fatalf("re-hold id = %d, want past %d", again.Hold, two.Hold)
			}
		})

		deflatedList := func() *link.DeflatedPart {
			json := `[{"text":"` + string(repeatBytes('q', 30_000)) + `"}]`
			return link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte(json)))
		}

		// Ports "acknowledges a message waiting for its asynchronous inflate only once applied, in order (race 14)".
		t.Run("acknowledges a message waiting for its asynchronous inflate only once applied, in order (race 14)", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
			p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
				Hold: hold, Variant: editor.Variant, Members: boundMembers(t, deflatedList(), link.Text("1")), Failed: []link.FailedMember{},
			}}})
			p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{stateEntry(hold,
				[]link.StateMember{{Member: editor.State["count"], Ops: mustJSONPart(t, []any{[]any{"r", 2.0}})}}, nil)}})
			p.Peer.Next("ack")
			list := memberValue(t, notes, "notes").([]any)
			if len(list) != 1 || len(list[0].(*delta.JsonObject).Value("text").(string)) != 30_000 {
				t.Fatalf("notes = %d entries, want the inflated one of 30000 characters", len(list))
			}
			p.Peer.Next("ack")
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 2.0)
		})

		// Ports "drops a message whose asynchronous inflate is in flight when the link closes, and those behind it (race 13)".
		t.Run("drops a message whose asynchronous inflate is in flight when the link closes, and those behind it (race 13)", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
			p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
				Hold: hold, Variant: editor.Variant, Members: boundMembers(t, deflatedList(), link.Text("1")), Failed: []link.FailedMember{},
			}}})
			p.Peer.Close()
			for range 20 {
				nexttest.Delivered()
			}
			if kind := refStateKind(notes); kind != RefStateLoading {
				t.Fatalf("state = %s, want loading", kind)
			}
			if p.Errors.Len() != 0 {
				t.Fatalf("errors = %v, want none", p.Errors.All())
			}
		})

		// Ports "inflates nothing it gave up (Q1): a corrupt deflated result of an unknown call, an entry of a released hold (both paths)".
		t.Run("inflates nothing it gave up (Q1): a corrupt deflated result of an unknown call, an entry of a released hold (both paths)", func(t *testing.T) {
			for _, compression := range []link.Compression{nextnode.NodeCompression, nil} {
				p := rawProvider(t, rawLinkedOptions{Compression: compression})
				kept, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
				must(t, err)
				released, err := p.Client.Hold(Notes, jsonObject("session", "S2"), background, GetOptions{})
				must(t, err)
				a, b := bindNext(t, p.Peer), bindNext(t, p.Peer)
				// The acks of the two bindings (Pi's next("ack") below meets the state's: its turn order does the same).
				p.Peer.Next("ack")
				p.Peer.Next("ack")
				settle(p.Peer)
				released.Release()
				p.Peer.Next("release")
				corrupt := func() *link.DeflatedPart { return link.NewDeflatedPart([]byte{255, 255, 255}) }
				p.Peer.Send(&link.ResultMessage{Call: 999, Value: corrupt()})
				p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{
					stateEntry(b, []link.StateMember{{Member: editor.State["count"], Ops: corrupt()}}, nil),
					stateEntry(a, []link.StateMember{{Member: editor.State["count"], Ops: mustJSONPart(t, []any{[]any{"r", 4.0}})}}, nil),
				}})
				p.Peer.Next("ack")
				nexttest.RequireJSONEqual(t, memberValue(t, kept, "count"), 4.0)
				if p.Peer.Closed() {
					t.Fatal("the link closed")
				}
				if p.Errors.Len() != 0 {
					t.Fatalf("errors = %v, want none", p.Errors.All())
				}
			}
		})

		// Ports "checks a deflated part's wire size before inflating it: corrupt data past the limit only fails its hold".
		t.Run("checks a deflated part's wire size before inflating it: corrupt data past the limit only fails its hold", func(t *testing.T) {
			for _, compression := range []link.Compression{nextnode.NodeCompression, nil} {
				p := rawProvider(t, rawLinkedOptions{Compression: compression, Limits: link.PartLimits{Part: new(10.0)}})
				notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
				must(t, err)
				hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
				corrupt := repeatBytes(255, 20)
				p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
					Hold: hold, Variant: editor.Variant, Members: boundMembers(t, link.NewDeflatedPart(corrupt), link.Text("1")), Failed: []link.FailedMember{},
				}}})
				if released := p.Peer.Next("release").(*link.ReleaseMessage); released.Hold != hold {
					t.Fatalf("released hold = %d, want %d", released.Hold, hold)
				}
				state := notes.State().Value()
				if state.Kind != RefStateFailed || state.Cause.Error.Code != data.CodeTooLarge {
					t.Fatalf("state = %+v, want failed too_large", state)
				}
				if p.Errors.Len() != 0 {
					t.Fatalf("errors = %v, want none", p.Errors.All())
				}
			}
		})

		// Ports "keeps a hold failed for a part past the limits off the next link until retry, offline retry included".
		t.Run("keeps a hold failed for a part past the limits off the next link until retry, offline retry included", func(t *testing.T) {
			p := rawProvider(t, part50)
			notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
			p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
				Hold: hold, Variant: editor.Variant, Members: members(bigNotes()), Failed: []link.FailedMember{},
			}}})
			p.Peer.Next("release")
			p.Peer.Close()
			settle(p.Peer)
			next := rawParent(t, p.Client, link.PartLimits{Part: new(50.0)})
			settle(next)
			// A reconnect holds nothing again: the failure is final until retry.
			requireKinds(t, next)
			if kind := refStateKind(notes); kind != RefStateFailed {
				t.Fatalf("state = %s, want failed", kind)
			}
			next.Close()
			settle(next)
			// A retry while offline holds it on the next link.
			notes.Retry()
			if kind := refStateKind(notes); kind != RefStateLoading {
				t.Fatalf("state after retry = %s, want loading", kind)
			}
			third := rawParent(t, p.Client, link.PartLimits{})
			if held := third.Next("hold").(*link.HoldMessage); held.Type != Notes.Name {
				t.Fatalf("held type = %q, want %q", held.Type, Notes.Name)
			}
		})

		// Ports "stops an asynchronous inflate at once when the link closes: nothing of the link stays (GC)". Go form:
		// Pi stalls a stubbed DecompressionStream; link.InflateAsync (which stops at once on the link's close, tested in
		// package link) cannot be stalled, so the part inflates, and what is asserted is that the host and its link
		// stay collectable after the close.
		t.Run("stops an asynchronous inflate at once when the link closes: nothing of the link stays (GC)", func(t *testing.T) {
			var clientRef weak.Pointer[chordHost]
			func() {
				p := rawProvider(t, rawLinkedOptions{})
				_, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
				must(t, err)
				hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
				p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
					Hold: hold, Variant: editor.Variant,
					Members: boundMembers(t, link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte("[]"))), link.Text("1")),
					Failed:  []link.FailedMember{},
				}}})
				settle(p.Peer)
				p.Peer.Close()
				settle(p.Peer)
				must(t, p.Client.Close(background))
				clientRef = weak.Make(p.Client.(*chordHost))
			}()
			nexttest.ExpectCollected(t, clientRef)
		})

		// Ports "fails only the hold of a state part past the limits (race 10); the others of the message apply".
		t.Run("fails only the hold of a state part past the limits (race 10); the others of the message apply", func(t *testing.T) {
			p := rawProvider(t, part50)
			one, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			two, err := p.Client.Hold(Notes, jsonObject("session", "S2"), background, GetOptions{})
			must(t, err)
			a, b := bindNext(t, p.Peer), bindNext(t, p.Peer)
			settle(p.Peer)
			ops := func(count float64, pad int) link.Part {
				list := []any{[]any{"r", count}}
				for range pad {
					list = append(list, []any{"r", count})
				}
				return mustJSONPart(t, list)
			}
			p.Peer.Send(&link.StateMessage{Holds: []link.StateEntry{
				stateEntry(a, []link.StateMember{{Member: editor.State["count"], Ops: ops(5, 10)}}, nil),
				stateEntry(b, []link.StateMember{{Member: editor.State["count"], Ops: ops(7, 0)}}, nil),
			}})
			if released := p.Peer.Next("release").(*link.ReleaseMessage); released.Hold != a {
				t.Fatalf("released hold = %d, want %d", released.Hold, a)
			}
			settle(p.Peer)
			state := one.State().Value()
			if state.Kind != RefStateFailed || state.Cause.Error.Code != data.CodeTooLarge {
				t.Fatalf("state = %+v, want failed too_large", state)
			}
			nexttest.RequireJSONEqual(t, memberValue(t, two, "count"), 7.0)
			if p.Peer.Closed() {
				t.Fatal("the link closed")
			}
		})
	})
}
