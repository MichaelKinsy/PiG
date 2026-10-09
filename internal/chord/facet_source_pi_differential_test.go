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

// facetSourceDifferentialServices are package-global service views, so the test defines them once per process.
var facetSourceDifferentialServices = sync.OnceValue(func() map[string]ServiceDefinition[*FacetServiceImplementation] {
	services := map[string]ServiceDefinition[*FacetServiceImplementation]{}
	for _, name := range []string{"s", "k"} {
		services[name] = DefineService[*FacetServiceImplementation]("fsd." + name)
	}
	return services
})

// facetSourceDifferentialSource offers a live provider's catalogue and opens a loopback binding to it, keeping the binding the host
// opened so the test can rebind it.
type facetSourceDifferentialSource struct {
	provider *RemoteServiceProvider
	binding  *RemoteServiceBinding
}

func (source *facetSourceDifferentialSource) AcceptsUnavailableServices() bool { return false }

func (source *facetSourceDifferentialSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	return source.provider.Catalogue(), nil
}

func (source *facetSourceDifferentialSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services:     options.Services,
		Transport:    NewLoopbackTransport(source.provider),
		OnError:      options.OnError,
		AssertAccess: options.AssertAccess,
	})
	if err != nil {
		return nil, err
	}
	source.binding = binding
	return binding, nil
}

