package host

// pi: packages/chord/test/next/examples/14-flow-control.ts

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 14-flow-control.ts --------------------------------------------------------------------------------------------

// exPausable is a slow tab's end of its link: while paused, what arrives waits (so the tab acknowledges nothing).
type exPausable struct {
	link     link.Link
	mu       sync.Mutex
	paused   bool
	waiting  [][]byte
	listener func([]byte)
}

func newExPausable(end link.Link) *exPausable {
	p := &exPausable{link: end}
	end.OnMessage(func(message []byte) {
		p.mu.Lock()
		// Messages that arrive before the host listens wait too (a microtask cannot interleave in Pi).
		if p.paused || p.listener == nil {
			p.waiting = append(p.waiting, message)
			p.mu.Unlock()
			return
		}
		listener := p.listener
		p.mu.Unlock()
		if listener != nil {
			listener(message)
		}
	})
	return p
}

func (p *exPausable) pause() { p.mu.Lock(); p.paused = true; p.mu.Unlock() }

func (p *exPausable) resume() {
	p.mu.Lock()
	p.paused = false
	waiting := p.waiting
	p.waiting = nil
	listener := p.listener
	p.mu.Unlock()
	for _, message := range waiting {
		listener(message)
	}
}

func (p *exPausable) Send(message []byte) { p.link.Send(message) }
func (p *exPausable) OnMessage(listener func([]byte)) {
	p.mu.Lock()
	p.listener = listener
	paused := p.paused
	p.mu.Unlock()
	if !paused {
		p.resume()
	}
}
func (p *exPausable) Close()                  { p.link.Close() }
func (p *exPausable) OnClose(listener func()) { p.link.OnClose(listener) }

