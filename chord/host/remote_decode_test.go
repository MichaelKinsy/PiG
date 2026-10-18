package host

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// opsRewriteLink is a client's upstream link that turns the first state frame with ops into the frame the wire text ops
// decodes to with that frame's id, as a WebSocket or IPC link decodes received text (DecodeFrame).
type opsRewriteLink struct {
	Link
	t   *testing.T
	ops string

	mu   sync.Mutex
	done bool
}

func (link *opsRewriteLink) Receive(handler func(frame Frame)) {
	link.Link.Receive(func(frame Frame) {
		if state, ok := frame.(StateFrame); ok && state.Ops != nil {
			link.mu.Lock()
			first := !link.done
			link.done = true
			link.mu.Unlock()
			if first {
				decoded, err := DecodeFrame(fmt.Appendf(nil, `{"t":"state","hold":%d,"m":%q,"as":"ops","ops":%s}`, state.Hold, state.M, link.ops))
				if err != nil {
					link.t.Errorf("DecodeFrame: %v", err)
					return
				}
				frame = decoded
			}
		}
		handler(frame)
	})
}

// pi: packages/chord/src/remote.ts
// Pi 34a8bb38 delta/index.ts:660-670: Decoder.decode(wire: readonly unknown[]) runs assertValidWireOp on each element before
// use, and remote.ts:113-115 decodes a state frame's ops with it. Go's Decode([]WireOp) runs AssertValidWireOp on each element
// the same way (chord/delta/codec.go). The one element WireOp ([]any) cannot hold is a non-array, which never reaches the
// decoder: FrameSchema types ops as List(List(Unknown)) (wire.ts:109), so DecodeFrame refuses such a frame as Pi's isFrame
// does. A frame with a well-formed list of malformed ops passes the schema and is refused by the decoder of the remote state
// member, which reports the TypeError and keeps its value.
//
// mutation-checked: removing the AssertValidWireOp call from Decoder.Decode fails it.
func TestRemoteStateRejectsMalformedWireOpsInTheDecoder(t *testing.T) {
	for _, text := range []string{`{"t":"state","hold":1,"m":"n","as":"ops","ops":[5]}`, `{"t":"state","hold":1,"m":"n","as":"ops","ops":[null]}`, `{"t":"state","hold":1,"m":"n","as":"ops","ops":5}`} {
		if _, err := DecodeFrame([]byte(text)); err == nil {
			t.Errorf("DecodeFrame accepted %s; Pi's isFrame refuses it", text)
		}
	}
	for _, test := range []struct{ ops, message string }{
		{`[["s","x",1]]`, "path is not an array"},
		{`[["s"]]`, "s arity"},
		{`[[]]`, "op is not a tuple"},
		{`[["q",["n"],1]]`, "unknown op verb: q"},
	} {
		t.Run(test.ops, func(t *testing.T) {
			counter := Remote("t.decodeOps", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
				State:   Fields[Schema]{{"n", tNumber()}},
				Methods: Fields[MethodSpec]{{"set", Method(tNumber(), nil)}},
			}}}})
			server := requestsHost(t, HostOptions{Name: "server"})
			requestsServe(t, server.Serve, counter, func(_ any, _ Principal, self Self) (Implementation, error) {
				n, err := self.State("n", tNumber(), float64(0), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("variant", "main", "n", n, "set", methodFunc(func(value any, ctx Ctx) (any, error) {
					return nil, n.Replace(ctx, value, CellReplaceOptions{})
				})), nil
			}, ServeOptions{})
			down, up := MemoryLinkPair()
			if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var reported []error
			browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: &opsRewriteLink{Link: up, t: t, ops: test.ops}, ConnectOptions: ConnectOptions{Principal: kindServer()}},
				OnError: func(err error) {
					mu.Lock()
					reported = append(reported, err)
					mu.Unlock()
				}})
			ref := requestsGet(t, browser, counter, requestsEmpty(), nil)
			if err := readyErr(t, ref); err != nil {
				t.Fatal(err)
			}
			n := requestsReadable(t, ref, "n")
			stop := n.Subscribe(func(any) {})
			defer stop()
			if n.Value() != float64(0) {
				t.Fatalf("the snapshot is %v, want 0", n.Value())
			}
			mustCall(t, ref, "set", float64(1), nil)
			waitFor(t, "the decoder's report", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(reported) > 0
			})
			mu.Lock()
			defer mu.Unlock()
			var typeErr *delta.TypeError
			if !errors.As(reported[0], &typeErr) || typeErr.Message != test.message {
				t.Fatalf("reported %v, want TypeError %q", reported, test.message)
			}
			if got := n.Value(); got != float64(0) {
				t.Fatalf("value after refused ops = %v, want 0", got)
			}
		})
	}
}
