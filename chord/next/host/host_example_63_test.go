package host

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Twin of packages/chord/test/examples/63-optimistic-answers.ts: a call's answer follows its state (decision of 2026-11-05).
// A session's worker renames its session and pushes the new title to the server's index inside the method; a slow client
// holds the index and calls `rename` (relayed by the server to the worker). The server's window for the client is full, so its
// index change waits; before the server forwards the worker's answer it sends that waiting state, so when the call settles in
// the client the index already has the new title. The terminal half of the example (`TuiScope.optimistic`, the redraws a
// component records) is not in this module: what it shows is the client's index at each moment, checked here directly.

func TestExample63OptimisticAnswers(t *testing.T) {
	report := contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()))
	sessionIndex := mustContract(declareLocal("example.session-index", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("titles", contracts.Map(contracts.Str())),
			contracts.Mem("report", contracts.NewMethod(report)),
		},
	}))
	sessions := mustContract(contracts.Remote("example.sessions", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("all", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("titles", contracts.Map(contracts.Str()))},
			Methods: []contracts.MethodEntry{contracts.Meth("report", contracts.NewMethod(report))},
		})},
	}))
	sessionTitle := mustContract(contracts.Remote("example.session-title", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			return Session.Address(jsonObject("id", params.Value("id"))), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("title", contracts.Str())},
			Methods: []contracts.MethodEntry{contracts.Meth("rename", contracts.NewMethod(contracts.Str()))},
		})},
	}))
	indexDoc, err := documents.DefineState("index", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("titles", contracts.Map(contracts.Str()))), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("titles", jsonObject()) },
	})
	must(t, err)
	titleDoc, err := documents.DefineState("title", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("title", contracts.Str())), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("title", "") },
	})
	must(t, err)

	rel := relay(t, relayedOptions{
		Server: func(h FacetHost) error {
			if err := h.Serve(sessionIndex, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				doc := stateOf(self, indexDoc)
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"titles": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
							return values[0].(*delta.JsonObject).Value("titles"), nil
						}),
					},
					Methods: map[string]contracts.MethodFunction{"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						input := args.(*delta.JsonObject)
						return nil, doc.Change(func(draft *delta.Object) {
							_ = draft.Object("titles").Set(input.Value("id").(string), input.Value("title"))
						}, ctx)
					}},
				}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(sessions, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				index := self.Get(sessionIndex, jsonObject())
				return contracts.Implementation{Variant: "all", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"titles": self.Derive([]any{index.Member("titles")}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
					Methods: map[string]contracts.MethodFunction{"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return index.Call("report", args, ctx)
					}},
				}}, nil
			}, ServeOptions{})
		},
		Worker: func(h FacetHost) error {
			return h.Serve(sessionTitle, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				doc := stateOf(self, titleDoc)
				held := self.Get(sessions, jsonObject())
				id := params.Value("id")
				return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"title": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
							return values[0].(*delta.JsonObject).Value("title"), nil
						}),
					},
					// The rule for a method that should feel instant: it awaits its own changes, the push to the server included.
					Methods: map[string]contracts.MethodFunction{"rename": func(title delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						if err := doc.Change(func(draft *delta.Object) { _ = draft.Set("title", title) }, ctx); err != nil {
							return nil, err
						}
						return held.Call("report", jsonObject("id", id, "title", title), ctx)
					}},
				}}, nil
			}, ServeOptions{})
		},
	})
	// A terminal client on a link that can stall.
	tab := newHost(t, HostOptions{OnError: func(HostError) {}})
	a, b := link.MemoryLinkPair()
	must(t, rel.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	slow := newPausableLink(b)
	must(t, tab.Connect(slow, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	mustLoad(t)(tab.Load(nil, background))
	flush := func() {
		flushOwn(rel)
		transported(a, b)
	}
	index, err := tab.Get(background, sessions, jsonObject(), GetOptions{})
	must(t, err)
	session, err := tab.Get(background, sessionTitle, jsonObject("id", "S1"), GetOptions{})
	must(t, err)
	titleOf := func(id string) any { return memberValue(t, index, "titles").(*delta.JsonObject).Value(id) }
	if _, err := session.Call("rename", "Draft", background); err != nil {
		t.Fatalf("rename Draft: %v", err)
	}
	flush()
	if got := titleOf("S1"); got != "Draft" {
		t.Fatalf("the index shows %v, want Draft", got)
	}

	// The client stalls (it acknowledges nothing). Other sessions report to the index: two `state` messages fill the server's
	// window for this client, the third change waits there.
	slow.pause()
	slow.forget()
	server := mustGet(t, rel.Server, sessionIndex, jsonObject())
	for _, id := range []string{"S2", "S3", "S4"} {
		if _, err := server.Call("report", jsonObject("id", id, "title", "session "+id), background); err != nil {
			t.Fatalf("report %s: %v", id, err)
		}
	}
	flush()
	eventually(t, "the two states that fill the server's window reached the stalled client", func() bool { return len(slow.seen()) >= 2 })
	requireStrings(t, slow.seen(), []string{"state", "state"})

	// The user renames S1: the worker renames, pushes the title to the index (whose change waits in the server's window too)
	// and answers. The server sends its waiting state before it forwards the answer, past its window. The call runs on a
	// goroutine, so what reaches the stalled link is awaited: the answer is its last message (the five messages Pi's example
	// logs once the client resumes: the two states, the worker's title state, the waiting state, the answer).
	settled := make(chan any, 1)
	go func() {
		_, err := session.Call("rename", "Renamed", background)
		if err != nil {
			settled <- err
			return
		}
		settled <- titleOf("S1")
	}()
	eventually(t, "the answer reached the stalled client", func() bool { return slices.Contains(slow.seen(), "result") })
	flush()
	requireStrings(t, slow.seen(), []string{"state", "state", "state", "state", "result"})
	select {
	case got := <-settled:
		t.Fatalf("the call settled (%v) while the client held its answer", got)
	default:
	}
	if got := titleOf("S1"); got != "Draft" {
		t.Fatalf("while the call is pending the index shows %v, want Draft", got)
	}

	// The client catches up: the states, then the answer. When the call settles the index has the new title, so the index
	// never shows "Draft" again (an answer that overtook the waiting state showed it once more).
	slow.resume()
	flush()
	flush()
	if got := receive(t, settled); got != "Renamed" {
		t.Fatalf("when the call settled the index showed %v, want Renamed", got)
	}
	seen := slow.seen()
	// The waiting state went out before the answer.
	if answer := slices.Index(seen, "result"); answer < 0 || slices.Contains(seen[answer+1:], "state") {
		t.Fatalf("messages = %v, want the answer after every state", seen)
	}
	if got := titleOf("S1"); got != "Renamed" {
		t.Fatalf("the index shows %v, want Renamed", got)
	}
}
