// SPDX-License-Identifier: MIT

package sizereport

import (
	"strings"
	"testing"
)

func TestPackageOf(t *testing.T) {
	for name, want := range map[string]string{
		"github.com/MichaelKinsy/PiG/internal/typebox.(*errorWalker).check":  "github.com/MichaelKinsy/PiG/internal/typebox",
		"github.com/MichaelKinsy/PiG/internal/typebox.Check":                 "github.com/MichaelKinsy/PiG/internal/typebox",
		"golang.org/x/text/collate.mainValues":                               "golang.org/x/text/collate",
		"runtime.pclntab":                                                    "runtime",
		"slices.pdqsortCmpFunc[go.shape.struct { a string }]":                "slices",
		"gopkg.in/yaml%2ev3.(*parser).parse":                                 "gopkg.in/yaml%2ev3",
		"github.com/MichaelKinsy/PiG/ai..gobytes.1":                          "github.com/MichaelKinsy/PiG/ai",
		"github.com/MichaelKinsy/PiG/internal/codingagent.run.func1":         "github.com/MichaelKinsy/PiG/internal/codingagent",
		"type:*github.com/MichaelKinsy/PiG/third_party/jsonschema.Schema":    "github.com/MichaelKinsy/PiG/third_party/jsonschema",
		"type:.eq.github.com/MichaelKinsy/PiG/ai.Message":                    "github.com/MichaelKinsy/PiG/ai",
		"type:M4K4IM16SM13K3M24SM13K3M8I":                                    "type:(unnamed)",
		"go:func.*":                                                          "go:func",
		"go:itab.github.com/MichaelKinsy/PiG/ai.T,github.com/MichaelKinsy/I": "go:itab",
		"_rt0_amd64_linux":                                                   "(no package)",
		"crypto/internal/fips140/drbg.memory":                                "crypto/internal/fips140/drbg",
		"go.yaml.in/yaml/v3.(*decoder).unmarshal":                            "go.yaml.in/yaml/v3",
	} {
		if got := PackageOf(name); got != want {
			t.Errorf("PackageOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestParseNMAndSum(t *testing.T) {
	const nm = ` 4763280   33554432 B crypto/internal/fips140/drbg.memory
 39c71c8    3377976 r go:func.*
 44bd5c0    1005824 D golang.org/x/text/collate.mainValues
            1024 T runtime.main
 4000000       16 T slices.Index[go.shape.[]string,go.shape.string]
junk line
`
	symbols, err := ParseNM(strings.NewReader(nm))
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 5 {
		t.Fatalf("parsed %d symbols, want 5: %+v", len(symbols), symbols)
	}
	totals := Sum(symbols)
	if totals.BSS["crypto/internal/fips140/drbg"] != 33554432 || totals.File["crypto/internal/fips140/drbg"] != 0 {
		t.Errorf("a bss symbol counts as file bytes: %+v", totals)
	}
	if totals.File["go:func"] != 3377976 || totals.File["golang.org/x/text/collate"] != 1005824 || totals.File["runtime"] != 1024 || totals.File["slices"] != 16 {
		t.Errorf("file totals: %+v", totals.File)
	}
}

func TestChain(t *testing.T) {
	edges := map[string][]string{
		"_":                          {"main.main"},
		"main.main":                  {"pkg/a.Run", "pkg/b.Run"},
		"pkg/a.Run":                  {"type:pkg/c.T"},
		"pkg/b.Run":                  {"pkg/c/sub.F"},
		"pkg/c/sub.F":                {"pkg/d.G"},
		"type:pkg/c.T":               {"pkg/d.G"},
		"unreferenced/pkg.Dead.func": {"pkg/e.H"},
	}
	chain, viaInit := Chain(edges, "pkg/c")
	if strings.Join(chain, " ") != "_ main.main pkg/a.Run type:pkg/c.T" || viaInit {
		t.Errorf("chain to pkg/c: %v (via init %v)", chain, viaInit)
	}
	if chain, _ := Chain(edges, "pkg/e"); chain != nil {
		t.Errorf("a package no live symbol references has a chain: %v", chain)
	}
	edges["_"] = append(edges["_"], "go:main.inittasks")
	edges["go:main.inittasks"] = []string{"pkg/f..inittask"}
	if chain, viaInit := Chain(edges, "pkg/f"); strings.Join(chain, " ") != "_ go:main.inittasks pkg/f..inittask" || !viaInit {
		t.Errorf("chain to a package only its initializer keeps: %v (via init %v)", chain, viaInit)
	}
}
