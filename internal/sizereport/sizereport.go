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

// Symbols builds the package without stripping it and returns the symbols of its symbol table, with the sizes of the data symbols made exact (see ExactDataSizes) for a target whose symbol table does not record sizes.
func (b Build) Symbols(ctx context.Context, workDir string) ([]Symbol, error) {
	symbols, err := b.nmSymbols(ctx, filepath.Join(workDir, "unstripped"))
	if err != nil || b.GOOS == "linux" {
		return symbols, err
	}
	reference := b
	reference.GOOS = "linux"
	exact, err := reference.nmSymbols(ctx, filepath.Join(workDir, "reference"))
	if err != nil {
		return nil, err
	}
	target, err := b.compiledFiles(ctx)
	if err != nil {
		return nil, err
	}
	linux, err := reference.compiledFiles(ctx)
	if err != nil {
		return nil, err
	}
	return ExactDataSizes(symbols, exact, func(pkg string) bool {
		files, ok := target[pkg]
		return ok && files == linux[pkg]
	}), nil
}

// nmSymbols builds the package unstripped into out and reads its symbol table.
func (b Build) nmSymbols(ctx context.Context, out string) ([]Symbol, error) {
	if err := b.Binary(ctx, out, false); err != nil {
		return nil, err
	}
	nm := b.command(ctx, "go", "tool", "nm", "-size", out)
	stdout, err := nm.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = nm.Start(); err != nil {
		return nil, err
	}
	symbols, err := ParseNM(stdout)
	if waitErr := nm.Wait(); err == nil {
		err = waitErr
	}
	return symbols, err
}

// compiledFiles lists, for every package the build links, the files the compiler gets for the build's target after build constraints: Go, cgo, C, assembly and object files and the files its embed directives pick up. A file set is one comparable string. The key is the package as the linker writes it in symbol names (a dot in the last element of the import path becomes %2e).
func (b Build) compiledFiles(ctx context.Context) (map[string]string, error) {
	args := []string{"list", "-deps", "-f", "{{.ImportPath}}\t{{.GoFiles}} {{.CgoFiles}} {{.CFiles}} {{.SFiles}} {{.SysoFiles}} {{.EmbedFiles}}"}
	if b.Tags != "" {
		args = append(args, "-tags", b.Tags)
	}
	args = append(args, b.Package)
	out, err := b.command(ctx, "go", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps %s for %s/%s: %w", b.Package, b.GOOS, b.GOARCH, err)
	}
	files := map[string]string{}
	//portlint:allow pathseparators go list writes \n line ends only on every platform; Go-only build tooling, no Pi counterpart
	for line := range strings.SplitSeq(string(out), "\n") {
		if pkg, set, ok := strings.Cut(line, "\t"); ok {
			files[linkerPath(pkg)] = set
		}
	}
	return files, nil
}

// linkerPath is the import path as the linker writes it in symbol names.
func linkerPath(pkg string) string {
	slash := strings.LastIndexByte(pkg, '/')
	return pkg[:slash+1] + strings.ReplaceAll(pkg[slash+1:], ".", "%2e")
}

// ExactDataSizes corrects the sizes of the data symbols of a Mach-O or PE binary with those of the same program built for linux on the same architecture. ELF symbol tables record each symbol's size; Mach-O and PE tables do not, and `go tool nm -size` then reports the distance to the next symbol in the table, which includes any symbols the linker leaves out of the table. A table such as bidi.bidiIndex (3,328 B) was reported as 13,760 B on darwin_arm64 and the embed.FS file list of export (408 B) as 3,328 B on windows_amd64, which put a package over the heavyweight line only because of where the linker had placed it. A data symbol's size does not depend on the operating system when the package compiles the same files on both, so for the packages sameFiles accepts the smaller of the two sizes is the real one. A package whose file set differs between the systems may declare a symbol of the same name with another size on each (a table in a !linux file), so its symbols keep the size nm reported: that never under-reports. Code and the linker's own tables (names that start with "go:") keep the reported size too, since they differ between the operating systems.
//
// Residual: a file shared by linux and the target can declare a table whose size depends on a per-OS constant (an array sized by a value a per-OS file defines). The file sets match, so the linux size is trusted and growth of that table on the target stays hidden from the per-package gate; only the stripped-size ceilings in ratchet.toml bound it, in total.
func ExactDataSizes(symbols, linux []Symbol, sameFiles func(pkg string) bool) []Symbol {
	exact := make(map[string]int64, len(linux))
	for _, symbol := range linux {
		if isData(symbol) {
			exact[symbol.Name] = symbol.Size
		}
	}
	corrected := slices.Clone(symbols)
	for i, symbol := range corrected {
		if size, ok := exact[symbol.Name]; ok && isData(symbol) && size < symbol.Size && sameFiles(PackageOf(symbol.Name)) {
			corrected[i].Size = size
		}
	}
	return corrected
}

// isData reports whether symbol is an initialized data symbol of a program's own packages: nm's types D and R and their local forms.
func isData(symbol Symbol) bool {
	return strings.ContainsRune("DdRr", rune(symbol.Kind)) && !strings.HasPrefix(symbol.Name, "go:")
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
