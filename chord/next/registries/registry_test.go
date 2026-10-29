package registries_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/test/next/registries/registry.test.ts.
//
// Designed out: `expectTypeOf(Http).toEqualTypeOf<Registry<...>>()` and `expectTypeOf(value).toEqualTypeOf<
// RegistryValue<number>>()` are type-level only; Go checks them at compile time with the typed assignments below.
//
// The "providers" cases need the host (`createHost`, `defineFacet`, `loadedPlugins`, `host.serve`, `host.registry`,
// `host.close`) and are not in this package.

func TestTokens(t *testing.T) {
	t.Run("carry a name, a version and the types only; names and versions are checked at declaration", func(t *testing.T) {
		type contribution struct{ path string }
		Http, err := registries.DefineRegistry[contribution, []string]("chord.http", registries.DefineRegistryOptions{Version: 2})
		if err != nil {
			t.Fatalf("DefineRegistry: %v", err)
		}
		if want := (registries.Registry[contribution, []string]{Kind: "registry", Name: "chord.http", Version: 2}); Http != want {
			t.Fatalf("token = %#v, want %#v", Http, want)
		}
		var _ registries.Registry[contribution, []string] = Http

		_, err = registries.DefineRegistry[any, any]("Chord.Http", registries.DefineRegistryOptions{Version: 1})
		if err == nil || !strings.Contains(err.Error(), `registry "Chord.Http": expected lowercase`) {
			t.Fatalf("bad name error = %v", err)
		}
		_, err = registries.DefineRegistry[any, any]("chord.http", registries.DefineRegistryOptions{Version: 0})
		if err == nil || !strings.Contains(err.Error(), "chord.http.version: expected a positive integer") {
			t.Fatalf("bad version error = %v", err)
		}
	})
}