// Pi packages/chord/src/facets/host.ts:596-655 (#resolveExternalServices opens one binding per source), 687-713 (#bindServices binds
// an external singleton through use and a keyed service through the binding), 423-512 (reload), 727-735 (#disposeServiceBindings)
// and services/consumer.ts:444-660 against the installed chord 1.1.0 dist, by testdata/facet_source_differential.mjs. A facet host
// consumes a singleton s and a keyed k offered by an external source, a loopback binding to a live provider; its facet u uses s and
// observes k. Seeded random sequences mix provider operations (provide, withdraw, replace, spawn, instance close), reloads of u whose
// new generation may throw from setup or activation, and rebinds of the source's binding. After each step the step's result, the
// lifecycle and observation events, the host's reported errors and what each of the last two generations' s handles and every
// observed k view answers must equal Pi's, and so must the final dispose. Generation 0's activation is not recorded: Go starts the
// first external keyed subscription on its own goroutine, concurrently with the first activation callback, while Pi's loopback start
// always resumes after the callback's synchronous prefix (host.ts:117-122), so that one pair has no fixed order in Go.
func TestFacetHostExternalSourceMatchesPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed = 200, 20, 20260709
	cmd := exec.CommandContext(t.Context(), "node", "testdata/facet_source_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), "fsd")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	type observation struct {
		Result string   `json:"result"`
		Events []string `json:"events"`
		Probe  []string `json:"probe"`
	}
	type operation struct {
		Op             string `json:"op"`
		Key            string `json:"key"`
		Index          int    `json:"index"`
		Generation     int    `json:"generation"`
		SetupThrows    bool   `json:"setupThrows"`
		ActivateThrows bool   `json:"activateThrows"`
		Bound          bool   `json:"bound"`
	}
	var cases []struct {
		Initial struct {
			Provide bool `json:"provide"`
			Spawn   bool `json:"spawn"`
		} `json:"initial"`
		Ops      []operation   `json:"ops"`
		Observed []observation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	services := facetSourceDifferentialServices()
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
		provisions := 0
		newImplementation := func(service string) *FacetServiceImplementation {
			tag := fmt.Sprintf("%s%d", service, provisions)
			provisions++
			return &FacetServiceImplementation{Members: []FacetServiceMember{{Name: "m", Invoke: func(context.Context, []json.RawMessage) (json.RawMessage, error) {
				return json.Marshal(tag)
			}}}}
		}
		type held struct {
			label   string
			service *FacetService
		}
		var handles, views []held
		facet := func(generation int, setupThrows, activateThrows bool) Facet {
			return Facet{Id: "u", Setup: func(env *FacetEnvironment) error {
				record(fmt.Sprintf("setup u#%d", generation))
				handle, err := UseFacetService(env, services["s"].Id(), false)
				if err != nil {
					return err
				}
				mu.Lock()
				handles = append(handles, held{fmt.Sprintf("u%d.s", generation), handle})
				mu.Unlock()
				if err := ObserveFacetService(env, services["k"].Id(), false, func(_ context.Context, view *FacetService) error {
					mu.Lock()
					events = append(events, fmt.Sprintf("open u#%d v%d", generation, len(views)))
					views = append(views, held{fmt.Sprintf("v%d", len(views)), view})
					mu.Unlock()
					return nil
				}); err != nil {
					return err
				}
				if err := env.OnActivate(func(context.Context) error {
					if generation > 0 {
						record(fmt.Sprintf("activate u#%d", generation))
					}
					if activateThrows {
						return fmt.Errorf("activate u#%d failed", generation)
					}
					return nil
				}); err != nil {
					return err
				}
				if setupThrows {
					return fmt.Errorf("setup u#%d failed", generation)
				}
				return nil
			}}
		}
		var closers []func() error
		if c.Initial.Provide {
			if err := Provide(provider, services["s"], newImplementation("s")); err != nil {
				t.Fatal(err)
			}
		}
		if c.Initial.Spawn {
			closer, err := Spawn(provider, services["k"], "a", newImplementation("k"))
			if err != nil {
				t.Fatal(err)
			}
			closers = append(closers, closer)
		}
		source := &facetSourceDifferentialSource{provider: provider}
		quiesce := func() {
			if source.binding != nil {
				quiesceBinding(source.binding)
			}
		}
		probe := func() []string {
			mu.Lock()
			targets := append(append([]held{}, handles[max(0, len(handles)-2):]...), views...)
			mu.Unlock()
			answers := []string{}
			for _, target := range targets {
				remote, err := target.service.Remote()
				var raw json.RawMessage
				if err == nil {
					raw, err = remote.Call(ctx, "m")
				}
				if err != nil {
					answers = append(answers, fmt.Sprintf("%s!%s", target.label, describeProviderError(err)))
					continue
				}
				var answer string
				if err := json.Unmarshal(raw, &answer); err != nil {
					t.Fatal(err)
				}
				answers = append(answers, fmt.Sprintf("%s=%s", target.label, answer))
			}
			return answers
		}
		take := func(result string) observation {
			quiesce()
			got := observation{Result: result, Probe: probe()}
			mu.Lock()
			got.Events = events
			events = []string{}
			mu.Unlock()
			return got
		}
		var trace []string
		compare := func(step int, label string, got observation) bool {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(c.Observed[step])
			trace = append(trace, fmt.Sprintf("step %d %s\n      got  %s\n      want %s", step, label, gotJSON, wantJSON))
			if bytes.Equal(gotJSON, wantJSON) {
				return true
			}
			failures++
			if failures <= 3 {
				t.Errorf("scenario %d differs from Pi:\n    %s", index, strings.Join(trace[max(0, len(trace)-4):], "\n    "))
			}
			return false
		}
		host, err := CreateFacetHost(ctx, FacetOptions{
			Facets:         []Facet{facet(0, false, false)},
			ServiceSources: []RemoteServiceSource{source},
			OnError:        func(err error) { record("error " + describeProviderError(err)) },
		})
		if !compare(0, "create", take(settle(err))) || err != nil {
			if err == nil {
				_ = host.Dispose(ctx)
			}
			continue
		}
		matched := true
		for step, op := range c.Ops {
			result := "ok"
			switch op.Op {
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
			case "reload":
				result = settle(host.Reload(ctx, []Facet{facet(op.Generation, op.SetupThrows, op.ActivateThrows)}))
			case "rebind":
				result = settle(source.binding.Rebind(ctx, op.Bound))
			}
			if !compare(step+1, fmt.Sprintf("%+v", op), take(result)) {
				matched = false
				break
			}
		}
		if matched && len(c.Observed) == len(c.Ops)+2 {
			got := take(settle(host.Dispose(ctx)))
			if source.binding != nil {
				_, err := source.binding.Use(services["s"].Id())
				got.Probe = append(got.Probe, "binding "+settle(err))
			}
			compare(len(c.Ops)+1, "dispose", got)
		} else {
			_ = host.Dispose(ctx)
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, scenarios)
	}
}
