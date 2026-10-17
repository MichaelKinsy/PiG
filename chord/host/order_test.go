package host

// Order tests for the places where Pi's iteration and start order is observable: listener invocations
// (object.ts:1090-1110), variant and member order (object.ts, remote.ts) and Link.onClose's Set (link.ts).

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// rootSeqOf is the sequence number of a root id (host.ts:440 `${name}#${incarnation}:${seq}`).
func rootSeqOf(t *testing.T, rootID string) int {
	t.Helper()
	seq, err := strconv.Atoi(rootID[strings.LastIndexByte(rootID, ':')+1:])
	if err != nil {
		t.Fatalf("root id %q: %v", rootID, err)
	}
	return seq
}

// object.ts:1090-1110: a listener event starts its invocation synchronously inside the publication, so the roots of
// the invocations (host.root, host.ts:438-442) are made, and the listener functions entered, in publication order.
func TestListenerInvocationsStartInPublicationOrder(t *testing.T) {
	const events = 64
	host := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	var mu sync.Mutex
	var seqs, values []int
	var wait sync.WaitGroup
	wait.Add(events + 1) // the initial value, then every change
	factory := func(_ any, _ Principal, self Self) (Implementation, error) {
		count, err := self.State("count", linkCountSchema, delta.JsonObjectOf("n", float64(0)), nil)
		if err != nil {
			return nil, err
		}
		if err := self.On("log", count, func(event any, ctx Ctx) error {
			mu.Lock()
			seqs = append(seqs, rootSeqOf(t, ctx.RootID()))
			values = append(values, int(countN(event)))
			mu.Unlock()
			wait.Done()
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main",
			"count", count,
			"add", methodFunc(func(args any, ctx Ctx) (any, error) {
				for range int(args.(float64)) {
					if err := count.Change(ctx, func(draft any) {
						object := draft.(*delta.Object)
						_ = object.Set("n", object.Get("n").(float64)+1)
					}); err != nil {
						return nil, err
					}
				}
				return countN(count.Value()), nil
			}),
			"who", methodFunc(func(any, Ctx) (any, error) { return nil, nil }),
		), nil
	}
	if err := host.Serve(linkCounter, factory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	counter := mustGet(t, host, "c")
	mustCall(t, counter, "add", float64(events), nil)
	wait.Wait()
	mu.Lock()
	defer mu.Unlock()
	// The value each invocation saw, by the order its root was made in.
	byRoot := make([]int, len(values))
	for index, value := range values {
		rank := 0
		for _, other := range seqs {
			if other < seqs[index] {
				rank++
			}
		}
		byRoot[rank] = value
	}
	for index, value := range byRoot {
		if value != index {
			t.Fatalf("listener roots by creation order saw values %v, want 0..%d in order", byRoot, events)
		}
	}
}

// link.ts:17,38-41,70-71: close handlers are a Set, so a handler registered twice is called once.
func TestLinkOnCloseCallsAReRegisteredHandlerOnce(t *testing.T) {
	left, _ := MemoryLinkPair()
	var reasons []string
	handler := func(reason string) { reasons = append(reasons, reason) }
	other := func(reason string) { reasons = append(reasons, "other "+reason) }
	left.OnClose(handler)
	left.OnClose(other)
	left.OnClose(handler)
	left.Close("done")
	if got := strings.Join(reasons, ","); got != "done,other done" {
		t.Fatalf("close handlers saw %q, want %q", got, "done,other done")
	}
}

// contract.ts:126-141, object.ts:538-546, 640-646 and 1323-1350, connection.ts:324: a contract's variants and members
// and an implementation's members keep their declaration order (own-key order). It shows in the description a link carries
// (JSON.stringify), in which variant describes a member first, in which validation failure is reported, in coverage
// reasons and in a shape's members. Sorted order differs from declaration order in every case below.
func TestContractsAndImplementationsKeepDeclarationOrder(t *testing.T) {
	contract := Remote("t.ord", RemoteDef{Version: 1, Input: tNumber(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{
		{"zeta", VariantDef{State: Fields[Schema]{{"y", tNumber()}}, Methods: Fields[MethodSpec]{{"b", Method(tNull(), tNull())}}}},
		{"alpha", VariantDef{State: Fields[Schema]{{"b", tString()}}}},
	}})
	data, err := json.Marshal(contract.Description)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"t.ord","scope":"remote","version":1,"input":{"type":"number"},"host":{"name":"server"},"variants":{` +
		`"zeta":{"y":{"kind":"state","schema":{"type":"number"}},"b":{"kind":"method","args":{"type":"null"},"result":{"type":"null"}}},` +
		`"alpha":{"b":{"kind":"state","schema":{"type":"string"}}}}}`
	if string(data) != want {
		t.Fatalf("description JSON\n got %s\nwant %s", data, want)
	}
	var decoded Description
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if again, _ := json.Marshal(decoded); string(again) != want {
		t.Fatalf("decoded description JSON %s", again)
	}

	if described, _ := (&RefImpl{contract: contract}).describe(nil, "b"); described.Kind != MemberMethod {
		t.Fatalf("b is described as %s, want zeta's method", described.Kind)
	}

	service, err := makeService(contract, nil, ServeOptions{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImplementation(service, chordjson.ObjectOf("variant", "zeta", "q", 1, "c", 2)); messageOf(err) != "invalid/implementation: t.ord: q is not in variant zeta" {
		t.Fatalf("extra members: %v", err)
	}
	if err := validateImplementation(service, chordjson.ObjectOf("variant", "zeta")); messageOf(err) != "invalid/implementation: t.ord: y must be a cell, a derivation or a held object's state member" {
		t.Fatalf("missing members: %v", err)
	}

	consumer := Remote("t.ord", RemoteDef{Version: 1, Input: tNumber(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{
		{"zeta", VariantDef{}}, {"alpha", VariantDef{}},
	}})
	provider := Remote("t.ord", RemoteDef{Version: 1, Input: tNumber(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"other", VariantDef{}}}})
	if reason := Coverage(provider.Description, consumer.Description); reason != "variant zeta missing" {
		t.Fatalf("coverage reason %q", reason)
	}

	host := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	serveCounter(t, host, nil)
	counter := mustGet(t, host, "c")
	if err := counter.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if shape := counter.Shape(); shape == nil || strings.Join(shape.Members, ",") != "count,add,who" {
		t.Fatalf("shape = %+v, want members count,add,who", shape)
	}
}
