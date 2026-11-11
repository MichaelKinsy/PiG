package host

// pi: packages/chord/test/examples/12-routes.ts

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 12-routes.ts --------------------------------------------------------------------------------------------------

func TestExample12Routes(t *testing.T) {
	notes := exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exSessionAddress,
		contracts.Var("editor", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("notes", exArr(exStr()))},
			Methods: []contracts.MethodEntry{
				contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt())),
				contracts.Meth("whoami", contracts.NewMethod(contracts.Schema{}, exStr())),
			},
		}),
		contracts.Var("reader", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("notes", exArr(exStr()))},
			Methods: []contracts.MethodEntry{contracts.Meth("whoami", contracts.NewMethod(contracts.Schema{}, exStr()))},
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
					return exAdd(notesDoc, args.Value("text"), ctx)
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
	mustRoute(t)(server.Route(Session, RouteOptions{
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
			go func() { _, _ = worker.Load([]HostFacet{{Name: "@example/worker", Facet: workerFacet}}, background) }()
			resolveMu.Lock()
			workerLink = up
			resolveMu.Unlock()
			return ResolvedLink{Link: up, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	mustLoad(t)(server.Load([]HostFacet{{Name: "@example/server", Facet: serverFacet}}, background))
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
	_, err := guest.Host.Get(background, notes, jsonObject("session", "S2"), GetOptions{})
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
