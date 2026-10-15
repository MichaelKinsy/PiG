package ai

import (
	"testing"
)

var providerHeadersSink ProviderHeaders

// Every configured header becomes its own non-null value, and the values of one map cost one allocation together.
func TestProviderHeadersFromStringsGivesEachHeaderItsOwnValue(t *testing.T) {
	if got := ProviderHeadersFromStrings(nil); got != nil {
		t.Fatalf("nil headers = %v, want nil", got)
	}
	if got := ProviderHeadersFromStrings(map[string]string{}); got != nil {
		t.Fatalf("empty headers = %v, want nil", got)
	}
	values := map[string]string{"User-Agent": "pig", "Editor-Version": "1", "X-Empty": "", "X-Same": "pig"}
	headers := ProviderHeadersFromStrings(values)
	if len(headers) != len(values) {
		t.Fatalf("headers = %v, want %d entries", headers, len(values))
	}
	seen := map[*string]string{}
	for name, value := range values {
		got := headers[name]
		if got == nil || *got != value {
			t.Fatalf("%s = %v, want %q", name, got, value)
		}
		if other, shared := seen[got]; shared {
			t.Fatalf("%s and %s share one value", name, other)
		}
		seen[got] = name
	}
	*headers["User-Agent"] = "changed"
	for name, value := range values {
		if name != "User-Agent" && *headers[name] != value {
			t.Fatalf("writing User-Agent changed %s to %q", name, *headers[name])
		}
	}
	if values["User-Agent"] != "pig" {
		t.Fatal("writing a header value changed the configured map")
	}
	shared := new(string)
	mapAllocs := testing.AllocsPerRun(100, func() {
		filled := make(ProviderHeaders, len(values))
		for name := range values {
			filled[name] = shared
		}
		providerHeadersSink = filled
	})
	if allocs := testing.AllocsPerRun(100, func() { providerHeadersSink = ProviderHeadersFromStrings(values) }); allocs > mapAllocs+1 {
		t.Fatalf("ProviderHeadersFromStrings(%d headers) = %v allocations, want the map's %v and one for the values", len(values), allocs, mapAllocs)
	}
}
