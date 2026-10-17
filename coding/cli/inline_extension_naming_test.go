package cli

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports test/suite/regressions/6260-inline-extension-naming.test.ts: bare factories load as <inline:N> by 1-based position, named
// ones as <inline:name>, and a named factory keeps its hidden flag.
func TestInlineExtensionNamingMatchesPi(t *testing.T) {
	noop := func(extension.API) error { return nil }
	for _, tc := range []struct {
		name   string
		inline []extension.InlineExtension
		paths  []string
		hidden []bool
	}{
		{"bare", []extension.InlineExtension{extension.ExtensionFactory(noop), extension.ExtensionFactory(noop)}, []string{"<inline:1>", "<inline:2>"}, []bool{false, false}},
		{"named", []extension.InlineExtension{extension.NamedInlineExtension{Name: "my-provider", Factory: noop}, extension.NamedInlineExtension{Name: "my-commands", Factory: noop}}, []string{"<inline:my-provider>", "<inline:my-commands>"}, []bool{false, false}},
		{"hidden", []extension.InlineExtension{extension.NamedInlineExtension{Name: "built-in", Factory: noop, Hidden: true}}, []string{"<inline:built-in>"}, []bool{true}},
		{"mixed", []extension.InlineExtension{extension.ExtensionFactory(noop), extension.NamedInlineExtension{Name: "named-ext", Factory: noop}, extension.ExtensionFactory(noop)}, []string{"<inline:1>", "<inline:named-ext>", "<inline:3>"}, []bool{false, false, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := loadProductionExtensionSet(t, []string{"--no-extensions"}, tc.inline...)
			requirePaths(t, result.paths(), tc.paths)
			for i, ext := range result.Extensions {
				if ext.Hidden != tc.hidden[i] {
					t.Errorf("%s hidden = %v, want %v", ext.Path, ext.Hidden, tc.hidden[i])
				}
			}
		})
	}
}

// A factory that fails is a load error for its path and leaves the other factories loaded (resource-loader.ts:1147-1150).
func TestInlineExtensionFactoryErrorIsALoadErrorForItsPath(t *testing.T) {
	result := loadProductionExtensionSet(t, []string{"--no-extensions"},
		extension.ExtensionFactory(func(extension.API) error { return nil }),
		extension.NamedInlineExtension{Name: "bad", Factory: func(extension.API) error { panic("exploded") }},
	)
	requirePaths(t, result.paths(), []string{"<inline:1>"})
	var failures []string
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Type == "error" {
			failures = append(failures, diagnostic.Message)
		}
	}
	if want := []string{`Failed to load extension "<inline:bad>": exploded`}; !slices.Equal(failures, want) {
		t.Fatalf("error diagnostics = %q, want %q", failures, want)
	}
}
