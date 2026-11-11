package host

// pi: packages/chord/test/examples/17-limits-and-compression.ts

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// --- 17-limits-and-compression.ts ----------------------------------------------------------------------------------

// exCountingLink is a link end counting the bytes its host receives.
type exCountingLink struct {
	link.Link
	mu       sync.Mutex
	received int
}

func (c *exCountingLink) OnMessage(listener func([]byte)) {
	c.Link.OnMessage(func(message []byte) {
		c.mu.Lock()
		c.received += len(message)
		c.mu.Unlock()
		listener(message)
	})
}

func (c *exCountingLink) bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received
}

// exWords are the words of entries that compress like prose (about 3x), not like a repeated string.
var exWords = strings.Fields("the a model tool call file read write test error result agent session note code run build")

// exText is Pi's `text(i)`: about 1 KB of words picked by a fixed pseudo-random sequence (JavaScript's double arithmetic).
func exEntryText(i int) string {
	seed := float64(i*7919 + 1)
	var words []string
	for length := 0; length < 1000; length += len(words[len(words)-1]) + 1 {
		seed = math.Mod(seed*1103515245+12345, 2147483648)
		word := exWords[int(seed)%len(exWords)]
		if int(seed)%5 == 0 {
			word = fmt.Sprintf("%s%d", word, int(seed)%1000)
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

func TestExample17LimitsAndCompression(t *testing.T) {
	entry := exObj(exProp("id", exStr()), exProp("text", exStr()))
	store := exLocal("example.store", contracts.Params{exProp("id", exStr())},
		contracts.Mem("entries", exArr(entry)), contracts.Mem("append", contracts.NewMethod(entry)))
	conversation := exRemote("example.conversation", contracts.Params{exProp("id", exStr())}, exServerAddress,
		contracts.Var("v", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("entries", exArr(entry))},
			Methods: []contracts.MethodEntry{contracts.Meth("append", contracts.NewMethod(entry))},
		}))
	entries := exDefineState("entries", documents.StateOptions{
		Schema:  exObj(exProp("list", exArr(entry))),
		Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
	})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(store, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, entries)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"entries": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"append": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { exPush(draft, args) }, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(conversation, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			backing := self.Get(store, jsonObject("id", exParam(params, "id")))
			return exVariant("v", map[string]chord.Watchable[delta.JsonValue]{"entries": Expose(backing.Member("entries"))},
				map[string]contracts.MethodFunction{
					"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return backing.Call("append", args, ctx)
					},
				}), nil
		}, ServeOptions{})
	})
	errs := &hostErrors{}
	// setUp is a server compressing with compression and a tab without a compressor (a browser) on a link the server
	// accepts with limits; the tab's own link to the server has tabLimits.
	setUp := func(compression link.Compression, limits, tabLimits link.PartLimits) (Host, Host, *exCountingLink) {
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Compression: compression, OnError: errs.push})
		mustLoad(t)(server.Load([]HostFacet{{Name: "@example/conversation", Facet: facet}}, background))
		tab := newHost(t, HostOptions{OnError: errs.push})
		mustLoad(t)(tab.Load(nil, background))
		a, b := link.MemoryLinkPair()
		counted := &exCountingLink{Link: b}
		must(t, server.Accept(a, LinkOptions{Principal: jsonObject("kind", "user"), Limits: limits}, background))
		must(t, tab.Connect(counted, LinkOptions{Principal: serverPrincipal(), Limits: tabLimits}, background))
		return server, tab, counted
	}
	fill := func(server Host, id string) {
		backing := exGet(t, server, store, jsonObject("id", id))
		for i := range 500 {
			exCall(t, backing, "append", jsonObject("id", fmt.Sprintf("e%d", i), "text", exEntryText(i)))
		}
	}
	// 1. A 500 KB snapshot to a browser tab, without and with compression at the server.
	var snapshotBytes []int
	for _, compression := range []link.Compression{nil, nextnode.NodeCompression} {
		server, tab, counted := setUp(compression, link.PartLimits{}, link.PartLimits{})
		fill(server, "c1")
		reference := exGet(t, tab, conversation, jsonObject("id", "c1"))
		if got := len(exValue(t, reference, "entries").([]any)); got != 500 {
			t.Fatalf("snapshot: %d entries, want 500", got)
		}
		snapshotBytes = append(snapshotBytes, counted.bytes())
		exCloseHost(t, tab)
		exCloseHost(t, server)
	}
	if snapshotBytes[0] < 500*1000 || snapshotBytes[1] >= snapshotBytes[0]/2 {
		t.Fatalf("received bytes (uncompressed, deflated) = %v: want >= 500 KB and the deflated snapshot well below half", snapshotBytes)
	}
	// 2. Limits per part: the server admits at most 64 KiB per part from this tab. A larger argument fails its call only.
	{
		part := 64.0 * 1024
		server, tab, _ := setUp(nextnode.NodeCompression, link.PartLimits{Part: &part}, link.PartLimits{})
		reference := exGet(t, tab, conversation, jsonObject("id", "c2"))
		_, err := reference.Call("append", jsonObject("id", "big", "text", strings.Repeat("x", 100_000)), background)
		_ = exCode(t, err, data.CodeTooLarge, data.ReasonPart)
		exCall(t, reference, "append", jsonObject("id", "small", "text", "fits"))
		scopeEventually(t, "the link stays: entries", func() bool {
			list, _ := exValue(t, reference, "entries").([]any)
			return len(list) == 1 && list[0].(*delta.JsonObject).Value("id") == "small"
		})
		exCloseHost(t, tab)
		exCloseHost(t, server)
	}
	// 3. A compression bomb: 6 MB of zeros deflated, past the tab's decoded limit of 1 MiB. Only that hold fails.
	{
		decoded := 1024.0 * 1024
		server, tab, _ := setUp(nextnode.NodeCompression, link.PartLimits{}, link.PartLimits{Decoded: &decoded})
		bomb := exGet(t, server, store, jsonObject("id", "bomb"))
		for i := range 6 {
			exCall(t, bomb, "append", jsonObject("id", fmt.Sprintf("z%d", i), "text", strings.Repeat("0", 1_000_000)))
		}
		fill(server, "fine")
		_, err := tab.Get(background, conversation, jsonObject("id", "bomb"), GetOptions{})
		_ = exCode(t, err, data.CodeTooLarge, data.ReasonPart)
		fine := exGet(t, tab, conversation, jsonObject("id", "fine"))
		if got := len(exValue(t, fine, "entries").([]any)); got != 500 {
			t.Fatalf("another hold on the same link: %d entries", got)
		}
		exCloseHost(t, tab)
		exCloseHost(t, server)
	}
	// 4. A client with a compressor still sends its arguments up uncompressed: the server, which accepted its link as a
	// child, would refuse a compressed part. Its 100 KB argument arrives as JSON, within the server's default limits.
	{
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Compression: nextnode.NodeCompression, OnError: errs.push})
		mustLoad(t)(server.Load([]HostFacet{{Name: "@example/conversation", Facet: facet}}, background))
		client := newHost(t, HostOptions{Compression: nextnode.NodeCompression, OnError: errs.push})
		mustLoad(t)(client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		counted := &exCountingLink{Link: a}
		must(t, server.Accept(counted, LinkOptions{Principal: jsonObject("kind", "user")}, background))
		must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal()}, background))
		reference := exGet(t, client, conversation, jsonObject("id", "c4"))
		exCall(t, reference, "append", jsonObject("id", "big", "text", strings.Repeat(exEntryText(0), 100)))
		if got := counted.bytes(); got < 100*1000 {
			t.Fatalf("a compressing client's 100 KB argument: %d bytes received by the server", got)
		}
		exCloseHost(t, client)
		exCloseHost(t, server)
	}
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
