package host

import (
	"context"
	"fmt"
	"runtime"
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
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/remote/remote-object.test.ts.
//
// Go forms: see remote_hold_test.go (rhSync is `await transported()`; rhVirtual is `fakeTimers()`; a started Promise is
// an rhGo goroutine). `instancesOf(RemoteObject, "hold")` is designed out: a Go test reaches the host's remote objects
// through the references' bindings and checks collection with weak pointers.

func rhWireError(err error) link.WireError { return link.WireErrorOf(err) }

func rhPart(t testing.TB, json string) link.Part {
	t.Helper()
	return link.Text(json)
}

// rhProvider is rawProvider's client and raw peer.
func rhProvider(t testing.TB) *rawProviderHost { return rawProvider(t, rawLinkedOptions{}) }

func rhRefState(ref *Reference) RefStateKind { return ref.State().Value().Kind }

func rhHoldRef(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	ref, err := host.Hold(typ, params, background, GetOptions{})
	must(t, err)
	return ref
}

func rhNotes(session string) *delta.JsonObject { return jsonObject("session", session) }

func rhStateEntry(hold int, members []link.StateMember, failed []link.FailedMember, retired []int) *link.StateMessage {
	if failed == nil {
		failed = []link.FailedMember{}
	}
	if retired == nil {
		retired = link.NoRetired
	}
	return &link.StateMessage{Holds: []link.StateEntry{{Hold: hold, Members: members, Failed: failed, Retired: retired}}}
}

// rhCalls waits for the watch's collector to hold n calls.
func rhWaitCalls[T any](t testing.TB, collector *nexttest.Collector[T], n int) {
	t.Helper()
	rhWaitFor(t, func() bool { return collector.Len() >= n })
}

func rhCallKinds(calls []chord.WatchCall[delta.JsonValue]) []string {
	kinds := make([]string, len(calls))
	for i, call := range calls {
		kinds[i] = string(call.Kind)
	}
	return kinds
}

func TestRemoteObjectHoldOverTheParentLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: a hold over the parent link › reject a broken failure at once
		t.Run("reject a broken failure at once", func(t *testing.T) {
			broken := rhRemote("app.broken", []contracts.Property{rhProp("n", contracts.Int())})
			hosts := linked(t, func(host FacetHost) error {
				return host.Serve(broken, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					return contracts.Implementation{}, fmt.Errorf("factory threw")
				}, ServeOptions{})
			}, linkedOptions{})
			timeout := 5 * time.Second
			_, err := hosts.Client.Get(broken, jsonObject(), background, GetOptions{ReadyTimeout: &timeout})
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"unavailable","message":"app.broken: factory threw"}`; got != want {
				t.Fatalf("rejection = %s, want %s", got, want)
			}
		})

		// remote-object.test.ts: a hold over the parent link › gone: the reference ends, its pending calls reject with the error
		t.Run("gone: the reference ends, its pending calls reject with the error", func(t *testing.T) {
			rig := rhProvider(t)
			got := rhGo(func() (*Reference, error) {
				return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{})
			})
			hold := bindNext(t, rig.Peer)
			notes := got.must(t)
			watch, err := notes.Member("notes").Watch(background)
			must(t, err)
			call := rhGo(func() (delta.JsonValue, error) { return notes.Call("wait", nil, background) })
			rig.Peer.Next("call")
			ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.notes(S1): ended"}, nil)
			rig.Peer.Send(&link.GoneMessage{Hold: hold, Error: rhWireError(ended)})
			_, err = call.wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"released","reason":"ended","message":"app.notes(S1): ended"}`; got != want {
				t.Fatalf("rejection = %s, want %s", got, want)
			}
			<-watch.Closed()
			if kind := watch.End().Kind; kind != chord.WatchEnded {
				t.Fatalf("watch ended %v, want ended", kind)
			}
			if _, err := notes.Member("count").Value(); err == nil || !strings.Contains(err.Error(), "app.notes(S1): ended") {
				t.Fatalf("count.value error = %v", err)
			}
		})

		// remote-object.test.ts: a hold over the parent link › two references from one host are two holds
		t.Run("two references from one host are two holds", func(t *testing.T) {
			rig := rhProvider(t)
			first := rhGo(func() (*Reference, error) { return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{}) })
			if id := bindNext(t, rig.Peer); id != 0 {
				t.Fatalf("first hold = %d, want 0", id)
			}
			second := rhGo(func() (*Reference, error) { return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{}) })
			if id := bindNext(t, rig.Peer); id != 1 {
				t.Fatalf("second hold = %d, want 1", id)
			}
			first.must(t)
			second.must(t)
		})

		// remote-object.test.ts: a hold over the parent link › a member failure fails only that replica: reads throw it, its watch gets a failure call and stays open; the others update
		t.Run("a member failure fails only that replica: reads throw it, its watch gets a failure call and stays open; the others update", func(t *testing.T) {
			rig := rhProvider(t)
			got := rhGo(func() (*Reference, error) { return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{}) })
			hold := bindNext(t, rig.Peer)
			notes := got.must(t)
			editor := editorWire(t)
			count := nexttest.ChangesOf(rhWatch(t, notes.Member("count")))
			list := nexttest.FramesOf(rhWatch(t, notes.Member("notes")))
			failed := []link.FailedMember{{Member: editor.State["count"], Error: rhWireError(data.NewPluginError("derive", "count threw"))}}
			members := []link.StateMember{{Member: editor.State["notes"], Ops: rhPart(t, `[["p",[],0,0,[{"text":"a"}]]]`)}}
			rig.Peer.Send(rhStateEntry(hold, members, failed, nil))
			rhWaitCalls(t, count, 1)
			rhWaitCalls(t, list, 1)
			if first := count.Items()[0]; first.Kind != chord.WatchCallFailed || first.Error.(*data.PluginError).Code != "derive" {
				t.Fatalf("count call = %+v", first)
			}
			frame := list.Items()[0]
			rhEqual(t, "list", []any{frame.Value, frame.Ops}, []any{[]any{map[string]any{"text": "a"}}, []any{[]any{"p", []any{}, 0, 0, []any{map[string]any{"text": "a"}}}}})
			if _, err := notes.Member("count").Value(); err == nil || !strings.Contains(err.Error(), "count threw") {
				t.Fatalf("count.value error = %v", err)
			}
			// The provider's derive healed: its next value is a root replacement, in the same hold.
			healed := []link.StateMember{{Member: editor.State["count"], Ops: rhPart(t, `[["r",1]]`)}}
			rig.Peer.Send(rhStateEntry(hold, healed, nil, nil))
			rhWaitCalls(t, count, 2)
			rhEqual(t, "count.value", rhRefValue(t, notes, "count"), 1)
			last := count.Items()[count.Len()-1]
			rhEqual(t, "last", []any{last.Kind, last.Value, last.Ops, last.Retired}, []any{"value", 1, []any{[]any{"r", 1}}, false})
		})
	})
}

