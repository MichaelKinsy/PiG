package surfaces

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// pi: packages/chord/test/surfaces/registered.test.ts. Tests of scripts that register themselves (slice 11 §1, §2): the served
// text calls `chordFacet` with the plugin, the digest and the build's function; evaluating it answers only the client's
// modules and refuses a default export that is no facet. A Go script is a Go function (Pi's is JavaScript text a `vm` runs: language mechanics).

// built is a built script's function as esbuild wraps a facet: it requires specifier, exports a facet.
func built(specifier string) FacetFunction {
	return func(require func(string) (any, error), module *Module) error {
		if _, err := require(specifier); err != nil {
			return err
		}
		module.Exports = ModuleExports{Default: host.DefineFacet(func(host.FacetHost, host.FacetSelf) error { return nil })}
		return nil
	}
}

func TestRegisteredScriptsRegisterTheirPluginDigestAndFunctionTheSourceMapCommentKeptInsideTheCall(t *testing.T) {
	source := "(function (require, module, exports) {\nconst chord = require(\"@acme/app/v1\");\n//# sourceMappingURL=data:application/json;base64,e30=\n})"
	text := RegisteredSource("@acme/notes", "abc123", source)
	if !strings.HasPrefix(text, `chordFacet("@acme/notes","abc123",(function (require, module, exports) {`) {
		t.Fatalf("text = %q", text)
	}
	if want := "e30=\n})\n);\n"; !strings.HasSuffix(text, want) {
		t.Fatalf("text ends %q, want the map comment on its own line before the closing call %q", text[len(text)-30:], want)
	}
	script := RegisteredScript{Plugin: "@acme/notes", Digest: "abc123", Run: built("@acme/app/v1")}
	facet, err := EvaluateRegistered(script, ClientModules{"@acme/app/v1": struct{}{}})
	if err != nil {
		t.Fatal(err)
	}
	if facet == (host.Facet{}) {
		t.Fatal("no facet")
	}
}

func TestRegisteredScriptsAnswerOnlyTheClientsModulesNamingThePlugin(t *testing.T) {
	script := RegisteredScript{Plugin: "@acme/notes", Digest: "d", Run: built("node:fs")}
	_, err := EvaluateRegistered(script, ClientModules{"@acme/app/v1": struct{}{}})
	want := `plugin "@acme/notes": "node:fs" is not one of this client's host packages`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestRegisteredScriptsRefuseADefaultExportThatIsNoFacet(t *testing.T) {
	script := RegisteredScript{Plugin: "@acme/bad", Digest: "d", Run: func(_ func(string) (any, error), module *Module) error {
		module.Exports = ModuleExports{Default: 42}
		return nil
	}}
	_, err := EvaluateRegistered(script, ClientModules{})
	if err == nil || !strings.HasPrefix(err.Error(), `plugin "@acme/bad": its default export is not a facet`) {
		t.Fatalf("err = %v", err)
	}
}

// What the script throws is what evaluating it throws.
func TestEvaluatingARegisteredScriptThrowsWhatTheScriptThrows(t *testing.T) {
	boom := errors.New("broken plugin")
	script := RegisteredScript{Plugin: "@acme/x", Digest: "d", Run: func(func(string) (any, error), *Module) error { return boom }}
	if _, err := EvaluateRegistered(script, ClientModules{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
