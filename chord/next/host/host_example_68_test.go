package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Twin of packages/chord/test/examples/68-shared-holds.ts (Pi 47e9d74f9186): remote references sharing one wire hold
// (decision "References share one wire hold"). A page shows one remote counter in three components; each takes its own
// reference, as a component does. The page's host holds the counter once: one `hold` message, one snapshot, one encode
// per commit at the server; the second and third references join the held object at once, with no message. Each
// reference keeps its own release: releasing two sends nothing, the last sends the one `release`, and a reference made
// after it holds anew.
//
// The script prints four lines and asserts nothing; here each printed value is a check, with the value the script
// prints on Pi's host:
//
//	holds sent: 1 | server's references: 1
//	counts: [ 1, 1, 1 ] | encodes for the commit: 1
//	releases after two: 0
//	releases after three: 1 | holds sent: 2

// ex68Counted is Pi's `counted(link, sent)`: link with the kinds of the messages it sends counted (decoded: the binary
// codec is the wire's).
type ex68Counted struct {
	link.Link
	mu   sync.Mutex
	sent map[string]int
}

func (c *ex68Counted) Send(message []byte) {
	if decoded, err := link.BinaryCodec.Decode(message); err == nil {
		c.mu.Lock()
		c.sent[decoded.Kind()]++
		c.mu.Unlock()
	}
	c.Link.Send(message)
}

// get is `sent.get(kind) ?? 0`.
func (c *ex68Counted) get(kind string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent[kind]
}

func TestExample68SharedHolds(t *testing.T) {
	// A counter on the server: its count, and a method that adds one.
	counter := exRemote("example.shared-counter", contracts.Params{}, exServerAddress, contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{exProp("count", exInt())},
		Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod())},
	}))
	count := exDefineState("count", documents.StateOptions{
		Schema: exObj(exProp("n", exInt())), Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})

	// 1. The server serves the counter; its adapter counts the encodes of its link's commits.
	recorder := telemetry.NewInMemoryTelemetryContext()
	errs := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Telemetry: recorder, OnError: errs.push})
	mustLoad(t)(server.Load([]HostFacet{{Name: "@example/counter", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(counter, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, count)
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{
				"count": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("n"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
				},
			}), nil
		}, ServeOptions{})
	})}}, background))

	// 2. The page: its link to the server counts what the page sends.
	page := newHost(t, HostOptions{OnError: errs.push})
	mustLoad(t)(page.Load(nil, background))
	near, far := link.MemoryLinkPair()
	sent := &ex68Counted{Link: near, sent: map[string]int{}}
	must(t, server.Accept(far, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
	must(t, page.Connect(sent, LinkOptions{Principal: serverPrincipal(), Codec: link.BinaryCodec}, background))

	// 3. Three components, three references: the first holds and waits for the snapshot; the others join at once.
	components := make([]*Reference, 0, 3)
	for range 3 {
		components = append(components, exGet(t, page, counter, jsonObject()))
	}
	if holds, references := sent.get("hold"), activityOf(server).References; holds != 1 || references != 1 {
		t.Fatalf("holds sent: %d | server's references: %d, want 1 | 1", holds, references)
	}

	// 4. A commit at the server is encoded once (not once per reference) and reaches every component in one batch.
	encodes := func() float64 {
		total := 0.0
		for _, measurement := range recorder.GetMetrics() {
			if measurement.Name == "chord.link.encodes" {
				total += measurement.Value
			}
		}
		return total
	}
	// Pi waits 10 ms after the call for the commit to reach the page. Here each component's count is watched and the
	// check waits for the three values (the barrier "the commit reached every component", bounded by exReceive's 10 s).
	changed := make(chan struct{}, 1)
	var watches []chord.WatchHandle[delta.JsonValue]
	for _, component := range components {
		watch, err := component.Member("count").Watch(background)
		must(t, err)
		watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
			select {
			case changed <- struct{}{}:
			default:
			}
			return nil
		})
		watches = append(watches, watch)
	}
	before := encodes()
	exCall(t, components[0], "bump", nil)
	counts := func() []any {
		values := make([]any, 0, len(components))
		for _, component := range components {
			values = append(values, exValue(t, component, "count"))
		}
		return values
	}
	for !exJSONEqual(counts(), []any{1.0, 1.0, 1.0}) {
		exReceive(t, "the commit reaches the three components: counts "+exText(counts()), changed)
	}
	flushed(server)
	if got := encodes() - before; got != 1 {
		t.Fatalf("counts: %s | encodes for the commit: %v, want 1", exText(counts()), got)
	}
	for _, watch := range watches {
		_, err := watch.Stop()
		must(t, err)
	}

	// 5. Releasing two sends nothing; the last sends the one release; a reference made after it holds anew.
	components[0].Release()
	components[1].Release()
	flushed(page)
	if got := sent.get("release"); got != 0 {
		t.Fatalf("releases after two: %d, want 0", got)
	}
	components[2].Release()
	exGet(t, page, counter, jsonObject())
	// The new hold was answered, so everything the page sent before it is counted: a release the first two references
	// had sent late would be here too.
	if releases, holds := sent.get("release"), sent.get("hold"); releases != 1 || holds != 2 {
		t.Fatalf("releases after three: %d | holds sent: %d, want 1 | 2", releases, holds)
	}
	must(t, page.Close(background))
	must(t, server.Close(background))
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
