package host

// pi: packages/chord/test/examples/08-remote.ts

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

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
	mustLoad(t)(client.Load(nil, background))
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
