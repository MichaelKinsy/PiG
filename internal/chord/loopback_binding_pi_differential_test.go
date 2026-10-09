package chord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// loopbackBindingImplementation is one provision: m answers its tag and st is its own state.
type loopbackBindingImplementation struct {
	tag   string
	state *MutableReplicatedState[map[string]any]
}

func (implementation *loopbackBindingImplementation) M(context.Context) (string, error) {
	return implementation.tag, nil
}

func (implementation *loopbackBindingImplementation) St() *MutableReplicatedState[map[string]any] {
	return implementation.state
}

// loopbackBindingServices are package-global service views, so the test defines them once per process.
var loopbackBindingServices = sync.OnceValue(func() map[string]ServiceDefinition[*loopbackBindingImplementation] {
	services := map[string]ServiceDefinition[*loopbackBindingImplementation]{}
	for _, name := range []string{"s", "k", "x"} {
		services[name] = DefineService[*loopbackBindingImplementation]("lbd." + name)
	}
	return services
})

// Pi packages/chord/src/services/consumer.ts:444-660 (RemoteServiceBindingImpl use, observe, ready, rebind, dispose and the singleton
// start), 262-440 (KeyedBinding), 37-140 (member views), loopback.ts:5-17 and provider.ts against the installed chord 1.1.0 dist, by
// testdata/loopback_binding_differential.mjs. A binding allowlisting s (singleton) and k (keyed) is connected through the loopback
// transport to a live provider. Seeded random sequences mix consumer operations (use and observe, including the wrong mode and the
// unallowlisted x; unobserve; rebind; a late dispose) with provider operations (provide, withdraw, replace, spawn, instance close and
// state changes). After each step and binding readiness, the step result, the readiness result, the reported errors, the opened
// observations and every held handle's method answer and state value must equal Pi's.
func TestRemoteServiceBindingOverLoopbackMatchesPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed = 200, 25, 20260708
	cmd := exec.CommandContext(t.Context(), "node", "testdata/loopback_binding_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), "lbd")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	type observation struct {
		Result string   `json:"result"`
		Ready  string   `json:"ready"`
		Events []string `json:"events"`
		Probe  []string `json:"probe"`
	}
	var cases []struct {
		Ops []struct {
			Op      string `json:"op"`
			Service string `json:"service"`
			Index   int    `json:"index"`
			Bound   bool   `json:"bound"`
			Key     string `json:"key"`
			N       int    `json:"n"`
		} `json:"ops"`
		Observed []observation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	services := loopbackBindingServices()
	settle := func(err error) string {
		if err != nil {
			return "!" + describeProviderError(err)
		}
		return "ok"
	}
	ctx := t.Context()
	failures := 0
	for index, c := range cases {
		provider, err := NewRemoteServiceProvider(SingletonService(services["s"]), KeyedService(services["k"]))
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		events := []string{}
		record := func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}
		binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
			Services:  ServiceIDs(services["s"].Id(), services["k"].Id()),
			Transport: NewLoopbackTransport(provider),
			OnError:   func(err error) { record("error " + describeProviderError(err)) },
		})
		if err != nil {
			t.Fatal(err)
		}
		var states []*MutableReplicatedState[map[string]any]
		newImplementation := func(service string) *loopbackBindingImplementation {
			state, err := NewReplicatedState(map[string]any{"n": 0})
			if err != nil {
				t.Fatal(err)
			}
			implementation := &loopbackBindingImplementation{tag: fmt.Sprintf("%s%d", service, len(states)), state: state}
			states = append(states, state)
			return implementation
		}
		type held struct {
			label   string
			service *RemoteService
		}
		var closers []func() error
		var handles, views []held
		var unobservers []func()
		var trace []string
		for step, op := range c.Ops {
			mu.Lock()
			events = []string{}
			mu.Unlock()
			result := "ok"
			switch op.Op {
			case "use":
				handle, err := binding.Use(services[op.Service].Id())
				result = settle(err)
				if err == nil && !slicesContainsFacade(handles, func(h held) *serviceFacade { return h.service.facade }, handle.facade) {
					handles = append(handles, held{op.Service, handle})
				}
			case "observe":
				observer := len(unobservers)
				unobserve, err := binding.Observe(services[op.Service].Id(), func(_ context.Context, view *RemoteService) error {
					mu.Lock()
					events = append(events, fmt.Sprintf("open %d %d", observer, len(views)))
					views = append(views, held{fmt.Sprintf("v%d", len(views)), view})
					mu.Unlock()
					return nil
				})
				result = settle(err)
				if err == nil {
					unobservers = append(unobservers, unobserve)
				}
			case "unobserve":
				unobservers[op.Index]()
			case "rebind":
				result = settle(binding.Rebind(ctx, op.Bound))
			case "dispose":
				result = settle(binding.Dispose(ctx))
			case "provide":
				result = settle(Provide(provider, services["s"], newImplementation("s")))
			case "withdraw":
				result = settle(Withdraw(provider, services["s"]))
			case "replace":
				result = settle(Replace(provider, services["s"], newImplementation("s")))
			case "spawn":
				closer, err := Spawn(provider, services["k"], op.Key, newImplementation("k"))
				result = settle(err)
				if err == nil {
					closers = append(closers, closer)
				}
			case "close":
				result = settle(closers[op.Index]())
			case "change":
				result = settle(states[op.Index].Change(ctx, func(draft map[string]any) error {
					draft["n"] = op.N
					return nil
				}))
			}
			// The oracle's awaits after each operation drain the loopback starts it launched before readiness is asked for.
			quiesceBinding(binding)
			ready := settle(binding.Ready(ctx))
			quiesceBinding(binding)
			mu.Lock()
			targets := append(append([]held{}, handles...), views...)
			mu.Unlock()
			probe := []string{}
			for _, target := range targets {
				raw, err := target.service.Call(ctx, "m")
				if err != nil {
					probe = append(probe, fmt.Sprintf("%s.m!%s", target.label, describeProviderError(err)))
				} else {
					var answer string
					if err := json.Unmarshal(raw, &answer); err != nil {
						t.Fatal(err)
					}
					probe = append(probe, fmt.Sprintf("%s.m=%s", target.label, answer))
				}
				probe = append(probe, target.label+".st"+loadReplicaText(target.service))
			}
			mu.Lock()
			got := observation{Result: result, Ready: ready, Events: events, Probe: probe}
			gotJSON, _ := json.Marshal(got)
			mu.Unlock()
			want := c.Observed[step]
			wantJSON, _ := json.Marshal(want)
			trace = append(trace, fmt.Sprintf("step %d %+v\n      got  %s\n      want %s", step, op, gotJSON, wantJSON))
			if !bytes.Equal(gotJSON, wantJSON) {
				failures++
				if failures <= 3 {
					t.Errorf("scenario %d differs from Pi:\n    %s", index, strings.Join(trace[max(0, len(trace)-4):], "\n    "))
				}
				break
			}
		}
		_ = binding.Dispose(ctx)
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, scenarios)
	}
}

// quiesceBinding waits until every subscription start the binding tracks has settled, as the oracle's setImmediate does after readiness:
// readiness rejects at its first failure the way Promise.all does, while the loopback starts left behind finish in Pi's microtasks.
func quiesceBinding(binding *RemoteServiceBinding) {
	for {
		snapshot, err := binding.readinessSnapshot()
		if err != nil {
			return
		}
		for _, start := range snapshot.starts {
			<-start.done
		}
		if same, err := binding.readinessCurrent(snapshot); err != nil || same {
			return
		}
	}
}

// loadReplicaText renders a state member read the way the oracle does: =<JSON>, =undefined while unhydrated, or !<error>.
func loadReplicaText(service *RemoteService) string {
	replica, err := service.State("st")
	if err != nil {
		return "!" + describeProviderError(err)
	}
	value, hydrated, err := replica.Load()
	if err != nil {
		return "!" + describeProviderError(err)
	}
	if !hydrated {
		return "=undefined"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "!" + err.Error()
	}
	return "=" + string(encoded)
}

func slicesContainsFacade[E any](items []E, facade func(E) *serviceFacade, want *serviceFacade) bool {
	for _, item := range items {
		if facade(item) == want {
			return true
		}
	}
	return false
}
