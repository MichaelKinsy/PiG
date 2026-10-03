package experimental

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

func TestPortWave07ExperimentalPluginReload(t *testing.T) {
	t.Parallel()
	// upstream: packages/coding-agent/test/experimental-plugin-reload.test.ts:9. The faux conversation is the stand-in of internal/experimental/durabletest for experimental-durable-support.ts openFauxConversation.
	t.Run("loads and cuts over a fresh Session facet generation", func(t *testing.T) {
		t.Parallel()
		var activations, disposals []int
		generation := 0
		loader := sessionPluginTestLoader(func(context.Context) (chord.LoadedFacets, error) {
			generation++
			current := generation
			return chord.LoadedFacets{
				Facets: []chord.Facet{{Id: "reloadable-session-plugin", Setup: func(env *chord.FacetEnvironment) error {
					return env.OnActivate(func(context.Context) error { activations = append(activations, current); return nil })
				}}},
				Dispose: func(context.Context) error { disposals = append(disposals, current); return nil },
			}, nil
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
		disposed := false
		t.Cleanup(func() {
			if !disposed {
				if err := worker.Dispose(); err != nil {
					t.Error(err)
				}
			}
		})
		if !reflect.DeepEqual(activations, []int{1}) {
			t.Fatalf("activations = %v, want [1]", activations)
		}
		_, err = worker.Invoke(context.Background(), chord.ServiceCall{ServiceId: services.SessionPluginsDefinition.Id(), Member: "reload", Args: []json.RawMessage{}}, services.WorkerServiceScope{ServerConnectionId: "server-1", AttachmentId: "attachment-1"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(activations, []int{1, 2}) {
			t.Fatalf("activations = %v, want [1 2]", activations)
		}
		if !reflect.DeepEqual(disposals, []int{1}) {
			t.Fatalf("disposals = %v, want [1]", disposals)
		}
		err = worker.Dispose()
		disposed = true
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(disposals, []int{1, 2}) {
			t.Fatalf("disposals = %v, want [1 2]", disposals)
		}
	})
}

type sessionPluginTestLoader func(context.Context) (chord.LoadedFacets, error)

func (loader sessionPluginTestLoader) Load(ctx context.Context) (chord.LoadedFacets, error) {
	return loader(ctx)
}