func TestRemoteObjectRetiredMembers(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: retired members › reach the replicas' watches with the flag: with the member's ops, or alone
		t.Run("reach the replicas' watches with the flag: with the member's ops, or alone", func(t *testing.T) {
			rig := rhProvider(t)
			got := rhGo(func() (*Reference, error) { return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{}) })
			hold := bindNext(t, rig.Peer, jsonObject("text", "a"))
			notes := got.must(t)
			editor := editorWire(t)
			list := nexttest.ChangesOf(rhWatch(t, notes.Member("notes")))
			count := nexttest.ChangesOf(rhWatch(t, notes.Member("count")))
			members := []link.StateMember{{Member: editor.State["notes"], Ops: rhPart(t, `[["r",[]]]`)}}
			retired := []int{editor.State["notes"], editor.State["count"]}
			rig.Peer.Send(rhStateEntry(hold, members, nil, retired))
			rhWaitCalls(t, list, 1)
			rhWaitCalls(t, count, 1)
			l, c := list.Items()[0], count.Items()[0]
			rhEqual(t, "list", []any{l.Kind, l.Value, l.Ops, l.Retired}, []any{"value", []any{}, []any{[]any{"r", []any{}}}, true})
			rhEqual(t, "count", []any{c.Kind, c.Value, c.Ops, c.Retired}, []any{"value", 1, []any{}, true})
		})

		// remote-object.test.ts: retired members › of a failed member wait for its next value: never read from the failed replica; the link stays open
		t.Run("of a failed member wait for its next value: never read from the failed replica; the link stays open", func(t *testing.T) {
			rig := rhProvider(t)
			got := rhGo(func() (*Reference, error) { return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{}) })
			hold := bindNext(t, rig.Peer, jsonObject("text", "a"))
			notes := got.must(t)
			editor := editorWire(t)
			count := nexttest.ChangesOf(rhWatch(t, notes.Member("count")))
			member := editor.State["count"]
			failed := []link.FailedMember{{Member: member, Error: rhWireError(data.NewPluginError("derive", "count threw"))}}
			rig.Peer.Send(rhStateEntry(hold, nil, failed, nil))
			// A retirement alone (no ops) of the failed member, then its healed value.
			rig.Peer.Send(rhStateEntry(hold, nil, nil, []int{member}))
			rhWaitCalls(t, count, 1)
			rig.Peer.Send(rhStateEntry(hold, []link.StateMember{{Member: member, Ops: rhPart(t, `[["r",5]]`)}}, nil, nil))
			rhWaitCalls(t, count, 2)
			rhSync(t, rig.Peer)
			if rig.Peer.Closed() || rhRefState(notes) != RefStateLive || rig.Errors.Len() != 0 {
				t.Fatalf("closed %v, state %v, errors %v; want open, live, none", rig.Peer.Closed(), rhRefState(notes), rig.Errors.All())
			}
			calls := count.Items()
			if calls[0].Kind != chord.WatchCallFailed || !strings.Contains(calls[0].Error.Error(), "count threw") {
				t.Fatalf("first call = %+v", calls[0])
			}
			rhEqual(t, "second", []any{calls[1].Kind, calls[1].Value, calls[1].Ops, calls[1].Retired}, []any{"value", 5, []any{[]any{"r", 5}}, true})
		})

		// remote-object.test.ts: retired members › that %s closes the link
		for name, retired := range map[string]func(editor wireIndexes) []int{
			"names a member outside the variant": func(wireIndexes) []int { return []int{7} },
			"names a member twice":               func(e wireIndexes) []int { return []int{e.State["count"], e.State["count"]} },
		} {
			t.Run("that "+name+" closes the link", func(t *testing.T) {
				rig := rhProvider(t)
				got := rhGo(func() (*Reference, error) { return rig.Client.Get(Notes, rhNotes("S1"), background, GetOptions{}) })
				hold := bindNext(t, rig.Peer)
				got.must(t)
				rig.Peer.Send(rhStateEntry(hold, nil, nil, retired(editorWire(t))))
				rhWaitFor(t, rig.Peer.Closed)
				if kinds := rhErrorKinds(rig.Errors); !slices.Equal(kinds, []string{"protocol_error"}) {
					t.Fatalf("errors = %v, want [protocol_error]", kinds)
				}
			})
		}
	})
}

func rhErrorKinds(errs *hostErrors) []string {
	var kinds []string
	for _, err := range errs.All() {
		kinds = append(kinds, string(err.Kind))
	}
	return kinds
}

func TestRemoteObjectASnapshot(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editorMember := func(name string) int { return editorWire(t).State[name] }
		cases := []struct {
			name    string
			members func() []link.BoundMember
			variant func() int
		}{
			{"misses a member", func() []link.BoundMember {
				return []link.BoundMember{{Member: editorMember("notes"), Value: link.Text("[]")}}
			}, func() int { return editorWire(t).Variant }},
			{"names a member twice", func() []link.BoundMember {
				return []link.BoundMember{
					{Member: editorMember("notes"), Value: link.Text("[]")}, {Member: editorMember("notes"), Value: link.Text("[]")},
					{Member: editorMember("count"), Value: link.Text("0")},
				}
			}, func() int { return editorWire(t).Variant }},
			{"names a member outside the variant", func() []link.BoundMember {
				return []link.BoundMember{{Member: 7, Value: link.Text("0")}}
			}, func() int { return editorWire(t).Variant }},
			{"names a variant outside the version", func() []link.BoundMember { return nil }, func() int { return 5 }},
		}
		for _, c := range cases {
			// remote-object.test.ts: a snapshot › that %s closes the link; the reference stays loading
			t.Run("that "+c.name+" closes the link; the reference stays loading", func(t *testing.T) {
				rig := rhProvider(t)
				notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
				hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
				rig.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
					Hold: hold, Variant: c.variant(), Members: c.members(), Failed: []link.FailedMember{},
				}}})
				rhWaitFor(t, rig.Peer.Closed)
				if kinds := rhErrorKinds(rig.Errors); !slices.Equal(kinds, []string{"protocol_error"}) {
					t.Fatalf("errors = %v, want [protocol_error]", kinds)
				}
				if kind := rhRefState(notes); kind != RefStateLoading {
					t.Fatalf("state = %v, want loading", kind)
				}
				if variant, bound, err := notes.Variant(); err != nil || bound || variant != "" {
					t.Fatalf("variant = %q %v %v, want unbound", variant, bound, err)
				}
			})
		}
	})
}

