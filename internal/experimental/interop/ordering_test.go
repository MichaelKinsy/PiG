//go:build !windows

package interop

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Ports packages/server/src/server.ts handleRequest and session-router.ts executeServiceCall ordering: requests on one
// connection reach the hosted Session in the order the client sent them, because each request runs synchronously up to
// the same number of await points. A Go goroutine per request must not reorder them.
func TestServerRequestOrdering(t *testing.T) {
	t.Parallel()
	testServerRequestOrdering(t, false)
}

// A request rejected before it reaches the host (here, addressed to another server) must not release the requests behind it
// before the requests ahead of it: upstream throws WrongServerError synchronously inside handleRequest, which changes nothing
// about when its neighbours reach the host (packages/server/src/server.ts:347).
func TestServerRequestOrderingAcrossRejectedRequests(t *testing.T) {
	t.Parallel()
	testServerRequestOrdering(t, true)
}

func testServerRequestOrdering(t *testing.T, interleaveRejected bool) {
	const requests = 300
	for _, kind := range []string{"node server", "go server"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			start := startGoServer
			if kind == "node server" {
				start = startNodeServer
			}
			fixture := start(t, nil)
			factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: fixture.path})
			if err != nil {
				t.Fatal(err)
			}
			c, err := client.Connect(t.Context(), client.ClientOptions{TransportFactory: factory, ServerId: logicalServerID})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Dispose() }()
			server := protocol.ServerTarget{ServerId: logicalServerID}
			if _, err := c.Request(t.Context(), server, chord.ServiceCall{ServiceId: "pi.session-management", Member: "attach", Args: args(`"session-1"`)}); err != nil {
				t.Fatal(err)
			}
			for c.Attachment() == nil {
			}
			session := *c.Attachment()
			operations := make([]*chord.ServiceInvocation, requests)
			var rejected []*chord.ServiceInvocation
			for i := range operations {
				operations[i], err = c.BeginInvoke(t.Context(), session, chord.ServiceCall{ServiceId: "s", Member: "echo", Args: args(strconv.Itoa(i))})
				if err != nil {
					t.Fatal(err)
				}
				if interleaveRejected {
					wrong, err := c.BeginInvoke(t.Context(), protocol.ServerTarget{ServerId: otherServerID}, chord.ServiceCall{ServiceId: "s", Member: "echo", Args: args(strconv.Itoa(i))})
					if err != nil {
						t.Fatal(err)
					}
					rejected = append(rejected, wrong)
				}
			}
			for _, operation := range operations {
				if _, err := operation.Wait(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			for _, operation := range rejected {
				if _, err := operation.Wait(context.Background()); err == nil {
					t.Fatal("a request addressed to another server succeeded")
				}
			}
			fixture.stop()
			var seen []int
			for _, event := range fixture.log.ordered() {
				var decoded struct {
					Event string
					Call  struct {
						Member string
						Args   []int
					}
				}
				if err := json.Unmarshal([]byte(event), &decoded); err != nil || decoded.Event != "call" || decoded.Call.Member != "echo" {
					continue
				}
				seen = append(seen, decoded.Call.Args[0])
			}
			if len(seen) != requests {
				t.Fatalf("host observed %d calls, want %d", len(seen), requests)
			}
			for i, value := range seen {
				if value != i {
					t.Fatalf("host observed call %d at position %d; first reordering in %v", value, i, seen[:min(len(seen), i+8)])
				}
			}
		})
	}
}
