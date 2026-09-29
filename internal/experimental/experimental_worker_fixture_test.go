package experimental

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	hruntime "github.com/MichaelKinsy/PiG/agent/harness/runtime"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

const experimentalFauxWorkerEnv = "PIG_TEST_FAUX_SESSION_WORKER"

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:541-547,595-601,632-638. Only the worker entry changes; server/coordinator execution remains real.
func installFauxSessionWorker(t *testing.T) {
	t.Helper()
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.fauxWorker = true
}

// upstream: packages/coding-agent/test/fixtures/faux-session-worker.ts:13-59. The caller already consumes the internal worker role; RunSessionWorkerWithHarness owns the actual control channel, durable repository and worker service lifetime.
func runFauxSessionWorker(ctx context.Context, args []string) (result error) {
	var closeProvider func() error
	defer func() {
		if closeProvider != nil {
			result = errors.Join(result, closeProvider())
		}
	}()
	return RunSessionWorkerWithHarness(ctx, args, func(ctx context.Context, stored session.Session, options SessionWorkerOptions, _ *envpkg.NodeExecutionEnv) (SessionWorkerRuntime, error) {
		if options.Provider != "anthropic" || options.Model != "claude-sonnet-4-5" {
			return SessionWorkerRuntime{}, fmt.Errorf("Unexpected faux worker model: %s/%s", options.Provider, options.Model)
		}
		faux := ai.NewFauxProvider(ai.FauxConfig{})
		closeProvider = faux.Close
		faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{
			Content: []ai.FauxContentBlock{ai.FauxText("deterministic remote answer")}, Timestamp: new(int64(20)),
		})})
		models := ai.CreateModels()
		models.SetProvider(faux.Provider())
		created, err := hruntime.CreateAgentHarness(ctx, hruntime.AgentHarnessOptions{
			Session: stored, Models: models, Model: faux.GetModel(),
			Tools: []harness.AgentHarnessTool{}, Resources: agentharness.Resources{},
		})
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		return SessionWorkerRuntime{
			Harness:     &codingWorkerHarness{harness: created.Harness},
			FacetLoader: chord.CreateStaticFacetLoader([]chord.Facet{keyedProbeFacet()}),
		}, nil
	})
}

// upstream: packages/coding-agent/test/fixtures/keyed-service.ts:3-9.
type keyedProbeState struct {
	Value string `json:"value"`
}

type keyedProbe interface {
	State() pico3.ReplicatedStateOf[*keyedProbeState]
	Replace(context.Context, string) error
	Wait(context.Context) error
}

var keyedProbeDefinition = pico3.DefineService[keyedProbe]("test.keyed-probe")

type keyedProbeService struct {
	state   *chord.MutableReplicatedState[*keyedProbeState]
	replace func(string) error
}

func (probe *keyedProbeService) State() pico3.ReplicatedStateOf[*keyedProbeState] {
	return probe.state
}
func (probe *keyedProbeService) Replace(_ context.Context, value string) error {
	return probe.replace(value)
}
func (*keyedProbeService) Wait(ctx context.Context) error {
	if ctx.Done() == nil {
		return errors.New("Probe wait requires cancellation")
	}
	<-ctx.Done()
	return context.Cause(ctx)
}

func keyedProbeFacet() chord.Facet {
	return chord.DefineFacet(chord.Facet{Id: "@test/keyed-probe", Setup: func(env *chord.FacetEnvironment) error {
		probes, err := chord.ProvideMany(env, keyedProbeDefinition)
		if err != nil {
			return err
		}
		var spawn func(string) error
		spawn = func(value string) error {
			state, err := chord.NewReplicatedState(&keyedProbeState{Value: value})
			if err != nil {
				return err
			}
			closeProbe := func() {}
			closeProbe, err = probes.Spawn("probe", &keyedProbeService{state: state, replace: func(next string) error {
				closeProbe()
				return spawn(next)
			}})
			return err
		}
		return env.OnActivate(func(context.Context) error { return spawn("first") })
	}})
}

type keyedProbeView struct{ resolve func() (keyedProbe, error) }

func (view keyedProbeView) State() pico3.ReplicatedStateOf[*keyedProbeState] {
	return chord.StateView(func() (pico3.ReplicatedStateOf[*keyedProbeState], error) {
		probe, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return probe.State(), nil
	})
}
func (view keyedProbeView) Replace(ctx context.Context, value string) error {
	probe, err := view.resolve()
	if err != nil {
		return err
	}
	return probe.Replace(ctx, value)
}
func (view keyedProbeView) Wait(ctx context.Context) error {
	probe, err := view.resolve()
	if err != nil {
		return err
	}
	return probe.Wait(ctx)
}

type remoteKeyedProbe struct{ service *chord.RemoteService }

func (probe remoteKeyedProbe) State() pico3.ReplicatedStateOf[*keyedProbeState] {
	replica, err := probe.service.State("state")
	if err != nil {
		panic(err)
	}
	return chord.TypedReplica[*keyedProbeState](replica)
}
func (probe remoteKeyedProbe) Replace(ctx context.Context, value string) error {
	_, err := probe.service.Call(ctx, "replace", value)
	return err
}
func (probe remoteKeyedProbe) Wait(ctx context.Context) error {
	_, err := probe.service.Call(ctx, "wait")
	return err
}

func init() {
	chord.RegisterServiceView(keyedProbeDefinition, func(resolve func() (keyedProbe, error)) keyedProbe { return keyedProbeView{resolve: resolve} })
	chord.RegisterRemoteClient(keyedProbeDefinition, func(service *chord.RemoteService) keyedProbe { return remoteKeyedProbe{service: service} })
}
