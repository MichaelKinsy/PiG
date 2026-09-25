package main

import (
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestCandidatePackageExcludesTestAndParityHelpers(t *testing.T) {
	for _, path := range []string{
		"github.com/MichaelKinsy/PiG/coding/extension/extensiontest",
		"github.com/MichaelKinsy/PiG/tui/parity",
		"github.com/MichaelKinsy/PiG/tui/termsim",
	} {
		if candidatePackage(path) {
			t.Errorf("candidatePackage(%q) = true", path)
		}
	}
	if !candidatePackage("github.com/MichaelKinsy/PiG/internal/codingagent") {
		t.Fatal("production package rejected")
	}
}

func TestRecordForObjectStableShapeHash(t *testing.T) {
	pkg := types.NewPackage("example.test/sdk", "sdk")
	params := types.NewTuple(types.NewParam(token.NoPos, pkg, "value", types.Typ[types.String]))
	results := types.NewTuple(types.NewParam(token.NoPos, pkg, "", types.Typ[types.Bool]))
	fn := types.NewFunc(token.NoPos, pkg, "Resolve", types.NewSignatureType(nil, nil, nil, params, results, false))
	first := recordForObject(pkg.Path(), fn.Name(), fn, func(*types.Package) string { return "" })
	second := recordForObject(pkg.Path(), fn.Name(), fn, func(*types.Package) string { return "" })
	if first.ShapeHash != second.ShapeHash || !strings.HasPrefix(first.ShapeHash, "sha256:") {
		t.Fatalf("unstable shape hashes: %q %q", first.ShapeHash, second.ShapeHash)
	}
	if string(first.Shape) != `{"type":"func(value string) bool"}` {
		t.Fatalf("shape = %s", first.Shape)
	}
}

func TestExtractIncludesExportedAndInternalProductionSymbols(t *testing.T) {
	result, err := extract([]string{"github.com/MichaelKinsy/PiG/coding/extension"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range result.Interfaces {
		seen[entry.ID] = true
	}
	for _, id := range []string{
		"go:github.com/MichaelKinsy/PiG/coding/extension#API",
		"go:github.com/MichaelKinsy/PiG/coding/extension#Context.CWD",
		"go:github.com/MichaelKinsy/PiG/coding/extension#ProjectTrustEvent",
		"go:github.com/MichaelKinsy/PiG/coding/extension#ctxKey",
	} {
		if !seen[id] {
			t.Errorf("missing %s", id)
		}
	}
}
