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
	writeFiles(t, root, files)
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

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Residual N1, pinned as a known limitation: one shared file declares a table whose size depends on a per-OS constant (here 64 B on linux, 20,064 B on darwin). The file sets match, so the linux size replaces the target's and the per-package gate sees 64 B; only the stripped-size ceilings bound the growth, in total.
//
// The gate cannot catch it without false positives. The extra bytes of a Mach-O or PE symbol over its real size are the linker's gap to the next symbol, and in the release binary that gap is unbounded for symbols whose real size is the same on every OS: on darwin_arm64 `sync..dict.entry[...]` spans 57,632 B for 480 B (a 57,152 B gap), `chacha20.incRotMatrix` 22,080 B for 32 B, `bidi.bidiIndex` 13,760 B for 3,328 B, and on windows_amd64 `gc/scan.gcExpandersAVX512` 47,680 B for 544 B. A rule that refuses the correction when the span exceeds the linux size by more than some bound either misses a table that grows by less than the bound or reports those packages as heavyweight again. If a size source without gaps for the target is found (for example the compiler's own symbol sizes) and the gate catches this table, change the want here to the target's size.
func TestSymbolsPinSharedFileTableOfPerOSSize(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module for two targets")
	}
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":           "module example.test/m\n\ngo 1.24\n",
		"main.go":          "package main\n\nimport (\n\t\"os\"\n\n\t\"example.test/m/shared\"\n)\n\nfunc main() { println(shared.Table[len(os.Args)]) }\n",
		"shared/shared.go": "package shared\n\nimport \"runtime\"\n\n// len(\"linux\") is 5 and len(\"darwin\") is 6.\nvar Table = [64 + (len(runtime.GOOS)-5)*20000]byte{1}\n",
	})
	build := Build{Root: root, Package: ".", GOOS: "darwin", GOARCH: "arm64"}
	raw, err := build.nmSymbols(t.Context(), filepath.Join(t.TempDir(), "unstripped"))
	if err != nil {
		t.Fatal(err)
	}
	corrected, err := build.Symbols(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const name = "example.test/m/shared.Table"
	sizeOf := func(symbols []Symbol) (size int64) {
		for _, symbol := range symbols {
			if symbol.Name == name {
				size = symbol.Size
			}
		}
		return size
	}
	if got := sizeOf(raw); got < 20064 {
		t.Fatalf("nm size of the darwin table is %d, want at least 20064: the module does not declare the table this test pins", got)
	}
	if got := sizeOf(corrected); got != 64 {
		t.Errorf("corrected size of the table is %d, want 64 (the pinned limitation: the linux size of a same-file table is trusted); if the gate now reports its real size, change this want and the comment", got)
	}
}
