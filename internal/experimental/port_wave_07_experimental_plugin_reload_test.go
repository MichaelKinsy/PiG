package experimental

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

func TestPortWave07ExperimentalPluginReload(t *testing.T) {
	t.Parallel()
	// upstream: packages/coding-agent/test/experimental-plugin-reload.test.ts:8.
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
		snapshot := agentharness.LaneSnapshot{
			Lane: "main", Transcript: []session.Entry{}, TipID: nil,
			Configuration: session.LaneConfiguration{Model: session.ModelRef{Provider: "test", ModelID: "model"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{}},
			Stats:         session.SessionStats{MessageCount: 0, Usage: ai.Usage{}},
			Operation:     nil, Queues: []agentharness.LaneQueuedItem{}, Faulted: false,
		}
		worker, err := services.CreateSessionWorkerServices(services.SessionWorkerServicesOptions{
			Lane: &sessionPluginTestLane{snapshot: snapshot}, FacetLoader: loader,
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

// The upstream mock supplies only watch/getModel/getThinkingLevel. Embedded nil contracts make unexpected command or setter calls fail rather than returning fabricated results.
type sessionPluginTestLane struct {
	services.AgentLane
	services.ModelsServiceLane
	snapshot agentharness.LaneSnapshot
}

func (lane *sessionPluginTestLane) Watch(context.Context) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
	return &sessionPluginTestWatch{snapshot: lane.snapshot}, nil
}
func (*sessionPluginTestLane) GetModel(context.Context) (*ai.Model, error) { return nil, nil }
func (*sessionPluginTestLane) GetThinkingLevel(context.Context) (ai.ThinkingLevel, error) {
	return ai.ThinkingOff, nil
}

type sessionPluginTestWatch struct{ snapshot agentharness.LaneSnapshot }

func (watch *sessionPluginTestWatch) Snapshot() agentharness.LaneSnapshot { return watch.snapshot }
func (*sessionPluginTestWatch) Start(agentharness.EventListener) error    { return nil }
func (watch *sessionPluginTestWatch) Resnapshot(context.Context) (agentharness.LaneSnapshot, error) {
	return watch.snapshot, nil
}
func (*sessionPluginTestWatch) Unsubscribe() {}
