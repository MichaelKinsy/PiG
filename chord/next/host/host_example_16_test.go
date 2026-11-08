package host

// pi: packages/chord/test/examples/16-presence-and-activity.ts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// --- 16-presence-and-activity.ts -----------------------------------------------------------------------------------

func TestExample16PresenceAndActivity(t *testing.T) {
	doc := exRemote("example.doc", contracts.Params{exProp("id", exStr())}, exServerAddress, contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{exProp("viewers", exInt())},
		Methods: []contracts.MethodEntry{contracts.Meth("think", contracts.NewMethod(contracts.Schema{}, exInt()))},
	}))
	userOf := func(name string) data.Principal { return jsonObject("kind", "user", "name", name) }
	thinking := make(chan struct{})
	release := make(chan struct{})
	linger := 50 * time.Millisecond
	// 1. The server serves `Doc`; its factory derives the viewer count from `host.references(Doc)`.
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(doc, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			id := exParam(params, "id")
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{
				"viewers": self.Derive([]any{h.References(doc)}, func(values []any) (delta.JsonValue, error) {
					count := 0
					for _, entry := range values[0].([]any) {
						if entry.(*delta.JsonObject).Value("params").(*delta.JsonObject).Value("id") == id {
							count++
						}
					}
					return float64(count), nil
				}),
			}, map[string]contracts.MethodFunction{
				"think": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					close(thinking)
					// Pi: `await release.promise`, which an abort does not end.
					chordctx.Suspend(ctx)
					<-release
					if principalKind(exCtx(ctx).Principal()) == "user" {
						return float64(42), nil
					}
					return float64(0), nil
				},
			}), nil
		}, ServeOptions{Linger: &linger})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/doc", Facet: facet})
	// 2. The supervisor's view: every publication of the counts.
	activityWatch, err := server.Activity().Watch(background)
	must(t, err)
	var published exCollector
	activityWatch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			published.add(exText(call.Value))
		}
		return nil
	})
	// 3. Ada opens the document in two tabs, Bob in one: three references, three incarnations, two objects (one per
	// principal); the viewer count is over every object of the document.
	ada1 := exAccept(t, server, userOf("ada"))
	ada2 := exAccept(t, server, userOf("ada"))
	bob := exAccept(t, server, userOf("bob"))
	adaDoc := exGet(t, ada1.Host, doc, jsonObject("id", "d1"))
	exGet(t, ada2.Host, doc, jsonObject("id", "d1"))
	exGet(t, bob.Host, doc, jsonObject("id", "d1"))
	scopeEventually(t, "viewers seen by ada", func() bool { return exValue(t, adaDoc, "viewers") == float64(3) })
	// The supervisor's watch is given the counts as they are when it runs, a slow one the newest only (`[["r", counts]]`):
	// the three holds are published for it once it ran, which the test waits for as it waits for the count.
	scopeEventually(t, "the supervisor saw the three holds", func() bool { return published.sawFrame(`"references":3`) })
	var present []string
	for _, ref := range exReferences(t, server, doc) {
		incarnation := ref.Value("incarnation").(string)
		present = append(present, ref.Value("principal").(*delta.JsonObject).Value("name").(string)+" @ "+incarnation[len(incarnation)-4:])
	}
	if len(present) != 3 || present[0][:3] != "ada" || present[1][:3] != "ada" || present[2][:3] != "bob" {
		t.Fatalf("references = %v, want ada, ada, bob", present)
	}
	if present[0] == present[1] {
		t.Fatalf("two tabs of ada have one incarnation: %v", present)
	}
	// 4. A call running counts as activity until it settles.
	thought := make(chan any, 1)
	go func() { value, _ := adaDoc.Call("think", nil, background); thought <- value }()
	<-thinking
	if got := exActivity(t, server).Calls; got != 1 {
		t.Fatalf("activity while thinking: %d calls", got)
	}
	scopeEventually(t, "the supervisor saw the running call", func() bool { return published.sawFrame(`"calls":1`) })
	close(release)
	if got := <-thought; got != float64(42) {
		t.Fatalf("think = %v", got)
	}
	// 5. Bob's link drops: a departure, at once. Bob's own `Doc` object lingers.
	bob.Link.Close()
	scopeEventually(t, "viewers after bob's link dropped", func() bool { return exValue(t, adaDoc, "viewers") == float64(2) })
	// 6. Ada closes both tabs: the object lingers (50 ms) with no references, then ends; then everything is zero.
	exCloseHost(t, ada1.Host)
	exCloseHost(t, ada2.Host)
	scopeEventually(t, "references at the end", func() bool { return len(exReferences(t, server, doc)) == 0 })
	scopeEventually(t, "the lingering objects ended", func() bool { return exActivity(t, server) == ActivityCounts{} })
	scopeEventually(t, "the last publication", func() bool {
		all := published.all()
		return len(all) > 0 && all[len(all)-1] == `{"references":0,"calls":0,"jobs":0,"lingering":0}`
	})
	frames := published.all()
	if len(frames) == 0 || frames[len(frames)-1] != `{"references":0,"calls":0,"jobs":0,"lingering":0}` {
		t.Fatalf("the supervisor's last publication = %v", frames)
	}
	sawCall, sawThree := false, false
	for _, frame := range frames {
		sawCall = sawCall || strings.Contains(frame, `"calls":1`)
		sawThree = sawThree || strings.Contains(frame, `"references":3`)
	}
	if !sawCall || !sawThree {
		t.Fatalf("publications %v lack a running call or the three holds", frames)
	}
	_, _ = activityWatch.Stop()
	exCloseHost(t, server)
}
