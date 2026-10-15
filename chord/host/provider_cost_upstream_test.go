package host

// Ports packages/chord/test/provider-cost.test.ts (Pi b45e0ebc): what a change costs the provider. The diff is computed
// once per change; each feed (one per connection and member) encodes it with its own stateful encoder and its link
// serializes it. Pi counts encoders and encodes by mocking its delta module (vitest vi.mock), which has no Go form: the
// counts are asserted on the wire instead, where one encode per feed is one frame of ops per link and no new stream is a
// frame that continues the member's stream (an op frame, never a snapshot or reset).

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var costList = Remote("t.e8-cost", RemoteDef{
	Version: 1, Input: tObject(), Host: NewHostNamed("server"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"items", tArray(tString())}},
		Methods: Fields[MethodSpec]{{"add", Method(tString(), nil)}},
	}}},
})

// provider-cost.test.ts:44-82 "one change to K clients: K encodes (one per feed), one frame per link, the same ops on
// every link"
func TestProviderCostOneChangeToKClientsOneFramePerLinkTheSameOpsOnEveryLink(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, costList, func(_ any, _ Principal, self Self) (Implementation, error) {
		items, err := self.State("items", tArray(tString()), []any{}, nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "items", items, "add", methodFunc(func(text any, ctx Ctx) (any, error) {
			return nil, items.Change(ctx, func(draft any) {
				if _, err := draft.(*delta.Array).Push(text); err != nil {
					panic(err)
				}
			})
		})), nil
	}, ServeOptions{})
	const clients = 4
	type client struct {
		link *countingLink
		ref  Ref
	}
	var all []client
	for range clients {
		down, up := MemoryLinkPair()
		// One principal: every client reaches the same object.
		if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
			t.Fatal(err)
		}
		link := newCountingLink(up)
		host := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
		all = append(all, client{link, bindingsHold(t, host, costList, map[string]any{}, nil)})
	}
	for _, c := range all {
		ready(t, c.ref)
	}
	before := make([]int, clients)
	for index, c := range all {
		before[index] = len(stateFrames(c.link.receivedFrames()))
	}
	mustCall(t, all[0].ref, "add", "x", nil)
	time.Sleep(10 * time.Millisecond)
	var ops []string
	for index, c := range all {
		frames := stateFrames(c.link.receivedFrames())[before[index]:]
		if len(frames) != 1 || frames[0].As != "ops" {
			t.Fatalf("client %d got %v, want one frame of ops (a stream continued, no new snapshot)", index, frames)
		}
		// The frame differs per link only by its hold id: compare the ops.
		wire := make([]any, len(frames[0].Ops))
		for position, op := range frames[0].Ops {
			wire[position] = []any(op)
		}
		ops = append(ops, jsonText(t, wire))
		if got := jsonText(t, requestsReadable(t, c.ref, "items").Value()); got != `["x"]` {
			t.Fatalf("client %d items = %s", index, got)
		}
	}
	if len(slices.Compact(slices.Clone(ops))) != 1 {
		t.Fatalf("every link carries the same ops: %v", ops)
	}
}
