// SPDX-License-Identifier: MIT

// Package sizereport attributes the bytes of a linked Go binary to the packages that put them there. It reads the symbol table
// that `go tool nm -size` prints for an unstripped build, so a change that makes a dead package live shows the package by name.
//
// The sizes are symbol sizes: they count code, read-only data and initialized data, and leave out zero-filled memory (bss), which
// takes no space in the file. They do not add up to the file size: the executable header, the runtime's type and function tables
// are attributed to the packages they describe only where the symbol name says so (the table symbols `go:func.*`, `go:itab.*` and
// `go:string.*` stay apart under their own names).
package sizereport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Symbol is one row of `go tool nm -size`.
type Symbol struct {
	Size int64
	// Kind is nm's one-letter type: a lower case letter is a local symbol, B and b are zero-filled memory.
	Kind byte
	Name string
}

// nmRow is a row of `go tool nm -size`: an address in hexadecimal (a symbol may have none), the size in decimal, the type letter and the name, which may hold spaces.
var nmRow = regexp.MustCompile(`^\s*(?:[0-9a-f]+\s+)?(\d+)\s+(\S)\s(.*)$`)

// ParseNM reads the output of `go tool nm -size`.
func ParseNM(r io.Reader) ([]Symbol, error) {
	var symbols []Symbol
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		row := nmRow.FindStringSubmatch(scanner.Text())
		if row == nil {
			continue
		}
		size, err := strconv.ParseInt(row[1], 10, 64)
		if err != nil {
			continue
		}
		symbols = append(symbols, Symbol{Size: size, Kind: row[2][0], Name: row[3]})
	}
	return symbols, scanner.Err()
}

// PackageOf is the package a symbol belongs to. Compiler tables are named for what they are: "go:func", "go:itab", "go:string". A type descriptor belongs to the package of its type; a generic instantiation to the package that declares the generic function.
func PackageOf(name string) string {
	switch {
	case strings.HasPrefix(name, "go:"):
		kind, _, _ := strings.Cut(name, ".")
		return kind
	case strings.HasPrefix(name, "type:"):
		name = strings.TrimPrefix(name, "type:")
		name = strings.TrimLeft(name, "*[]")
		name = strings.TrimPrefix(name, ".eq.")
		name = strings.TrimPrefix(name, ".hash.")
		pkg := packageOfQualified(name)
		if pkg == "" {
			return "type:(unnamed)"
		}
		return pkg
	}
	if pkg := packageOfQualified(name); pkg != "" {
		return pkg
	}
	return "(no package)"
}

// packageOfQualified is the import path before the first dot after the last slash of the symbol's own name: the linker escapes a dot in the last element of an import path, so it ends the path. The name stops at the first "[" (a generic instantiation's type arguments) or "(" (a method's receiver).
func packageOfQualified(name string) string {
	if end := strings.IndexAny(name, "[("); end >= 0 {
		name = name[:end]
	}
	slash := strings.LastIndexByte(name, '/')
	dot := strings.IndexByte(name[slash+1:], '.')
	if dot < 0 {
		return ""
	}
	return name[:slash+1+dot]
}

// IsImportPath reports whether name is an import path and not a table name such as "go:func", "type:(unnamed)" or "(no package)".
func IsImportPath(name string) bool { return name != "" && !strings.ContainsAny(name, ":() ") }

// IsStandard reports whether pkg belongs to the Go distribution: the first element of its import path has no dot ("net/http", "vendor/golang.org/x/net/idna"; not "golang.org/x/text/collate").
func IsStandard(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}

// Totals are the bytes of a binary per package.
type Totals struct {
	// File is the size of the symbols that take space in the file.
	File map[string]int64
	// BSS is the size of zero-filled memory, which does not.
	BSS map[string]int64
}

// Sum attributes symbols to packages.
func Sum(symbols []Symbol) Totals {
	totals := Totals{File: map[string]int64{}, BSS: map[string]int64{}}
	for _, symbol := range symbols {
		pkg := PackageOf(symbol.Name)
		if symbol.Kind == 'B' || symbol.Kind == 'b' {
			totals.BSS[pkg] += symbol.Size
		} else {
			totals.File[pkg] += symbol.Size
		}
	}
	return totals
}

// Total is the sum of the sizes.
func Total(sizes map[string]int64) (total int64) {
	for _, size := range sizes {
		total += size
	}
	return total
}

// Entry is one package and its bytes.
type Entry struct {
	Package string
	Bytes   int64
}

// Sorted lists the packages largest first, ties by name.
func Sorted(sizes map[string]int64) []Entry {
	entries := make([]Entry, 0, len(sizes))
	for pkg, size := range sizes {
		entries = append(entries, Entry{pkg, size})
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Package, b.Package)
	})
	return entries
}

