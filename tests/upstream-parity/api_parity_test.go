package parity

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// goAPIType is the reflected type of the pig extension.API interface.
func goAPIType() reflect.Type {
	return reflect.TypeFor[extension.API]()
}

// allowedDivergences names methods on extension.API that have NO direct
// upstream counterpart (camelCase → PascalCase or `on("...")` → `On*`).
// Each entry MUST have a corresponding row in DIVERGENCES.md.
//
// Note: upstream's `events: EventBus` property is picked up by the parser
// as a method (via propertyRE), so `Events` is already in upstream's
// Pascal-cased set: the property→method shape (D1) is a NAME-shape
// divergence visible in the API source, not an extra method here.
var allowedDivergences = map[string]string{}

// TestAPI_AllUpstreamEventsHaveOnMethod asserts that every event registrable
// via `pi.on(event, ...)` upstream has a corresponding `On<Event>` method on
// pig's extension.API interface.
func TestAPI_AllUpstreamEventsHaveOnMethod(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	api := goAPIType()
	for _, event := range s.API.Events {
		want := SnakeEventToOnMethod(event)
		if _, ok := api.MethodByName(want); !ok {
			gap(t, "api:"+want, "upstream event %q has no extension.API.%s",
				event, want)
		}
	}
}

// TestAPI_AllUpstreamMethodsHaveGoCounterpart asserts that every non-`on`
// method on upstream's ExtensionAPI has a matching method on extension.API
// (camelCase → PascalCase).
func TestAPI_AllUpstreamMethodsHaveGoCounterpart(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	api := goAPIType()
	for _, m := range s.API.Methods {
		want := CamelToPascal(m)
		if _, ok := api.MethodByName(want); !ok {
			t.Errorf("upstream ExtensionAPI.%s has no extension.API.%s",
				m, want)
		}
	}
}

// TestAPI_NoExtraGoMethodsBeyondUpstream asserts that pig's extension.API
// does not invent methods that don't exist upstream. Methods on the
// allowedDivergences map are intentional pig divergences; each MUST have
// a corresponding entry in DIVERGENCES.md.
func TestAPI_NoExtraGoMethodsBeyondUpstream(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}

	upstreamPascal := map[string]bool{}
	for _, m := range s.API.Methods {
		upstreamPascal[CamelToPascal(m)] = true
	}
	for _, e := range s.API.Events {
		upstreamPascal[SnakeEventToOnMethod(e)] = true
	}

	api := goAPIType()
	for method := range api.Methods() {
		name := method.Name
		if upstreamPascal[name] {
			continue
		}
		if _, ok := allowedDivergences[name]; ok {
			continue
		}
		t.Errorf("extension.API.%s has no upstream counterpart and is not "+
			"in allowedDivergences (a Go-only method here would need a "+
			"DIVERGENCES.md entry and an allowedDivergences row)",
			name)
	}
}

// TestAPI_GoSurfaceCounts is a coarse sanity check that the pig API exposes
// the expected total method count. Any change here forces a deliberate
// review of either the parity gates above or DIVERGENCES.md.
func TestAPI_GoSurfaceCounts(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	api := goAPIType()
	wantTotal := len(s.API.Methods) + len(s.API.Events) + len(allowedDivergences)
	got := api.NumMethod()
	if got != wantTotal {
		gap(t, "api-count", "extension.API method count: want %d (upstream %d methods + %d events + %d Go-only divergences), got %d",
			wantTotal, len(s.API.Methods), len(s.API.Events), len(allowedDivergences), got)
	}
}
