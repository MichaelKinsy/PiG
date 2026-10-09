//go:build !windows

package interop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type serviceHostAdapter struct {
	host *services.RoutedServerServiceHost
}

func (adapter serviceHostAdapter) AttachClient(ctx context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return adapter.host.AttachClient(ctx, presentation)
}

// Server-wide services reach their application in the order one connection sent the calls: pipelined mutations from a client run in
// send order through the real wire, router and service provider (packages/server/src/server.ts handleRequest, chord provider.ts:234).
func TestServerServicesMutationOrderOverTheWire(t *testing.T) {
	t.Parallel()
	const calls = 300
	var mu sync.Mutex
	var order []string
	host, err := services.CreateExperimentalServerServices(services.ExperimentalServerServicesOptions{
		List: func(context.Context) ([]services.SessionSummary, error) { return nil, nil },
		Create: func(_ context.Context, options services.SessionCreateOptions) (services.SessionSummary, error) {
			mu.Lock()
			order = append(order, *options.Id)
			mu.Unlock()
			return services.SessionSummary{SessionAddress: services.SessionAddress{ServerId: logicalServerID, SessionId: *options.Id}}, nil
		},
		Remove: func(context.Context, string) error { return nil },
		PrepareSessionPlugins: func(context.Context, string, []string) (services.PreparedSessionPlugins, error) {
			return services.PreparedSessionPlugins{}, nil
		},
		ReloadPresentationPlugins: func(context.Context, []string) (chord.JsonValue, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Dispose() })
	path := filepath.Join(socketDirectory(t), "s.sock")
	server, err := routing.CreateUnixServer(routing.ServerHost{
		ServerServices: serviceHostAdapter{host.Host},
		ResolveSession: func(context.Context, string) (routing.SessionMetadata, error) {
			return nil, routing.NewSessionNotFoundError()
		},
		OpenSession: func(context.Context, routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
			return nil, routing.NewSessionNotFoundError()
		},
	}, routing.UnixServerOptions{Path: path, ServerId: logicalServerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.Connect(t.Context(), client.ClientOptions{TransportFactory: factory, ServerId: logicalServerID})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Dispose() }()
	operations := make([]*chord.ServiceInvocation, calls)
	for i := range operations {
		id, _ := json.Marshal(map[string]any{"id": strconv.Itoa(i)})
		operations[i], err = c.BeginInvoke(t.Context(), protocol.ServerTarget{ServerId: logicalServerID}, chord.ServiceCall{ServiceId: "pi.session-management", Member: "create", Args: []json.RawMessage{id}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, operation := range operations {
		if _, err := operation.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range order {
		if id != strconv.Itoa(i) {
			t.Fatalf("position %d ran create %s; order %v", i, id, order[:min(len(order), i+6)])
		}
	}
	if len(order) != calls {
		t.Fatalf("%d creates ran, want %d", len(order), calls)
	}
}
