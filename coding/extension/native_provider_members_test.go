package extension_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// models.ts:150-215 Provider: the carrier an extension registers is the Provider object with each callback taking the operation's context.
// Every exported member of the Go Provider object (ai.ModelsProvider) is a member of the carrier of the same name, so registerNativeProvider(provider)
// loses none of them.
func TestNativeProviderCarriesEveryProviderMember(t *testing.T) {
	carrier := reflect.TypeFor[extension.NativeProvider]()
	object := reflect.TypeFor[ai.ModelsProvider]()
	var missing []string
	for field := range object.Fields() {
		if !field.IsExported() {
			continue
		}
		if _, ok := carrier.FieldByName(field.Name); !ok {
			missing = append(missing, field.Name)
		}
	}
	if len(missing) != 0 {
		slices.Sort(missing)
		t.Fatalf("NativeProvider lacks Provider members %v", missing)
	}
}

// models.ts:155 Provider.headers survives the carrier round trip a compiled-in extension's provider takes into the registry.
func TestNativeProviderOfRoundTripsProviderHeaders(t *testing.T) {
	provider := &ai.ModelsProvider{ID: "p", Name: "P", Headers: ai.ProviderHeaders{"X-A": new("a"), "X-Null": nil}}
	back := extension.NativeProviderOf(provider).ProviderObject()
	if len(back.Headers) != 2 || back.Headers["X-A"] == nil || *back.Headers["X-A"] != "a" {
		t.Fatalf("round-tripped headers = %v", back.Headers)
	}
	if value, ok := back.Headers["X-Null"]; !ok || value != nil {
		t.Fatalf("a null header was not preserved: %v", back.Headers)
	}
}