func TestRemoteObjectTransientFailuresOverALink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: transient failures over a link › are waited through; the deadline rejects with the last cause and releases the hold
		t.Run("are waited through; the deadline rejects with the last cause and releases the hold", func(t *testing.T) {
			flaky := mustContract(contracts.Remote("app.flaky", contracts.RemoteDeclaration{
				Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, HostAddress: exServerAddress,
				Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
					State: []contracts.Property{rhProp("n", contracts.Int())},
				})},
			}))
			var mu sync.Mutex
			var opens []string
			record := func(text string) int {
				mu.Lock()
				defer mu.Unlock()
				// `open ${opens.length + 1}`: only opens come before the abort.
				if text == "open" {
					text = fmt.Sprintf("open %d", len(opens)+1)
				}
				opens = append(opens, text)
				return len(opens)
			}
			virtual := newRhVirtual()
			errs := &hostErrors{}
			server := rhCreate(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: errs.push, loop: virtual.loop})
			must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
				return host.Serve(flaky, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					self.Open(func(ctx context.Context) (any, error) {
						if record("open") == 1 {
							return nil, fmt.Errorf("offline")
						}
						<-ctx.Done()
						record("aborted")
						return nil, nil
					}, func(any, NativeCloseReason, context.Context) error { return nil })
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(params.Value("id"))},
					}}, nil
				}, ServeOptions{})
			})}}, background))
			client := rhCreate(t, HostOptions{OnError: errs.push, loop: virtual.loop})
			must(t, client.Load(nil, background))
			a, b := link.MemoryLinkPair()
			must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			timeout := 1500 * time.Millisecond
			got := rhGo(func() (*Reference, error) {
				return client.Get(flaky, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &timeout})
			})
			rhSettleLinks(a, b)
			virtual.advance(1000)
			rhSettleLinks(a, b)
			rhWaitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(opens) == 2 })
			virtual.advance(500)
			_, err := got.wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"unavailable","message":"app.flaky(1): offline"}`; got != want {
				t.Fatalf("rejection = %s, want %s", got, want)
			}
			rhSettleLinks(a, b)
			// The release ended the object: its pending attempt was abandoned.
			rhWaitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(opens) == 3 })
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(opens, []string{"open 1", "open 2", "aborted"}) {
				t.Fatalf("opens = %v", opens)
			}
		})
	})
}

// --- GC of a released remote reference -------------------------------------------------------------------------------

func rhReplicaOf(t testing.TB, ref *Reference, member string) *replica {
	t.Helper()
	source, err := ref.Member(member).source()
	must(t, err)
	return source.(*replica)
}

func rhRemoteObjectOf(ref *Reference) *remoteObject { return ref.binding.target().(*remoteObject) }

func TestRemoteObjectGCOfAReleasedReference(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: GC of a released remote reference › a released reference retained by the test keeps no hold, replica or object
		t.Run("a released reference retained by the test keeps no hold, replica or object", func(t *testing.T) {
			hosts := linked(t, func(host FacetHost) error { _, err := serveNotes(host, nil); return err }, linkedOptions{})
			notes, err := hosts.Client.Get(Notes, rhNotes("S1"), background, GetOptions{})
			must(t, err)
			replicaRef := weak.Make(rhReplicaOf(t, notes, "notes"))
			objectRef := weak.Make(rhRemoteObjectOf(notes))
			notes.Release()
			transported(hosts.Links[0], hosts.Links[1])
			nexttest.ExpectCollected(t, replicaRef)
			nexttest.ExpectCollected(t, objectRef)
			runtime.KeepAlive(notes)
			runtime.KeepAlive(hosts)
		})

		// remote-object.test.ts: GC of a released remote reference › a failure call's error kept by the app keeps neither the replica nor the remote object
		t.Run("a failure call's error kept by the app keeps neither the replica nor the remote object", func(t *testing.T) {
			halves := rhRemote("app.halves2", []contracts.Property{rhProp("half", contracts.Int())},
				contracts.Meth("set", contracts.NewMethod(contracts.Int())))
			hosts := linked(t, func(host FacetHost) error {
				return host.Serve(halves, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					doc := stateOf(self, rhCounter)
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"half": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
								n := values[0].(*delta.JsonObject).Value("n").(float64)
								if n == 2 {
									return nil, fmt.Errorf("half threw")
								}
								return n / 2, nil
							}),
						},
						Methods: map[string]contracts.MethodFunction{
							"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
							},
						},
					}}, nil
				}, ServeOptions{})
			}, linkedOptions{})
			var kept []error
			var mu sync.Mutex
			ref, err := hosts.Client.Get(halves, jsonObject(), background, GetOptions{})
			must(t, err)
			replicaRef := weak.Make(rhReplicaOf(t, ref, "half"))
			objectRef := weak.Make(rhRemoteObjectOf(ref))
			watch := rhWatch(t, ref.Member("half"))
			watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
				if call.Kind == chord.WatchCallFailed {
					mu.Lock()
					kept = append(kept, call.Error)
					mu.Unlock()
				}
				return nil
			})
			_, err = ref.Call("set", float64(2), background)
			must(t, err)
			rhWaitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(kept) == 1 })
			ref.Release()
			transported(hosts.Links[0], hosts.Links[1])
			ref = nil
			nexttest.ExpectCollected(t, replicaRef)
			nexttest.ExpectCollected(t, objectRef)
			runtime.KeepAlive(kept)
			runtime.KeepAlive(hosts)
		})
	})
}

// --- self.get of a type over the link --------------------------------------------------------------------------------

var rhViewOfNotes = mustContract(contracts.Local("app.view", contracts.LocalDeclaration{
	Version: 1, Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int())},
}))

// rhViewClient is a client connected to a raw provider whose facet serves rhViewOfNotes, a view of Notes(S1)'s count; load
// is its `load` still running.
func rhViewClient(t testing.TB, loop sectionLoop, factories *int, mu *sync.Mutex) (Host, *RawPeer, *hostErrors, *rhAsync[struct{}]) {
	t.Helper()
	errs := &hostErrors{}
	client := rhCreate(t, HostOptions{OnError: errs.push, loop: loop})
	a, b := link.MemoryLinkPair()
	must(t, client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	facet := DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(rhViewOfNotes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			if mu != nil {
				mu.Lock()
				*factories++
				mu.Unlock()
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"count": Expose(self.Get(Notes, rhNotes("S1")).Member("count"))},
			}}, nil
		}, ServeOptions{})
	})
	loading := rhGo(func() (struct{}, error) {
		return struct{}{}, client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background)
	})
	return client, peer, errs, loading
}

func TestRemoteObjectSelfGetOverTheLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: self.get of a type over the link (dependency model §3) › a failed hold parks the dependent failed { waiting }: no new attempt or hold; bound wakes it
		t.Run("a failed hold parks the dependent failed { waiting }: no new attempt or hold; bound wakes it", func(t *testing.T) {
			var mu sync.Mutex
			factories := 0
			client, peer, errs, loading := rhViewClient(t, nil, &factories, &mu)
			hold := peer.Next("hold").(*link.HoldMessage).Hold
			cause := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "warming"}, nil)
			peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "transient", Error: rhWireError(cause)}})
			// Parked on a remote blocker: its first outcome, so the start barrier is over.
			loading.must(t)
			rhSync(t, peer)
			if errs.Len() != 0 {
				t.Fatalf("errors = %v", errs.All())
			}
			view := rhHoldRef(t, client, rhViewOfNotes, jsonObject())
			state := view.State().Value()
			if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "waiting for app.notes(S1): warming" {
				t.Fatalf("state = %+v", state)
			}
			bindHold(t, peer, hold, jsonObject("text", "a"))
			rhWaitFor(t, func() bool { return rhRefState(view) == RefStateLive })
			rhEqual(t, "count", rhRefValue(t, view, "count"), 1)
			mu.Lock()
			defer mu.Unlock()
			if factories != 1 {
				t.Fatalf("factories = %d, want 1", factories)
			}
			rhSync(t, peer)
			for _, kind := range peer.Kinds() {
				if kind == "hold" || kind == "release" {
					t.Fatalf("unexpected %s", kind)
				}
			}
		})
	})
}

func TestRemoteObjectSelfGetThroughARelayWhoseUpstreamDropped(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: self.get through a relay whose upstream dropped (R18) › a loading notice while loading changes nothing: the dependent goes on waiting and goes live when bound
		t.Run("a loading notice while loading changes nothing: the dependent goes on waiting and goes live when bound", func(t *testing.T) {
			client, peer, errs, loading := rhViewClient(t, nil, nil, nil)
			hold := peer.Next("hold").(*link.HoldMessage).Hold
			// The relay's upstream link dropped before the hold was bound: a drop is not a dependency failure (audit).
			peer.Send(&link.LoadingMessage{Hold: hold})
			rhSync(t, peer)
			bindHold(t, peer, hold, jsonObject("text", "a"))
			loading.must(t)
			if errs.Len() != 0 {
				t.Fatalf("errors = %v", errs.All())
			}
			view, err := client.Get(rhViewOfNotes, jsonObject(), background, GetOptions{})
			must(t, err)
			rhEqual(t, "count", rhRefValue(t, view, "count"), 1)
		})
	})
}

func TestRemoteObjectDependentOfAnObjectFailedTransientlyOverTheLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: a dependent of an object failed transiently over the link › parks at once on the real cause when its load holds it (no wait for a timeout)
		t.Run("parks at once on the real cause when its load holds it (no wait for a timeout)", func(t *testing.T) {
			// Timers stand still: neither the backoff window nor a readyTimeout ends while the test runs.
			virtual := newRhVirtual()
			view := mustContract(contracts.Local("app.view", contracts.LocalDeclaration{
				Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
			}))
			server := rhCreate(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}, loop: virtual.loop})
			must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
				return rhServeFlaky(&rhFailing{left: 1000})(host)
			})}}, background))
			first := rhCreate(t, HostOptions{OnError: func(HostError) {}, loop: virtual.loop})
			must(t, first.Load(nil, background))
			a, b := link.MemoryLinkPair()
			must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			must(t, first.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			// The first client's get makes the object fail; it stays failed for its backoff window.
			failing := rhGo(func() (*Reference, error) { return first.Get(rhFlaky, jsonObject(), background, GetOptions{}) })
			rhSettleLinks(a, b)
			errs := &hostErrors{}
			second := rhCreate(t, HostOptions{OnError: errs.push, loop: virtual.loop})
			c, d := link.MemoryLinkPair()
			must(t, server.Accept(c, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			must(t, second.Connect(d, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			facet := DefineFacet(func(host FacetHost, _ FacetSelf) error {
				return host.Serve(view, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					return contracts.Implementation{Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(rhFlaky, jsonObject()).Member("n"))},
					}}, nil
				}, ServeOptions{})
			})
			must(t, second.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background))
			rhSettleLinks(c, d)
			if errs.Len() != 0 {
				t.Fatalf("errors = %v", errs.All())
			}
			state := rhHoldRef(t, second, view, jsonObject()).State().Value()
			if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting ||
				state.Cause.Error.Message != "waiting for app.flaky: app.flaky: offline" {
				t.Fatalf("state = %+v", state)
			}
			must(t, first.Close(background))
			failing.wait()
		})
	})
}

// --- re-holds after a drop -------------------------------------------------------------------------------------------

// rhShifting is a remote type whose variants share `y`: `a` has `x` and a method, `b` has `z`.
var rhShifting = mustContract(contracts.Remote("app.shifting", contracts.RemoteDeclaration{
	Version: 1, HostAddress: exServerAddress,
	Variants: []contracts.VariantEntry{
		contracts.Var("a", contracts.VariantDeclaration{
			State:   []contracts.Property{rhProp("x", contracts.Int()), rhProp("y", contracts.Int())},
			Methods: []contracts.MethodEntry{contracts.Meth("m", contracts.NewMethod())},
		}),
		contracts.Var("b", contracts.VariantDeclaration{State: []contracts.Property{rhProp("y", contracts.Int()), rhProp("z", contracts.Int())}}),
	},
}))

type rhShiftingRig struct {
	Client Host
	Peer   *RawPeer
	View   *Reference
	Ref    *Reference
	Errors *hostErrors
}

// rhShiftingClient is a client whose View derives x+y of a Shifting reference, connected to a raw provider; View is bound.
func rhShiftingClient(t testing.TB) *rhShiftingRig {
	t.Helper()
	view := mustContract(contracts.Local("app.view", contracts.LocalDeclaration{
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("sum", contracts.Int()), contracts.Mem("label", contracts.Str())},
	}))
	errs := &hostErrors{}
	client := rhCreate(t, HostOptions{OnError: errs.push})
	must(t, client.Load(nil, background))
	peer := rawParent(t, client, link.PartLimits{})
	var held *Reference
	facet := DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(view, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			ref := self.Get(rhShifting, jsonObject())
			held = ref
			x, y := ref.Member("x"), ref.Member("y")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"sum": self.Derive([]any{x, y}, func(values []any) (delta.JsonValue, error) {
					return values[0].(float64) + values[1].(float64), nil
				}),
				// Reads the reference's variant when `y` changes.
				"label": self.Derive([]any{y}, func(values []any) (delta.JsonValue, error) {
					variant, _, _ := ref.Variant()
					return fmt.Sprintf("%s:%v", variant, values[0]), nil
				}),
			}}}, nil
		}, ServeOptions{})
	})
	loading := rhGo(func() (struct{}, error) {
		return struct{}{}, client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background)
	})
	a := wireNames(t, rhShifting, "a")
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
		Hold: hold, Variant: a.Variant, Failed: []link.FailedMember{},
		Members: []link.BoundMember{{Member: a.State["x"], Value: link.Text("1")}, {Member: a.State["y"], Value: link.Text("2")}},
	}}})
	loading.must(t)
	viewRef, err := client.Get(view, jsonObject(), background, GetOptions{})
	must(t, err)
	return &rhShiftingRig{Client: client, Peer: peer, View: viewRef, Ref: held, Errors: errs}
}

// rhReboundA sends the bound of the Shifting hold on a new link: variant a with x and y.
func rhBindShifting(t testing.TB, peer *RawPeer, variant string, values map[string]string) {
	t.Helper()
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	names := wireNames(t, rhShifting, variant)
	var members []link.BoundMember
	for _, name := range []string{"x", "y", "z"} {
		if value, ok := values[name]; ok {
			members = append(members, link.BoundMember{Member: names.State[name], Value: link.Text(value)})
		}
	}
	peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{Hold: hold, Variant: names.Variant, Members: members, Failed: []link.FailedMember{}}}})
}

func rhWaitState(t testing.TB, ref *Reference, want RefStateKind) {
	t.Helper()
	rhWaitFor(t, func() bool { return rhRefState(ref) == want })
}

func TestRemoteObjectReHoldsAfterADrop(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: re-holds after a drop › hold every reference not released again on the next link, with that link's fresh ids, in order
		t.Run("hold every reference not released again on the next link, with that link's fresh ids, in order", func(t *testing.T) {
			rig := rhProvider(t)
			var refs []*Reference
			for _, session := range []string{"S1", "S2", "S3"} {
				refs = append(refs, rhHoldRef(t, rig.Client, Notes, rhNotes(session)))
			}
			for range 3 {
				bindNext(t, rig.Peer)
			}
			refs[1].Release()
			rig.Peer.Close()
			rhWaitState(t, refs[0], RefStateLoading)
			next := rawParent(t, rig.Client, link.PartLimits{})
			for i, session := range []string{"S1", "S3"} {
				hold := next.Next("hold").(*link.HoldMessage)
				params := partValue(t, hold.Params).(*delta.JsonObject)
				if hold.Hold != i || params.Value("session") != session {
					t.Fatalf("hold %d = %d %v, want %d %s", i, hold.Hold, params, i, session)
				}
			}
		})

		// remote-object.test.ts: re-holds after a drop › apply bound in one batch: each member gets one frame [['r', value]] and a fresh decoder
		t.Run("apply bound in one batch: each member gets one frame [['r', value]] and a fresh decoder", func(t *testing.T) {
			rig := rhProvider(t)
			notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			bindNext(t, rig.Peer, jsonObject("text", "a"))
			rhWaitState(t, notes, RefStateLive)
			frames := nexttest.FramesOf(rhWatch(t, notes.Member("notes")))
			counts := nexttest.FramesOf(rhWatch(t, notes.Member("count")))
			rig.Peer.Close()
			rhWaitState(t, notes, RefStateLoading)
			next := rawParent(t, rig.Client, link.PartLimits{})
			hold := bindNext(t, next, jsonObject("text", "a"), jsonObject("text", "b"))
			rhWaitCalls(t, frames, 1)
			rhWaitCalls(t, counts, 1)
			list := []any{map[string]any{"text": "a"}, map[string]any{"text": "b"}}
			rhEqual(t, "frames", frames.Items(), []any{map[string]any{"Value": list, "Ops": []any{[]any{"r", list}}}})
			rhEqual(t, "counts", counts.Items(), []any{map[string]any{"Value": 2, "Ops": []any{[]any{"r", 2}}}})
			// The fresh decoder: the provider's new encoder starts over, interning paths anew.
			next.Send(rhStateEntry(hold, []link.StateMember{{Member: editorWire(t).State["count"], Ops: link.Text(`[["r",3]]`)}}, nil, nil))
			rhWaitCalls(t, counts, 2)
			last := counts.Items()[counts.Len()-1]
			rhEqual(t, "last", []any{last.Value, last.Ops}, []any{3, []any{[]any{"r", 3}}})
		})

		// remote-object.test.ts: re-holds after a drop › a derive over two members of a re-held reference sees the snapshot whole: one batch
		t.Run("a derive over two members of a re-held reference sees the snapshot whole: one batch", func(t *testing.T) {
			rig := rhShiftingClient(t)
			sums := nexttest.FramesOf(rhWatch(t, rig.View.Member("sum")))
			rig.Peer.Close()
			rhWaitState(t, rig.Ref, RefStateLoading)
			next := rawParent(t, rig.Client, link.PartLimits{})
			rhBindShifting(t, next, "a", map[string]string{"x": "10", "y": "20"})
			rhWaitCalls(t, sums, 1)
			rhSync(t, next)
			nexttest.Delivered()
			var seen []any
			for _, frame := range sums.Items() {
				seen = append(seen, frame.Value)
			}
			rhEqual(t, "sums", seen, []any{30})
		})

		// remote-object.test.ts: re-holds after a drop › another variant: vanished members fail (watches stay open), derives over them fail, new members get values, parked calls are checked (race 14)
		t.Run("another variant: vanished members fail (watches stay open), derives over them fail, new members get values, parked calls are checked (race 14)", func(t *testing.T) {
			rig := rhShiftingClient(t)
			if sum := rhRefValue(t, rig.View, "sum"); sum != float64(3) {
				t.Fatalf("sum = %v, want 3", sum)
			}
			a := rig.Ref
			x := nexttest.ChangesOf(rhWatch(t, a.Member("x")))
			y := nexttest.FramesOf(rhWatch(t, a.Member("y")))
			sum := nexttest.ChangesOf(rhWatch(t, rig.View.Member("sum")))
			labels := nexttest.FramesOf(rhWatch(t, rig.View.Member("label")))
			rig.Peer.Close()
			rhWaitState(t, a, RefStateLoading)
			parked := rhGo(func() (delta.JsonValue, error) { return a.Call("m", nil, background) })
			next := rawParent(t, rig.Client, link.PartLimits{})
			rhBindShifting(t, next, "b", map[string]string{"y": "20", "z": "30"})
			rhWaitFor(t, func() bool {
				variant, _, _ := a.Variant()
				return variant == "b"
			})
			_, err := parked.wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"invalid","reason":"member","message":"app.shifting (variant b) has no method \"m\""}`; got != want {
				t.Fatalf("parked = %s, want %s", got, want)
			}
			rhWaitCalls(t, x, 1)
			rhWaitCalls(t, sum, 1)
			rhWaitCalls(t, y, 1)
			rhWaitCalls(t, labels, 1)
			vanished := func(call chord.WatchCall[delta.JsonValue]) {
				t.Helper()
				chordErr, ok := data.IsChordErrorCode(call.Error, data.CodeInvalid)
				if call.Kind != chord.WatchCallFailed || !ok || chordErr.Reason != data.ReasonMember {
					t.Fatalf("call = %+v, want failed invalid/member", call)
				}
			}
			if x.Len() != 1 || sum.Len() != 1 {
				t.Fatalf("x %d calls, sum %d calls; want 1, 1", x.Len(), sum.Len())
			}
			vanished(x.Items()[0])
			vanished(sum.Items()[0])
			rhEqual(t, "y", []any{y.Items()[0].Value, y.Items()[0].Ops}, []any{20, []any{[]any{"r", 20}}})
			// The variant changed in the same batch: the derive recomputed with the new one.
			var texts []any
			for _, frame := range labels.Items() {
				texts = append(texts, frame.Value)
			}
			rhEqual(t, "labels", texts, []any{"b:20"})
			rhEqual(t, "z", rhRefValue(t, a, "z"), 30)
			if _, err := a.Member("x").Value(); err == nil || !strings.Contains(err.Error(), `app.shifting (variant b) has no state member "x"`) {
				t.Fatalf("x.value error = %v", err)
			}
			// A later binding of the variant that has `x` brings it back in the same watches; the derive heals.
			next.Close()
			rhWaitState(t, a, RefStateLoading)
			third := rawParent(t, rig.Client, link.PartLimits{})
			rhBindShifting(t, third, "a", map[string]string{"x": "5", "y": "6"})
			rhWaitFor(t, func() bool { return rhRefState(a) == RefStateLive && sum.Len() >= 2 && x.Len() >= 2 })
			last := x.Items()[x.Len()-1]
			rhEqual(t, "x last", []any{last.Kind, last.Value, last.Ops, last.Retired}, []any{"value", 5, []any{[]any{"r", 5}}, false})
			rhEqual(t, "view.sum", rhRefValue(t, rig.View, "sum"), 11)
			lastSum := sum.Items()[sum.Len()-1]
			rhEqual(t, "sum last", []any{lastSum.Kind, lastSum.Value, lastSum.Ops, lastSum.Retired}, []any{"value", 11, []any{[]any{"r", 11}}, false})
		})

		// remote-object.test.ts: re-holds after a drop › calls sent before a drop fail unavailable, never resent; an old link's leftovers never reach the next (race 1)
		t.Run("calls sent before a drop fail unavailable, never resent; an old link's leftovers never reach the next (race 1)", func(t *testing.T) {
			rig := rhProvider(t)
			notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			bindNext(t, rig.Peer)
			cancellable, cancel := chordctx.WithCancel(background)
			sent := rhGo(func() (delta.JsonValue, error) { return notes.Call("wait", nil, cancellable) })
			rig.Peer.Next("call")
			rig.Peer.Close()
			_, err := sent.wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"unavailable","message":"the link closed"}`; got != want {
				t.Fatalf("rejection = %s, want %s", got, want)
			}
			next := rawParent(t, rig.Client, link.PartLimits{})
			hold := bindNext(t, next)
			rhWaitState(t, notes, RefStateLive)
			cancel(fmt.Errorf("late"))
			notes.Release()
			release := next.Next("release").(*link.ReleaseMessage)
			if release.Hold != hold {
				t.Fatalf("release = %+v, want hold %d", release, hold)
			}
			rhKindsAre(t, next, "ack")
		})

		// remote-object.test.ts: re-holds after a drop › a drop before bound, or during the next link's handshake, leaves it loading; the next connect holds again (race 5)
		t.Run("a drop before bound, or during the next link's handshake, leaves it loading; the next connect holds again (race 5)", func(t *testing.T) {
			rig := rhProvider(t)
			notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			rig.Peer.Next("hold")
			rig.Peer.Close()
			a, b := link.MemoryLinkPair()
			must(t, rig.Client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			// The peer never answers the handshake; the hold waits in the channel, then the link drops.
			b.Close()
			transported(a, b)
			if kind := rhRefState(notes); kind != RefStateLoading {
				t.Fatalf("state = %v, want loading", kind)
			}
			bindNext(t, rawParent(t, rig.Client, link.PartLimits{}))
			rhWaitState(t, notes, RefStateLive)
		})

		// remote-object.test.ts: re-holds after a drop › a re-hold refused after a provider upgrade stays failed; the next connect holds it again
		t.Run("a re-hold refused after a provider upgrade stays failed; the next connect holds it again", func(t *testing.T) {
			notesV2 := mustContract(contracts.Remote("app.notes", contracts.RemoteDeclaration{
				Version: 2, Params: contracts.Params{contracts.Prop("session", contracts.Str())}, HostAddress: exServerAddress,
				Variants: []contracts.VariantEntry{contracts.Var("editor", contracts.VariantDeclaration{
					State: []contracts.Property{rhProp("count", contracts.Int())},
				})},
			}))
			hosts := linked(t, func(host FacetHost) error { _, err := serveNotes(host, nil); return err }, linkedOptions{})
			upgraded := rhCreate(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
			must(t, upgraded.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
				return host.Serve(notesV2, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Variant: "editor", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"count": rhConst(float64(0))},
					}}, nil
				}, ServeOptions{})
			})}}, background))
			notes, err := hosts.Client.Get(Notes, rhNotes("S1"), background, GetOptions{})
			must(t, err)
			// relink drops the client's link and connects it to `to`.
			relink := func(to Host, from *link.MemoryLink) *link.MemoryLink {
				from.Close()
				transported(from)
				a, b := link.MemoryLinkPair()
				must(t, to.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
				must(t, hosts.Client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
				transported(a, b)
				return a
			}
			toUpgraded := relink(upgraded, hosts.Links[0])
			rhWaitFor(t, func() bool { return rhRefState(notes) == RefStateFailed })
			state := notes.State().Value()
			if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Code != data.CodeContractMismatch || state.RetryAt != nil {
				t.Fatalf("state = %+v", state)
			}
			notes.Retry()
			transported(toUpgraded)
			if kind := rhRefState(notes); kind != RefStateFailed {
				t.Fatalf("state = %v, want failed", kind)
			}
			relink(hosts.Server, toUpgraded)
			rhWaitState(t, notes, RefStateLive)
		})

		// remote-object.test.ts: re-holds after a drop › ref.retry() of a broken failure sends retry on the same hold: no release, no new hold (audit)
		t.Run("ref.retry() of a broken failure sends retry on the same hold: no release, no new hold (audit)", func(t *testing.T) {
			rig := rhProvider(t)
			notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
			cause := data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: "not authorized"}, nil)
			rig.Peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "broken", Error: rhWireError(cause)}})
			rhWaitState(t, notes, RefStateFailed)
			if kind := notes.State().Value().Cause.Kind; kind != data.FailureBroken {
				t.Fatalf("cause = %v, want broken", kind)
			}
			notes.Retry()
			retry := rig.Peer.Next("retry").(*link.RetryMessage)
			if retry.Hold != hold || retry.Operation == "" {
				t.Fatalf("retry = %+v", retry)
			}
			bindHold(t, rig.Peer, hold)
			rhWaitState(t, notes, RefStateLive)
			rhSync(t, rig.Peer)
			for _, kind := range rig.Peer.Kinds() {
				if kind == "hold" || kind == "release" {
					t.Fatalf("unexpected %s", kind)
				}
			}
		})

		// remote-object.test.ts: re-holds after a drop › ref.retry() of an object whose factory threw, held twice: failed, loading, live; a call made then succeeds (audit)
		t.Run("ref.retry() of an object whose factory threw, held twice: failed, loading, live; a call made then succeeds (audit)", func(t *testing.T) {
			flaky := mustContract(contracts.Remote("app.flaky", contracts.RemoteDeclaration{
				Version: 1, HostAddress: exServerAddress,
				Variants: []contracts.VariantEntry{contracts.Var("only", contracts.VariantDeclaration{
					Methods: []contracts.MethodEntry{contracts.Meth("ping", contracts.NewMethod())},
				})},
			}))
			var mu sync.Mutex
			factories := 0
			hosts := linked(t, func(host FacetHost) error {
				return host.Serve(flaky, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					mu.Lock()
					factories++
					n := factories
					mu.Unlock()
					if n == 1 {
						return contracts.Implementation{}, fmt.Errorf("bad factory")
					}
					return contracts.Implementation{Variant: "only", Members: contracts.VariantImplementation{
						Methods: map[string]contracts.MethodFunction{
							"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
						},
					}}, nil
				}, ServeOptions{})
			}, linkedOptions{})
			one := rhHoldRef(t, hosts.Client, flaky, jsonObject())
			two := rhHoldRef(t, hosts.Client, flaky, jsonObject())
			rhWaitState(t, one, RefStateFailed)
			var states []string
			var called *rhAsync[string]
			var statesMu sync.Mutex
			watch, err := one.State().Watch(background)
			must(t, err)
			states = append(states, string(watch.Value().Kind))
			watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
				if call.Kind != chord.WatchCallValue {
					return nil
				}
				statesMu.Lock()
				defer statesMu.Unlock()
				states = append(states, string(call.Value.Kind))
				// A call made while it loads again waits for the binding; none sees the old failure.
				if call.Value.Kind == RefStateLoading && called == nil {
					called = rhGo(func() (string, error) {
						if _, err := one.Call("ping", nil, background); err != nil {
							return "rejected: " + err.Error(), nil
						}
						return "ok", nil
					})
				}
				return nil
			})
			one.Retry()
			rhWaitState(t, one, RefStateLive)
			rhWaitState(t, two, RefStateLive)
			statesMu.Lock()
			wait := called
			got := slices.Clone(states)
			statesMu.Unlock()
			if wait == nil {
				t.Fatal("no call was made while the reference loaded")
			}
			if result := wait.must(t); result != "ok" {
				t.Fatalf("call = %s, want ok", result)
			}
			if !slices.Equal(got, []string{"failed", "loading", "live"}) {
				rhWaitFor(t, func() bool { statesMu.Lock(); defer statesMu.Unlock(); return len(states) >= 3 })
				statesMu.Lock()
				got = slices.Clone(states)
				statesMu.Unlock()
			}
			if !slices.Equal(got, []string{"failed", "loading", "live"}) {
				t.Fatalf("states = %v, want [failed loading live]", got)
			}
			mu.Lock()
			defer mu.Unlock()
			if factories != 2 {
				t.Fatalf("factories = %d, want 2", factories)
			}
		})

		// remote-object.test.ts: re-holds after a drop › a drop while failed: loading, held again on the next link (race 19)
		t.Run("a drop while failed: loading, held again on the next link (race 19)", func(t *testing.T) {
			rig := rhProvider(t)
			notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
			cause := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "no configuration"}, nil)
			rig.Peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "broken", Error: rhWireError(cause)}})
			rhWaitState(t, notes, RefStateFailed)
			rig.Peer.Close()
			rhWaitState(t, notes, RefStateLoading)
			bindNext(t, rawParent(t, rig.Client, link.PartLimits{}))
			rhWaitState(t, notes, RefStateLive)
		})

		// remote-object.test.ts: re-holds after a drop › a member watch before the first binding waits for it, and rejects when it fails or is released (race 16)
		t.Run("a member watch before the first binding waits for it, and rejects when it fails or is released (race 16)", func(t *testing.T) {
			rig := rhProvider(t)
			bound := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			failing := rhHoldRef(t, rig.Client, Notes, rhNotes("S2"))
			released := rhHoldRef(t, rig.Client, Notes, rhNotes("S3"))
			watchOf := func(ref *Reference) *rhAsync[chord.WatchHandle[delta.JsonValue]] {
				return rhGo(func() (chord.WatchHandle[delta.JsonValue], error) { return ref.Member("count").Watch(background) })
			}
			watches := []*rhAsync[chord.WatchHandle[delta.JsonValue]]{watchOf(bound), watchOf(failing), watchOf(released)}
			bindNext(t, rig.Peer, jsonObject("text", "a"))
			hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
			offline := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "offline"}, nil)
			rig.Peer.Send(&link.FailedMessage{
				Hold: hold, Cause: link.WireCause{Kind: "transient", Error: rhWireError(offline)}, RetryIn: link.Some(1000),
			})
			released.Release()
			first := watches[0].must(t)
			rhEqual(t, "bound count", first.Value(), 1)
			_, err := watches[1].wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"unavailable","message":"offline"}`; got != want {
				t.Fatalf("failing = %s, want %s", got, want)
			}
			_, err = watches[2].wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"released","message":"app.notes: released"}`; got != want {
				t.Fatalf("released = %s, want %s", got, want)
			}
		})
	})
}

// --- a get across a drop ---------------------------------------------------------------------------------------------

func TestRemoteObjectAGetAcrossADrop(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: a get across a drop › keeps waiting within its one deadline, from the moment it was called
		t.Run("keeps waiting within its one deadline, from the moment it was called", func(t *testing.T) {
			virtual := newRhVirtual()
			client := rhCreate(t, HostOptions{OnError: func(HostError) {}, loop: virtual.loop})
			must(t, client.Load(nil, background))
			peer := rawParent(t, client, link.PartLimits{})
			timeout := time.Second
			got := rhGo(func() (*Reference, error) {
				return client.Get(Notes, rhNotes("S1"), background, GetOptions{ReadyTimeout: &timeout})
			})
			late := rhGo(func() (*Reference, error) {
				return client.Get(Notes, rhNotes("S2"), background, GetOptions{ReadyTimeout: &timeout})
			})
			peer.Next("hold")
			peer.Next("hold")
			virtual.advance(400)
			peer.Close()
			rhSync2(t, virtual)
			next := rawParent(t, client, link.PartLimits{})
			bindNext(t, next, jsonObject("text", "a"))
			ref := got.must(t)
			rhEqual(t, "count", rhRefValue(t, ref, "count"), 1)
			virtual.advance(600)
			_, err := late.wait()
			if got, want := rhJSON(t, rejection(t, err)), `{"code":"unavailable","message":"app.notes(S2): did not become live within 1000 ms"}`; got != want {
				t.Fatalf("late = %s, want %s", got, want)
			}
		})
	})
}

// rhSync2 lets the host's loop run what the closing link queued: a barrier section.
func rhSync2(t testing.TB, virtual *rhVirtual) {
	t.Helper()
	virtual.loop.barrier()
}

// --- an activating dependent whose remote dependency's link drops ----------------------------------------------------

func TestRemoteObjectActivatingDependentWhoseLinkDrops(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		facet := DefineFacet(func(host FacetHost, _ FacetSelf) error {
			return host.Serve(rhViewOfNotes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"count": Expose(self.Get(Notes, rhNotes("S1")).Member("count"))},
				}}, nil
			}, ServeOptions{})
		})

		// remote-object.test.ts: an activating dependent whose remote dependency's link drops (audit; supersedes race 18) › stays loading through the drop, no failure reported, and goes live when the next link binds it
		t.Run("stays loading through the drop, no failure reported, and goes live when the next link binds it", func(t *testing.T) {
			virtual := newRhVirtual()
			errs := &hostErrors{}
			client := rhCreate(t, HostOptions{OnError: errs.push, loop: virtual.loop})
			peer := rawParent(t, client, link.PartLimits{})
			loading := rhGo(func() (struct{}, error) {
				return struct{}{}, client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background)
			})
			peer.Next("hold")
			peer.Close()
			rhSync2(t, virtual)
			view := rhHoldRef(t, client, rhViewOfNotes, jsonObject())
			if kind := rhRefState(view); kind != RefStateLoading {
				t.Fatalf("state = %v, want loading", kind)
			}
			next := rawParent(t, client, link.PartLimits{})
			if hold := bindNext(t, next, jsonObject("text", "a")); hold != 0 {
				t.Fatalf("hold = %d, want 0", hold)
			}
			loading.must(t)
			rhWaitState(t, view, RefStateLive)
			if errs.Len() != 0 {
				t.Fatalf("errors = %v", errs.All())
			}
		})

		// remote-object.test.ts: an activating dependent whose remote dependency's link drops (audit; supersedes race 18) › dropped and never bound again, stays parked loading: nothing fails, no parked_long without a link
		t.Run("dropped and never bound again, stays parked loading: nothing fails, no parked_long without a link", func(t *testing.T) {
			virtual := newRhVirtual()
			errs := &hostErrors{}
			timeout := time.Second
			client := rhCreate(t, HostOptions{ReadyTimeout: &timeout, OnError: errs.push, loop: virtual.loop})
			peer := rawParent(t, client, link.PartLimits{})
			loading := rhGo(func() (struct{}, error) {
				return struct{}{}, client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background)
			})
			peer.Next("hold")
			// A remote blocker ends the start barrier's first pass.
			loading.must(t)
			peer.Close()
			rhSync2(t, virtual)
			virtual.advance(10_000)
			// No remote attempt answers for it while its path has no link (fix round 1, Opus F2).
			if errs.Len() != 0 {
				t.Fatalf("errors = %v", errs.All())
			}
			if kind := rhRefState(rhHoldRef(t, client, rhViewOfNotes, jsonObject())); kind != RefStateLoading {
				t.Fatalf("state = %v, want loading", kind)
			}
		})

		// remote-object.test.ts: an activating dependent whose remote dependency's link drops (audit; supersedes race 18) › without any link stays loading past its readyTimeout and goes live when the first link binds its one hold
		t.Run("without any link stays loading past its readyTimeout and goes live when the first link binds its one hold", func(t *testing.T) {
			virtual := newRhVirtual()
			errs := &hostErrors{}
			timeout := time.Second
			client := rhCreate(t, HostOptions{ReadyTimeout: &timeout, OnError: errs.push, loop: virtual.loop})
			must(t, client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background))
			virtual.advance(500)
			if failed := failures(errs.All()); len(failed) != 0 {
				t.Fatalf("failures = %v", failed)
			}
			next := rawParent(t, client, link.PartLimits{})
			if hold := bindNext(t, next, jsonObject("text", "a")); hold != 0 {
				t.Fatalf("hold = %d, want 0", hold)
			}
			one := time.Millisecond
			view, err := client.Get(rhViewOfNotes, jsonObject(), background, GetOptions{ReadyTimeout: &one})
			must(t, err)
			rhEqual(t, "count", rhRefValue(t, view, "count"), 1)
			rhSync(t, next)
			for _, kind := range next.Kinds() {
				if kind == "hold" || kind == "release" {
					t.Fatalf("unexpected %s", kind)
				}
			}
		})
	})
}

// --- GC across a drop ------------------------------------------------------------------------------------------------

func TestRemoteObjectGCAcrossADrop(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// remote-object.test.ts: GC across a drop › a reference that lived through a drop keeps nothing of the closed link
		t.Run("a reference that lived through a drop keeps nothing of the closed link", func(t *testing.T) {
			rig := rhProvider(t)
			notes := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			bindNext(t, rig.Peer)
			closedLink, closedPeer := func() (weak.Pointer[link.MemoryLink], weak.Pointer[RawPeer]) {
				a, b := link.MemoryLinkPair()
				rig.Peer.Close()
				rhWaitState(t, notes, RefStateLoading)
				must(t, rig.Client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
				second := newRawPeer(t, b, rawPeerOptions{})
				second.Next("hello")
				bindNext(t, second)
				rhWaitState(t, notes, RefStateLive)
				second.Close()
				rhWaitState(t, notes, RefStateLoading)
				return weak.Make(a), weak.Make(second)
			}()
			bindNext(t, rawParent(t, rig.Client, link.PartLimits{}))
			nexttest.ExpectCollected(t, closedLink)
			nexttest.ExpectCollected(t, closedPeer)
			rhWaitState(t, notes, RefStateLive)
		})
	})
}
