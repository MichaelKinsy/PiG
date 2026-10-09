package chord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// facetReloadDifferentialServices are package-global service views, so the test defines them once per process.
var facetReloadDifferentialServices = sync.OnceValue(func() map[string]ServiceDefinition[*FacetServiceImplementation] {
	services := map[string]ServiceDefinition[*FacetServiceImplementation]{}
	for _, name := range []string{"ra", "rb", "rc", "rd"} {
		services[name] = DefineService[*FacetServiceImplementation]("frd." + name)
	}
	return services
})

type facetReloadSpec struct {
	SetupThrows      bool   `json:"setupThrows"`
	Shape            string `json:"shape"`
	ActivateThrows   bool   `json:"activateThrows"`
	OwnThrows        bool   `json:"ownThrows"`
	DeactivateThrows bool   `json:"deactivateThrows"`
}

type facetReloadObservation struct {
	Result string   `json:"result"`
	Events []string `json:"events"`
	Probe  []string `json:"probe"`
}

// Pi packages/chord/src/facets/host.ts:388-421 (activate), 423-512 (reload: ID checks, staged setup and shape check, candidate
// activation in activation order, cutover, retirement in reverse order, keyed reconnection, and the three cleanup aggregates) and
// 514-540 (dispose) against the installed chord 1.1.0 dist, by testdata/facet_reload_differential.mjs. Facet a provides ra, b uses ra
// and provides rb, c provides rc. Seeded random reloads replace a subset of the facets (sometimes with a duplicate, unknown or empty
// ID); each new generation may throw from setup after registering its callbacks, drop or add a provision, or throw from activation,
// an owned cleanup or a deactivation. After the initial activation, each reload and the final dispose, the result (an aggregate with
// its causes), the setup, activation, deactivation and cleanup events it ran and the generation that answers ra, rb and rc through
// host.services.invoke must equal Pi's.
func TestFacetHostReloadsMatchPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed = 200, 8, 20260706
	cmd := exec.CommandContext(t.Context(), "node", "testdata/facet_reload_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), "frd")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var cases []struct {
		Initial map[string]facetReloadSpec `json:"initial"`
		Reloads [][]struct {
			Id   string          `json:"id"`
			Spec facetReloadSpec `json:"spec"`
		} `json:"reloads"`
		Observed []facetReloadObservation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	services := facetReloadDifferentialServices()
	provides := map[string]string{"a": "ra", "b": "rb", "c": "rc"}
	ctx := t.Context()
	failures := 0
	for index, c := range cases {
		var mu sync.Mutex
		events := []string{}
		record := func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}
		take := func() []string {
			mu.Lock()
			defer mu.Unlock()
			taken := events
			events = []string{}
			return taken
		}
		facet := func(id string, generation int, spec facetReloadSpec) Facet {
			name := fmt.Sprintf("%s#%d", id, generation)
			implementation := func() *FacetServiceImplementation {
				return &FacetServiceImplementation{Members: []FacetServiceMember{{Name: "m", Invoke: func(context.Context, []json.RawMessage) (json.RawMessage, error) {
					return json.Marshal(name)
				}}}}
			}
			return Facet{Id: id, Setup: func(env *FacetEnvironment) error {
				record("setup " + name)
				if id == "b" {
					if _, err := UseService(env, services["ra"]); err != nil {
						return err
					}
				}
				if spec.Shape != "none" {
					if err := ProvideService(env, services[provides[id]], implementation()); err != nil {
						return err
					}
				}
				if spec.Shape == "extra" {
					if err := ProvideService(env, services["rd"], implementation()); err != nil {
						return err
					}
				}
				hook := func(kind string, throws bool) func(context.Context) error {
					return func(context.Context) error {
						record(kind + " " + name)
						if throws {
							return fmt.Errorf("%s %s failed", kind, name)
						}
						return nil
					}
				}
				if err := errors.Join(env.Own(hook("own", spec.OwnThrows)), env.OnActivate(hook("activate", spec.ActivateThrows)), env.OnDeactivate(hook("deactivate", spec.DeactivateThrows))); err != nil {
					return err
				}
				if spec.SetupThrows {
					return fmt.Errorf("setup %s failed", name)
				}
				return nil
			}}
		}
		settle := func(err error) string {
			if err != nil {
				return "!" + describeProviderError(err)
			}
			return "ok"
		}
		var host *FacetHost
		probe := func() []string {
			answers := []string{}
			for _, name := range []string{"ra", "rb", "rc"} {
				raw, err := host.Services().Invoke(ctx, ServiceCall{ServiceId: services[name].Id(), Member: "m", Args: []json.RawMessage{}})
				if err != nil {
					answers = append(answers, fmt.Sprintf("%s=!%s", name, describeProviderError(err)))
					continue
				}
				var answer string
				if err := json.Unmarshal(raw, &answer); err != nil {
					t.Fatal(err)
				}
				answers = append(answers, fmt.Sprintf("%s=%s", name, answer))
			}
			return answers
		}
		var got []facetReloadObservation
		initial := []Facet{facet("a", 0, c.Initial["a"]), facet("b", 0, c.Initial["b"]), facet("c", 0, c.Initial["c"])}
		// b's handle on ra is an in-host remote client; Go needs the adapter Pi's typed proxy makes unnecessary, and b never calls it.
		host, err = CreateFacetHost(ctx, FacetOptions{Facets: initial, RemoteClients: map[string]func(*RemoteService) any{
			services["ra"].Id(): func(*RemoteService) any { return &FacetServiceImplementation{} },
		}})
		first := facetReloadObservation{Result: settle(err), Events: take(), Probe: []string{}}
		if err == nil {
			first.Probe = probe()
		}
		got = append(got, first)
		if err == nil {
			for step, reload := range c.Reloads {
				facets := make([]Facet, 0, len(reload))
				for _, entry := range reload {
					facets = append(facets, facet(entry.Id, step+1, entry.Spec))
				}
				result := settle(host.Reload(ctx, facets))
				got = append(got, facetReloadObservation{Result: result, Events: take(), Probe: probe()})
			}
			got = append(got, facetReloadObservation{Result: settle(host.Dispose(ctx)), Events: take(), Probe: []string{}})
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(c.Observed)
		if string(gotJSON) != string(wantJSON) {
			failures++
			if failures <= 3 {
				var lines []string
				for step := range max(len(got), len(c.Observed)) {
					var g, w []byte
					if step < len(got) {
						g, _ = json.Marshal(got[step])
					}
					if step < len(c.Observed) {
						w, _ = json.Marshal(c.Observed[step])
					}
					if !bytes.Equal(g, w) {
						lines = append(lines, fmt.Sprintf("step %d\n      got  %s\n      want %s", step, g, w))
					}
				}
				t.Errorf("scenario %d differs from Pi (reloads %v):\n    %s", index, len(c.Reloads), strings.Join(lines, "\n    "))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, scenarios)
	}
}
