// SPDX-License-Identifier: MIT

package sizereport

import (
	"os"
	"path/filepath"
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

// Mach-O and PE tables record no sizes, so nm reports the distance to the next symbol, gaps included; the linux sizes of the same data symbols are the real ones.
func TestExactDataSizes(t *testing.T) {
	pe := []Symbol{
		{Size: 13760, Kind: 'D', Name: "golang.org/x/text/unicode/bidi.bidiIndex"},
		{Size: 3328, Kind: 'R', Name: "github.com/MichaelKinsy/PiG/internal/codingagent/export.templateFS.files"},
		{Size: 900, Kind: 'T', Name: "pkg.F"},
		{Size: 5000, Kind: 'r', Name: "go:func.*"},
		{Size: 64, Kind: 'D', Name: "pkg.onlyOnWindows"},
		{Size: 100, Kind: 'D', Name: "pkg.grew"},
	}
	elf := []Symbol{
		{Size: 3328, Kind: 'D', Name: "golang.org/x/text/unicode/bidi.bidiIndex"},
		{Size: 408, Kind: 'r', Name: "github.com/MichaelKinsy/PiG/internal/codingagent/export.templateFS.files"},
		{Size: 100, Kind: 'T', Name: "pkg.F"},
		{Size: 10, Kind: 'r', Name: "go:func.*"},
		{Size: 200, Kind: 'D', Name: "pkg.grew"},
	}
	got := ExactDataSizes(pe, elf, func(string) bool { return true })
	want := []int64{3328, 408, 900, 5000, 64, 100}
	for i, symbol := range got {
		if symbol.Size != want[i] {
			t.Errorf("%s: size %d, want %d", symbol.Name, symbol.Size, want[i])
		}
	}
	if pe[0].Size != 13760 {
		t.Error("ExactDataSizes changed its input")
	}
}

// A package that compiles different files on linux and on the target may declare a symbol of the same name with a different size on each, so the linux size must not replace the target's: the symbol of an untrusted package keeps nm's size.
func TestExactDataSizesKeepsPackagesWithOtherFiles(t *testing.T) {
	pe := []Symbol{
		{Size: 40960, Kind: 'D', Name: "pkg/perOS.table"},
		{Size: 13760, Kind: 'D', Name: "pkg/same.table"},
	}
	elf := []Symbol{
		{Size: 64, Kind: 'D', Name: "pkg/perOS.table"},
		{Size: 3328, Kind: 'D', Name: "pkg/same.table"},
	}
	got := ExactDataSizes(pe, elf, func(pkg string) bool { return pkg == "pkg/same" })
	if got[0].Size != 40960 || got[1].Size != 3328 {
		t.Errorf("sizes %d, %d; want 40960 (other files, kept) and 3328 (same files, exact)", got[0].Size, got[1].Size)
	}
}

// The same table name with 64 B in a linux file and 40 KB in a !linux file (a per-OS data table) must show its 40 KB on darwin: the linux build's size of the name is not the table's size there.
func TestSymbolsKeepPerOSTableSize(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module for two targets")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":           "module example.test/m\n\ngo 1.24\n",
		"main.go":          "package main\n\nimport (\n\t\"os\"\n\n\t\"example.test/m/same\"\n\t\"example.test/m/tbl\"\n)\n\nfunc main() { println(tbl.Table[len(os.Args)], same.Table[len(os.Args)]) }\n",
		"tbl/tbl_linux.go": "package tbl\n\nvar Table = [64]byte{1}\n",
		"tbl/tbl_other.go": "//go:build !linux\n\npackage tbl\n\nvar Table = [40960]byte{1}\n",
		"same/same.go":     "package same\n\nvar Table = [3328]byte{1}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := Build{Root: root, Package: ".", GOOS: "darwin", GOARCH: "arm64"}
	symbols, err := build.Symbols(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int64{}
	for _, symbol := range symbols {
		sizes[symbol.Name] = symbol.Size
	}
	if got := sizes["example.test/m/tbl.Table"]; got < 40960 {
		t.Errorf("per-OS table: size %d, want at least 40960 (the linux build's 64 B hid it)", got)
	}
	if got := sizes["example.test/m/same.Table"]; got != 3328 {
		t.Errorf("same-file table: size %d, want 3328", got)
	}
}