// Build is one build of a binary: the package to build, the target and the tags.
type Build struct {
	Root       string
	Package    string
	GOOS       string
	GOARCH     string
	Tags       string
	ExtraFlags []string
}

func (b Build) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	//nolint:gosec // G204 (class b): the program is the go command and the arguments are fixed build flags and a package path the developer or the test chose.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = b.Root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+b.GOOS, "GOARCH="+b.GOARCH)
	return cmd
}

// Binary builds the package as the release build does (CGO_ENABLED=0, -trimpath, -buildvcs=false) into out. strip adds -ldflags "-s -w", which is what the size gate measures; without it the binary keeps the symbol table nm reads. The code is the same either way.
func (b Build) Binary(ctx context.Context, out string, strip bool, ldflags ...string) error {
	args := []string{"build", "-trimpath", "-buildvcs=false"}
	flags := slices.Clone(ldflags)
	if strip {
		flags = append(flags, "-s", "-w")
	}
	if len(flags) > 0 {
		args = append(args, "-ldflags", strings.Join(flags, " "))
	}
	if b.Tags != "" {
		args = append(args, "-tags", b.Tags)
	}
	args = append(args, b.ExtraFlags...)
	args = append(args, "-o", out, b.Package)
	if data, err := b.command(ctx, "go", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s for %s/%s: %w\n%s", b.Package, b.GOOS, b.GOARCH, err, data)
	}
	return nil
}

// Symbols builds the package without stripping it and returns the symbols of its symbol table.
func (b Build) Symbols(ctx context.Context, workDir string) ([]Symbol, error) {
	unstripped := filepath.Join(workDir, "unstripped")
	if err := b.Binary(ctx, unstripped, false); err != nil {
		return nil, err
	}
	nm := b.command(ctx, "go", "tool", "nm", "-size", unstripped)
	out, err := nm.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = nm.Start(); err != nil {
		return nil, err
	}
	symbols, err := ParseNM(out)
	if waitErr := nm.Wait(); err == nil {
		err = waitErr
	}
	return symbols, err
}

// StrippedSize builds the package with -s -w and returns the size of the binary, the number the size gate measures.
func (b Build) StrippedSize(ctx context.Context, workDir string) (int64, error) {
	stripped := filepath.Join(workDir, "stripped")
	if err := b.Binary(ctx, stripped, true); err != nil {
		return 0, err
	}
	info, err := os.Stat(stripped)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DumpDep builds the package with the linker's -dumpdep and returns the references it kept, from each symbol to the symbols it uses. The entry is the symbol "_".
func (b Build) DumpDep(ctx context.Context, out string) (map[string][]string, error) {
	args := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=-dumpdep"}
	if b.Tags != "" {
		args = append(args, "-tags", b.Tags)
	}
	cmd := b.command(ctx, "go", append(args, "-o", out, b.Package)...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	edges := map[string][]string{}
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		if from, to, ok := strings.Cut(scanner.Text(), " -> "); ok {
			edges[from] = append(edges[from], to)
		}
	}
	scanErr := scanner.Err()
	if err = cmd.Wait(); err != nil {
		return nil, fmt.Errorf("go build -ldflags=-dumpdep %s: %w", b.Package, err)
	}
	return edges, scanErr
}

// isInitTask is the linker's symbol for a package's initializers and for the list that runs them all: every imported package has one, so a chain through it says only that the package is imported.
func isInitTask(symbol string) bool {
	return symbol == "go:main.inittasks" || strings.HasSuffix(symbol, "..inittask")
}

// Chain is the shortest chain of references from the entry to a symbol of the package target or of a package below it, or nil when no symbol of it is linked. It follows the code that uses the package; a package that only an import keeps (its initializers run, no other symbol of it is used) has no chain, and Chain then returns the chain through the initializers with viaInit set.
func Chain(edges map[string][]string, target string) (chain []string, viaInit bool) {
	if chain = chainFrom(edges, target, false); chain != nil {
		return chain, false
	}
	return chainFrom(edges, target, true), true
}

func chainFrom(edges map[string][]string, target string, throughInit bool) []string {
	inTarget := func(symbol string) bool {
		pkg := PackageOf(symbol)
		return (pkg == target || strings.HasPrefix(pkg, target+"/")) && (throughInit || !isInitTask(symbol))
	}
	parent := map[string]string{"_": ""}
	queue := []string{"_"}
	for len(queue) > 0 {
		symbol := queue[0]
		queue = queue[1:]
		for _, next := range edges[symbol] {
			if _, seen := parent[next]; seen || (!throughInit && isInitTask(next)) {
				continue
			}
			parent[next] = symbol
			if inTarget(next) {
				var chain []string
				for at := next; at != ""; at = parent[at] {
					chain = append(chain, at)
				}
				slices.Reverse(chain)
				return chain
			}
			queue = append(queue, next)
		}
	}
	return nil
}
