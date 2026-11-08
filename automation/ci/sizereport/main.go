// SPDX-License-Identifier: MIT

// Command sizereport builds a binary as the release does and prints the bytes each package puts in it, so a size regression shows its cause by name.
//
//	go run ./automation/ci/sizereport                          the 40 largest packages of ./cmd/pig for the host target
//	go run ./automation/ci/sizereport -goos windows -goarch amd64 -top 80
//	go run ./automation/ci/sizereport -out before.tsv          save the report
//	go run ./automation/ci/sizereport -base before.tsv         the packages that grew or shrank since the saved report
//	go run ./automation/ci/sizereport -why go/types            the chain of references that keeps a package in the binary
//
// The sizes are symbol sizes of an unstripped build (see internal/sizereport); the stripped size is the number test/perfgate
// TestReleaseBinarySizeGate gates. The report file is tab-separated: a "# stripped" line, then the bytes and the package of each row.
package main

import (
	"bufio"
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/sizereport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sizereport:", err)
		os.Exit(1)
	}
}

func run() error {
	build := sizereport.Build{}
	flag.StringVar(&build.Package, "pkg", "./cmd/pig", "the package to build")
	flag.StringVar(&build.GOOS, "goos", runtime.GOOS, "target operating system")
	flag.StringVar(&build.GOARCH, "goarch", runtime.GOARCH, "target architecture")
	flag.StringVar(&build.Tags, "tags", "", "build tags")
	top := flag.Int("top", 40, "rows to print (0 prints every package)")
	out := flag.String("out", "", "write the full report to this file")
	base := flag.String("base", "", "compare with a report written by -out")
	allowlist := flag.Int64("allowlist", 0, "print the packages that are not in the Go distribution and have at least this many bytes, as the allow list of automation/perf/linked-packages.toml")
	why := flag.String("why", "", "print the chain of references from the program's entry to this package (a path or a path prefix)")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	build.Root = root
	work, err := os.MkdirTemp("", "sizereport-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	ctx := context.Background()
	if *why != "" {
		return printWhy(ctx, build, work, *why)
	}
	symbols, err := build.Symbols(ctx, work)
	if err != nil {
		return err
	}
	totals := sizereport.Sum(symbols)
	if *allowlist > 0 {
		for _, row := range sizereport.Sorted(totals.File) {
			if row.Bytes >= *allowlist && sizereport.IsImportPath(row.Package) && !sizereport.IsStandard(row.Package) {
				fmt.Printf("\t%q,\n", row.Package)
			}
		}
		return nil
	}
	stripped, err := build.StrippedSize(ctx, work)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s/%s stripped (-s -w): %d bytes\n", build.Package, build.GOOS, build.GOARCH, stripped)
	fmt.Printf("symbols in the file: %d bytes in %d packages; zero-filled memory (not in the file): %d bytes\n", sizereport.Total(totals.File), len(totals.File), sizereport.Total(totals.BSS))
	if *out != "" {
		if err := writeReport(*out, stripped, totals.File); err != nil {
			return err
		}
	}
	if *base != "" {
		baseStripped, baseSizes, err := readReport(*base)
		if err != nil {
			return err
		}
		printDiff(stripped, baseStripped, totals.File, baseSizes, *top)
		return nil
	}
	rows := sizereport.Sorted(totals.File)
	if *top > 0 && len(rows) > *top {
		rows = rows[:*top]
	}
	for _, row := range rows {
		fmt.Printf("%10d  %s\n", row.Bytes, row.Package)
	}
	return nil
}

func writeReport(path string, stripped int64, sizes map[string]int64) error {
	var text strings.Builder
	fmt.Fprintf(&text, "# stripped\t%d\n", stripped)
	for _, row := range sizereport.Sorted(sizes) {
		fmt.Fprintf(&text, "%d\t%s\n", row.Bytes, row.Package)
	}
	return os.WriteFile(path, []byte(text.String()), 0o644)
}

func readReport(path string) (stripped int64, sizes map[string]int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = file.Close() }()
	sizes = map[string]int64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		field, rest, _ := strings.Cut(scanner.Text(), "\t")
		if field == "# stripped" {
			stripped, _ = strconv.ParseInt(rest, 10, 64)
			continue
		}
		if size, err := strconv.ParseInt(field, 10, 64); err == nil {
			sizes[rest] = size
		}
	}
	return stripped, sizes, scanner.Err()
}

// printDiff lists the packages whose bytes changed, the largest change first.
func printDiff(stripped, baseStripped int64, sizes, base map[string]int64, top int) {
	fmt.Printf("stripped size: %+d bytes (%d -> %d)\n", stripped-baseStripped, baseStripped, stripped)
	delta := map[string]int64{}
	for pkg, size := range sizes {
		delta[pkg] = size - base[pkg]
	}
	for pkg, size := range base {
		if _, ok := sizes[pkg]; !ok {
			delta[pkg] = -size
		}
	}
	fmt.Printf("symbols: %+d bytes\n", sizereport.Total(sizes)-sizereport.Total(base))
	changed := map[string]int64{}
	for pkg, change := range delta {
		if change != 0 {
			changed[pkg] = change
		}
	}
	rows := make([]sizereport.Entry, 0, len(changed))
	for pkg, change := range changed {
		rows = append(rows, sizereport.Entry{Package: pkg, Bytes: change})
	}
	slices.SortFunc(rows, func(a, b sizereport.Entry) int {
		if c := cmp.Compare(max(b.Bytes, -b.Bytes), max(a.Bytes, -a.Bytes)); c != 0 {
			return c
		}
		return strings.Compare(a.Package, b.Package)
	})
	if top > 0 && len(rows) > top {
		rows = rows[:top]
	}
	for _, row := range rows {
		fmt.Printf("%+10d  %s\n", row.Bytes, row.Package)
	}
}

// printWhy builds with the linker's -dumpdep, which lists every reference between the symbols it kept, and prints the shortest chain from the program's entry to a symbol of the package.
func printWhy(ctx context.Context, build sizereport.Build, work, target string) error {
	edges, err := build.DumpDep(ctx, filepath.Join(work, "pig"))
	if err != nil {
		return err
	}
	chain, viaInit := sizereport.Chain(edges, target)
	if chain == nil {
		fmt.Printf("%s is not linked into %s for %s/%s\n", target, build.Package, build.GOOS, build.GOARCH)
		return nil
	}
	if viaInit {
		fmt.Printf("%s is linked only through its package initializer (an import keeps it; no linked code uses it):\n", target)
	} else {
		fmt.Printf("%s is linked because:\n", target)
	}
	for index, symbol := range chain {
		fmt.Printf("%s%s\n", strings.Repeat("  ", min(index, 1)), symbol)
	}
	return nil
}
