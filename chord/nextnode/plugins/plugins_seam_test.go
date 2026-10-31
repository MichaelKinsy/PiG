package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// The reference Builder and Evaluator of the tests: the seams of plugins_build.go and plugins_evaluate.go in their
// simplest form. Upstream builds with esbuild and evaluates with `vm.Script`, which Go cannot (AGENTS.md: no embedded
// JavaScript runtime); these read a facet file as its own script and look an evaluated facet up by the script's filename.

// referenceBuilder builds a facet file as a script of its own text, digested and hashed as `buildFacet` does (sha256, hex).
// A file whose text starts with "syntax error" fails with a build error at its first line, as esbuild reports one.
type referenceBuilder struct {
	// built counts the builds, by "plugin\x00kind".
	built map[string]int
}

var _ Builder = (*referenceBuilder)(nil)

func sha256Hex(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

// Build implements Builder.
func (b *referenceBuilder) Build(plugin Plugin, kind string, options BuildOptions, ctx context.Context) (BuiltFacet, error) {
	if b.built == nil {
		b.built = map[string]int{}
	}
	b.built[plugin.Name+"\x00"+kind]++
	entry := plugin.Facets[kind]
	text, err := os.ReadFile(entry)
	if err != nil {
		return BuiltFacet{}, err
	}
	if strings.HasPrefix(string(text), "syntax error") {
		return BuiltFacet{
			Kind: BuiltFacetFailed, Plugin: plugin.Name, Facet: kind,
			Errors: []string{fmt.Sprintf("%s:1:1: Expected %q", filepath.Base(entry), "}")},
		}, nil
	}
	return BuiltFacet{
		Kind: BuiltFacetScript, Plugin: plugin.Name, Facet: kind,
		Source: string(text), Digest: sha256Hex(text),
		Inputs: []BuildInput{{File: entry, Hash: sha256Hex(text)}},
		Copied: []string{}, Assets: []Asset{},
	}, nil
}

// referenceEvaluator evaluates a script to the facet registered for its filename (ScriptFilename): what a runtime that
// evaluated the script would have produced. Anything else is the evaluator's error, as a default export that is no facet is.
type referenceEvaluator struct {
	facets map[string]host.Facet
}

var _ Evaluator = referenceEvaluator{}

// EvaluateFacet implements Evaluator.
func (e referenceEvaluator) EvaluateFacet(built BuiltFacet, options EvaluateOptions) (host.Facet, error) {
	where := fmt.Sprintf("plugin %q (%s)", options.Plugin.Name, built.Facet)
	if built.Kind != BuiltFacetScript {
		return host.Facet{}, fmt.Errorf("%s: only a built script can be evaluated", where)
	}
	facet, ok := e.facets[ScriptFilename(built, options.Plugin)]
	if !ok || facet.Setup == nil {
		return host.Facet{}, fmt.Errorf("%s: its default export is not a facet (defineFacet)", where)
	}
	return facet, nil
}
