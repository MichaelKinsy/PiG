package host

// pi: packages/chord/test/next/examples/31-parked-dependencies.ts

import (
	"context"
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

// --- 31-parked-dependencies.ts -------------------------------------------------------------------------------------

func TestExample31ParkedDependencies(t *testing.T) {
	// The server's notes (the notes plugin), placed on the server.
	notes := exRemote("example.notes", contracts.Params{}, exServerAddress, contracts.Var("editor", contracts.VariantDeclaration{
		State:   []contracts.Property{exProp("list", exArr(exStr()))},
		Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr()))))},
	}))
	// A panel in the tab: a local object over the server's notes.
	panel := exLocal("example.panel", contracts.Params{exProp("id", exInt())}, contracts.Mem("count", exInt()))
	// A local object the panel's sibling needs, which no plugin of the tab serves.
	theme := exLocal("example.theme", contracts.Params{}, contracts.Mem("name", exStr()))
	header := exLocal("example.header", contracts.Params{exProp("id", exInt())}, contracts.Mem("title", exStr()))
	list := exStringListDoc("example.list")
	// The notes plugin's server facet.
	notesPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, list)
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{
				"list": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { exPush(draft, args.Value("text")) }, ctx)
				}),
			}), nil
		}, ServeOptions{})
	})
	// startServer is a server with the given plugins, its link to the tab accepted.
	startServer := func(plugins ...HostFacet) (Host, *link.MemoryLink, *link.MemoryLink) {
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
		mustLoad(t)(server.Load(plugins, background))
		up, down := link.MemoryLinkPair()
		must(t, server.Accept(up, LinkOptions{Principal: ownerPrincipal()}, background))
		return server, down, up
	}
	// The tab: its panel declares the notes (a dependency); its header a theme nobody serves.
	var panels int
	var panelMu sync.Mutex
	tabErrors := &hostErrors{}
	tab := newHost(t, HostOptions{OnError: tabErrors.push})
	mustLoad(t)(tab.Load([]HostFacet{{Name: "@example/panel", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(panel, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			panelMu.Lock()
			panels++
			panelMu.Unlock()
			remote := self.Get(notes, jsonObject())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"count": self.Derive([]any{remote.Member("list")}, func(values []any) (delta.JsonValue, error) {
					return float64(len(values[0].([]any))), nil
				}),
			}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(header, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"title": Expose(self.Get(theme, jsonObject()).Member("name"))}, nil), nil
		}, ServeOptions{})
	})}}, background))
	// 1. The first server has no notes plugin. Its answer is `failed { waiting }`: not the panel's fault.
	first, firstLink, firstUp := startServer()
	must(t, tab.Connect(firstLink, LinkOptions{Principal: serverPrincipal()}, background))
	panelRef := exHold(t, tab, panel, jsonObject("id", float64(1)))
	watch, kinds := exStateKinds(t, panelRef)
	scopeEventually(t, "panel parked waiting", func() bool { return strings.HasPrefix(exStateNow(t, panelRef), "failed, waiting") })
	// 2. A local type nobody serves here is the same answer: the request waits unbound, indexed under the type's name.
	headerRef := exHold(t, tab, header, jsonObject("id", float64(1)))
	scopeEventually(t, "header parked waiting", func() bool { return strings.HasPrefix(exStateNow(t, headerRef), "failed, waiting") })
	// 3. A getter waits through `waiting` up to its readyTimeout, then rejects with the cause (a hold just shows it).
	timeout := 200 * time.Millisecond
	if _, err := tab.Get(background, panel, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: &timeout}); err == nil {
		t.Fatal("get gave up: resolved")
	}
	// 4. Time passes: no timer runs for the parked panel, its factory is not run again.
	panelMu.Lock()
	if panels != 1 || !strings.HasPrefix(exStateNow(t, panelRef), "failed, waiting") {
		t.Fatalf("still parked: %s, factory runs: %d", exStateNow(t, panelRef), panels)
	}
	panelMu.Unlock()
	// 5. The server restarts with the notes plugin (the link drop shows as loading); the tab reconnects: its request is
	// held again and binds, and the parked panel goes on.
	firstUp.Close()
	second, secondLink, _ := startServer(HostFacet{Name: "@example/notes", Facet: notesPlugin})
	scopeEventually(t, "the tab's parent link closed", func() bool {
		return tab.Connect(secondLink, LinkOptions{Principal: serverPrincipal()}, background) == nil
	})
	live := exGet(t, tab, panel, jsonObject("id", float64(1)))
	exEqual(t, "panel live, count", exValue(t, live, "count"), float64(0))
	// Pi's `await tab.get(...)` and the awaits after it run the queued microtasks, so the `live` frame the panel's state
	// watch was advanced with has reached its listener before the references are released. A release ends a watch and drops
	// the frames it has not delivered (ref-state.ts `released`: `watch.end`; watch.ts `#terminate`: "drop what is pending").
	// The read above ran in a section of the tab, so the section that made the panel live has advanced every frame.
	deliveredOn(tab)
	panelMu.Lock()
	if panels != 1 {
		t.Fatalf("factory runs: %d", panels)
	}
	panelMu.Unlock()
	// The panel's notes are the owner's object on the server (a remote object per principal): the tab adds through its own.
	notesRef := exGet(t, tab, notes, jsonObject())
	exCall(t, notesRef, "add", jsonObject("text", "first"))
	scopeEventually(t, "panel count", func() bool { return exValue(t, live, "count") == float64(1) })
	headerRef.Release()
	panelRef.Release()
	live.Release()
	notesRef.Release()
	<-watch.Closed()
	if got := kinds.all(); len(got) == 0 || got[len(got)-1] != "ended" && !slices_Contains(got, "live") {
		t.Fatalf("panel states = %v", got)
	}
	exCloseHost(t, tab)
	exCloseHost(t, second)
	exCloseHost(t, first)
	if tabErrors.Len() > 0 {
		for _, err := range tabErrors.All() {
			if err.Kind != ErrParkedLong {
				t.Fatalf("tab background errors: %v", tabErrors.All())
			}
		}
	}
}