func TestExample14FlowControl(t *testing.T) {
	reply := exLocal("example.reply", contracts.Params{},
		contracts.Mem("reply", exObj(exProp("text", exStr()))), contracts.Mem("append", contracts.NewMethod(exStr())))
	view := mustContract(contracts.Remote("example.view", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{exProp("session", exStr())}, HostAddress: exSessionAddress,
		Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("reply", exObj(exProp("text", exStr())))},
		})},
	}))
	replyDoc := exDefineState("reply", documents.StateOptions{
		Schema:  exObj(exProp("text", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("text", "") },
	})
	workerFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(reply, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, replyDoc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"reply": doc.Member()}, map[string]contracts.MethodFunction{
				"append": func(token delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("text", draft.Get("text").(string)+token.(string)) }, ctx)
				},
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(view, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return exVariant("owner", map[string]chord.Watchable[delta.JsonValue]{
				"reply": Expose(self.Get(reply, jsonObject()).Member("reply")),
			}, nil), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	worker := func(id string) Host {
		address := Session.Address(jsonObject("id", id))
		host := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: errs.push})
		mustLoad(t)(host.Load([]HostFacet{{Name: "@example/worker", Facet: workerFacet}}, background))
		return host
	}
	tab := func(end link.Link) Host {
		host := newHost(t, HostOptions{OnError: errs.push})
		must(t, host.Connect(end, LinkOptions{Principal: serverPrincipal()}, background))
		mustLoad(t)(host.Load(nil, background))
		return host
	}
	textLen := func(ref *Reference) int {
		return len(exValue(t, ref, "reply").(*delta.JsonObject).Value("text").(string))
	}
	// 1. Folding at the provider: a tab directly on a worker. Paused, it acknowledges nothing; the worker sends two
	// `state` messages (the window), then folds 1,000 appends into its pending queue.
	w1 := worker("S1")
	up, down := link.MemoryLinkPair()
	must(t, w1.Accept(up, LinkOptions{Principal: ownerPrincipal()}, background))
	slow := newExPausable(down)
	t1 := tab(slow)
	view1 := exGet(t, t1, view, jsonObject("session", "S1"))
	reply1 := exGet(t, w1, reply, jsonObject())
	slow.pause()
	for range 1000 {
		exCall(t, reply1, "append", " tok")
	}
	transported(up, down)
	if got, want := textLen(reply1), 4000; got != want {
		t.Fatalf("the worker has %d characters, want %d", got, want)
	}
	// Paused, what the worker sent waits at the tab's end of the link: the tab shows nothing yet.
	if got := textLen(view1); got != 0 {
		t.Fatalf("paused: the tab shows %d characters, want 0", got)
	}
	// 2. Resumed: the tab applies and acknowledges the two, and the freed slot carries one `state` with the fold.
	slow.resume()
	scopeEventually(t, "the tab catches up", func() bool { return textLen(view1) == 4000 })
	// 3. The same slow tab through a server: the worker's window for that origin fills and it folds there; a second, fast
	// tab is another origin and receives every commit.
	serverErrors := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: serverErrors.push})
	var w2 Host
	var w2Ends [2]*link.MemoryLink
	var w2Mu sync.Mutex
	mustRoute(t)(server.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			a, b := link.MemoryLinkPair()
			id, _ := address.Params.Value("id").(string)
			made := worker(id)
			must(t, made.Connect(b, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background))
			w2Mu.Lock()
			w2, w2Ends = made, [2]*link.MemoryLink{a, b}
			w2Mu.Unlock()
			return ResolvedLink{Link: a, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	mustLoad(t)(server.Load(nil, background))
	up2, down2 := link.MemoryLinkPair()
	must(t, server.Accept(up2, LinkOptions{Principal: ownerPrincipal()}, background))
	slow2 := newExPausable(down2)
	t2 := tab(slow2)
	view2 := exGet(t, t2, view, jsonObject("session", "S2"))
	up3, down3 := link.MemoryLinkPair()
	must(t, server.Accept(up3, LinkOptions{Principal: ownerPrincipal()}, background))
	t3 := tab(down3)
	view3 := exGet(t, t3, view, jsonObject("session", "S2"))
	watch, fast := exWatch(t, background, view3.Member("reply"))
	w2Mu.Lock()
	provider, workerLink := w2, w2Ends
	w2Mu.Unlock()
	reply2 := exGet(t, provider, reply, jsonObject())
	slow2.pause()
	for i := range 5 {
		exCall(t, reply2, "append", fmt.Sprintf(" t%d", i))
		// A relay forwards at once and returns each `ack` to the worker, so a fast tab frees its slot before the next commit
		// and receives every one. In Pi the hops are microtasks the next commit's own microtasks follow; here a commit is a call
		// from this goroutine, so wait for the `state` to reach the fast tab and its `ack` the worker before the next one, and for
		// the tab's watch to deliver it: a delivery runs on a goroutine of the tab's here (a microtask in Pi, which runs before
		// the next commit's own), and a `state` applied before that goroutine ran folds into the delivery waiting, so the watch
		// saw one change for two commits and the count below never reached five (seen under CPU load).
		transported(up2, down2, up3, down3, workerLink[0], workerLink[1])
		turn(t3)
	}
	transported(up2, down2, up3, down3)
	exEqual(t, "the worker has", exValue(t, reply2, "reply"), exJSON(t, `{"text":" t0 t1 t2 t3 t4"}`))
	exEqual(t, "paused: the slow tab shows", exValue(t, view2, "reply"), exJSON(t, `{"text":""}`))
	scopeEventually(t, "the fast tab saw every change", func() bool { return len(fast.all()) == 5 })
	exEqual(t, "the fast tab", exValue(t, view3, "reply"), exJSON(t, `{"text":" t0 t1 t2 t3 t4"}`))
	// 4. Resumed: the slow tab acknowledges the two; the relay returns each `ack` to the worker with the origin, and the
	// worker's freed slot carries one `state` with the three folded appends.
	slow2.resume()
	scopeEventually(t, "the slow tab catches up", func() bool {
		return exJSONEqual(exValue(t, view2, "reply"), exJSON(t, `{"text":" t0 t1 t2 t3 t4"}`))
	})
	exCloseHost(t, t1)
	exCloseHost(t, w1)
	_, _ = watch.Stop()
	exCloseHost(t, t2)
	exCloseHost(t, t3)
	exCloseHost(t, server)
	exCloseHost(t, provider)
	if errs.Len() > 0 || serverErrors.Len() > 0 {
		t.Fatalf("background errors: %v %v", errs.All(), serverErrors.All())
	}
}

// exReferences is `host.references(Type).value`: one JSON object {params, principal?, incarnation} per reference.
func exReferences(t testing.TB, host Host, typ *contracts.Contract) []*delta.JsonObject {
	t.Helper()
	var value delta.JsonValue
	var err error
	host.(*chordHost).enter(func() { value, err = host.References(typ).Value() })
	must(t, err)
	list, _ := value.([]any)
	out := make([]*delta.JsonObject, len(list))
	for i, entry := range list {
		out[i] = entry.(*delta.JsonObject)
	}
	return out
}
