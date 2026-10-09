package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/src/experimental/services/worker.ts:91-109 reloadPlugins extends reloadTail synchronously, and
// the provider runs the member's synchronous prefix during invoke (packages/chord/src/services/provider.ts:234), so reloads one
// connection pipelines run in the order sent: the Nth call performs the Nth load. Calls are told apart by which loads fail,
// since a reload carries no arguments.
func TestWorkerPluginReloadsRunInCallOrder(t *testing.T) {
	const calls = 300
	failing := func(load int) bool { return load%5 == 0 }
	var mu sync.Mutex
	loads := 0
	loader := sessionPluginTestLoader(func(context.Context) (chord.LoadedFacets, error) {
		mu.Lock()
		loads++
		load := loads
		mu.Unlock()
		if failing(load) {
			return chord.LoadedFacets{}, errors.New("load failed")
		}
		return chord.LoadedFacets{Dispose: func(context.Context) error { return nil }}, nil
	})
	durable := durabletest.OpenFauxConversation()
	t.Cleanup(func() { _ = durable.Harness.Close(context.Background()) })
	worker, err := services.CreateSessionWorkerServices(services.SessionWorkerServicesOptions{
		Harness: durable.Harness, Conversation: durable.Conversation, FacetLoader: loader,
		Publish: func(context.Context, services.WorkerServiceScope, string, chord.ServiceProviderUpdate) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Dispose() })
	scope := services.WorkerServiceScope{ServerConnectionId: "server-1", AttachmentId: "attachment-1"}
	invocations := make([]*chord.ServiceInvocation, calls)
	for i := range invocations {
		invocations[i], err = worker.BeginInvoke(t.Context(), chord.ServiceCall{ServiceId: services.SessionPluginsDefinition.Id(), Member: "reload", Args: []json.RawMessage{}}, scope)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, invocation := range invocations {
		_, err := invocation.Wait(t.Context())
		// Load 1 is the initial activation; call i performs load i+2.
		if want := failing(i + 2); (err != nil) != want {
			t.Fatalf("reload %d failed=%v, want %v: it did not perform load %d", i, err != nil, want, i+2)
		}
	}
}
