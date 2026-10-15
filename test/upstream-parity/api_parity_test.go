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
// Each entry MUST have a corresponding row in docs/parity/DIVERGENCES.md.
//
// Note: upstream's `events: EventBus` property is picked up by the parser
// as a method (via propertyRE), so `Events` is already in upstream's
// Pascal-cased set: the property→method shape (D1) is a NAME-shape
// divergence visible in the API source, not an extra method here.
var allowedDivergences = map[string]string{}

// goOverloads names the Go methods that split one upstream method with several call signatures: Go has no overloads.
// The value is the upstream method (PascalCase) the Go method is a signature of; it is a language mechanic, not a
// divergence, so no DIVERGENCES.md row is needed.
var goOverloads = map[string]string{
	// extensions/types.ts:1830-1831 registerProvider(provider: Provider) and registerProvider(name, config)
	"RegisterNativeProvider": "RegisterProvider",
}

// TestAPI_NoExtraGoMethodsBeyondUpstream asserts that pig's extension.API
// does not invent methods that don't exist upstream. Methods on the
// allowedDivergences map are intentional pig divergences; each MUST have
// a corresponding entry in docs/parity/DIVERGENCES.md.
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
	for goName, upstream := range goOverloads {
		if _, ok := api.MethodByName(goName); !ok {
			t.Errorf("goOverloads names extension.API.%s, which does not exist; delete the stale entry", goName)
		}
		if !upstreamPascal[upstream] {
			t.Errorf("goOverloads maps extension.API.%s to upstream %s, which the pinned ExtensionAPI does not declare", goName, upstream)
		}
	}
	for method := range api.Methods() {
		name := method.Name
		if upstreamPascal[name] {
			continue
		}
		if _, ok := allowedDivergences[name]; ok {
			continue
		}
		if _, ok := goOverloads[name]; ok {
			continue // checked above
		}
		t.Errorf("extension.API.%s has no upstream counterpart and is not "+
			"in allowedDivergences (a Go-only method here would need a "+
			"docs/parity/DIVERGENCES.md entry and an allowedDivergences row)",
			name)
	}
}
