package host

// pi: packages/chord/test/examples/10-drops.ts

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 10-drops.ts ---------------------------------------------------------------------------------------------------

func TestExample10Drops(t *testing.T) {
	notes := exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exServerAddress, contracts.Var("editor", contracts.VariantDeclaration{
		State: []contracts.Property{exProp("notes", exArr(exStr()))},
		Methods: []contracts.MethodEntry{
			contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt())),
			contracts.Meth("slow", contracts.NewMethod()),
		},
	}))
	doc := exStringListDoc("notes")
	// Lingers for a minute after its last reference: a client that reconnects within it finds its notes again.
	linger := 60 * time.Second
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{
				"notes": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return exAdd(notesDoc, args.Value("text"), ctx)
				}),
				"slow": exBlocked(nil),
			}), nil
		}, ServeOptions{Linger: &linger})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/notes", Facet: facet})
	clientErrors := &hostErrors{}
	client := newHost(t, HostOptions{OnError: clientErrors.push})
	mustLoad(t)(client.Load(nil, background))
	connect := func() *link.MemoryLink { return exTwoPartyHosts(t, server, client, ownerPrincipal()) }
	held := exHold(t, client, notes, jsonObject("session", "S1"))
	if exStateNow(t, held) != "loading" || exVariantName(held) != "" {
		t.Fatalf("held without a link: %s, variant %q", exStateNow(t, held), exVariantName(held))
	}
	watch, kinds := exStateKinds(t, held)
	type result struct {
		value any
		err   error
	}
	parked := make(chan result, 1)
	go func() {
		value, err := held.Call("add", jsonObject("text", "written while loading"), background)
		parked <- result{value, err}
	}()
	scopeEventually(t, "the call parked", func() bool { return exParked(client, held) == 1 })
	up := connect()
	if got := <-parked; got.err != nil || got.value != float64(1) {
		t.Fatalf("parked call's result once live: %v, %v", got.value, got.err)
	}
	exEqual(t, "notes", exValue(t, held, "notes"), exJSON(t, `["written while loading"]`))
	if exVariantName(held) != "editor" {
		t.Fatalf("variant = %q", exVariantName(held))
	}
	inFlight := make(chan error, 1)
	go func() { _, err := held.Call("slow", nil, background); inFlight <- err }()
	scopeEventually(t, "the call is running on the server", func() bool { return exActivity(t, server).Calls == 1 })
	up.Close()
	if err := <-inFlight; err == nil {
		t.Fatal("in-flight call: resolved")
	}
	scopeEventually(t, "the drop shows as loading", func() bool { return exStateNow(t, held) == "loading" })
	exEqual(t, "last notes", exValue(t, held, "notes"), exJSON(t, `["written while loading"]`))
	first := make(chan result, 1)
	second := make(chan result, 1)
	go func() {
		value, err := held.Call("add", jsonObject("text", "first while down"), background)
		first <- result{value, err}
	}()
	scopeEventually(t, "the first call parked", func() bool { return exParked(client, held) == 1 })
	go func() {
		value, err := held.Call("add", jsonObject("text", "second while down"), background)
		second <- result{value, err}
	}()
	scopeEventually(t, "the second call parked", func() bool { return exParked(client, held) == 2 })
	_, frames := exWatch(t, background, held.Member("notes"))
	connect()
	if got := <-first; got.err != nil || got.value != float64(2) {
		t.Fatalf("first parked result: %v, %v", got.value, got.err)
	}
	if got := <-second; got.err != nil || got.value != float64(3) {
		t.Fatalf("second parked result: %v, %v", got.value, got.err)
	}
	scopeEventually(t, "the re-hold's frames", func() bool { return len(frames.all()) > 0 })
	scopeEventually(t, "notes after the re-hold", func() bool {
		return exJSONEqual(exValue(t, held, "notes"), exJSON(t, `["written while loading","first while down","second while down"]`))
	})
	held.Release()
	<-watch.Closed()
	if got := exStateNow(t, held); got != "ended" {
		t.Fatalf("state after release = %s", got)
	}
	if got := kinds.all(); len(got) == 0 || got[0] != "live" {
		t.Fatalf("ref.state frames = %v", got)
	}
	exCloseHost(t, client)
	exCloseHost(t, server)
	if clientErrors.Len() > 0 {
		t.Fatalf("client errors: %v", clientErrors.All())
	}
}
