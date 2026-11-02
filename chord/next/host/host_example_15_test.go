package host

// pi: packages/chord/test/next/examples/15-revocation.ts

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

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
	mustRoute(t)(server.Route(Session, RouteOptions{
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			id, _ := address.Params.Value("id").(string)
			return principalKind(principal) == "owner" || (principalKind(principal) == "guest" && principal.Value("session") == id), nil
		},
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			up, down := link.MemoryLinkPair()
			id, _ := address.Params.Value("id").(string)
			worker := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), RevokedBy: revokedBy, OnError: errs.push})
			must(t, worker.Connect(up, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
			go func() { _, _ = worker.Load([]HostFacet{{Name: "@example/notes", Facet: notesFacet}}, background) }()
			workersMu.Lock()
			workers = append(workers, worker)
			workersMu.Unlock()
			return ResolvedLink{Link: down, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	mustLoad(t)(server.Load(nil, background))
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
