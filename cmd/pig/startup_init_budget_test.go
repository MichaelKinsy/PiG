package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Every package-level regexp in a package linked into pig compiles when the process starts, whether or not the run reaches its user. internal/lazyregexp moves that work to the first use. This test walks the packages of this binary and rejects a regexp.MustCompile in a package-level variable, so a new one cannot bring the start-up cost back.
func TestLinkedPackagesCompileNoRegexpAtInit(t *testing.T) {
	command := exec.CommandContext(testbudget.Context(t), "go", "list", "-deps", "-f",
		`{{if and (not .Standard) .Module}}{{if eq .Module.Path "github.com/MichaelKinsy/PiG"}}{{.Dir}}{{range .GoFiles}}|{{.}}{{end}}{{end}}{{end}}`, ".")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var offenders []string
	packages := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			continue
		}
		packages++
		for _, name := range fields[1:] {
			path := filepath.Join(fields[0], name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					for _, value := range spec.(*ast.ValueSpec).Values {
						ast.Inspect(value, func(node ast.Node) bool {
							if _, isFunc := node.(*ast.FuncLit); isFunc {
								return false
							}
							if call, ok := node.(*ast.CallExpr); ok {
								if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
									if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "regexp" && selector.Sel.Name == "MustCompile" {
										offenders = append(offenders, path+":"+strconv.Itoa(fset.Position(call.Pos()).Line))
									}
								}
							}
							return true
						})
					}
				}
			}
		}
	}
	if packages < 20 {
		t.Fatalf("go list reported %d PiG packages; the walk does not cover the binary", packages)
	}
	if len(offenders) != 0 {
		t.Fatalf("package-level regexp.MustCompile runs at process start; use lazyregexp.New:\n%s", strings.Join(offenders, "\n"))
	}
}

// Lazy patterns skip the panic regexp.MustCompile raises at init, so every pattern in the binary must compile here.
func TestLazyRegexpPatternsAllCompile(t *testing.T) {
	if invalid := lazyregexp.CompileAll(); invalid != "" {
		t.Fatal(invalid)
	}
}

const (
	// packageInitBytesBudget and packageInitAllocsBudget bound the package initializers of this module. They are measured through the runtime's own init accounting, which does not depend on machine speed. The binary spent 2.0 MB in 18140 allocations before package-level regexps and catalog work moved out of init; 0.83 MB in 9701 allocations after. Raise a budget only with a profile that shows the new init work is needed before main.
	packageInitBytesBudget  = 1_000_000
	packageInitAllocsBudget = 11_500
)

func TestPackageInitWorkBudget(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	command := exec.CommandContext(testbudget.Context(t), binary, "--version")
	command.Env = append(os.Environ(), "GODEBUG=inittrace=1", "PIG_HOME="+t.TempDir())
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("pig --version: %v\n%s", err, stderr.String())
	}
	line := regexp.MustCompile(`^init (\S+) @[\d.]+ ms, [\d.]+ ms clock, (\d+) bytes, (\d+) allocs$`)
	type initWork struct {
		pkg          string
		bytes, count int
	}
	var work []initWork
	var totalBytes, totalAllocs int
	for text := range strings.SplitSeq(stderr.String(), "\n") {
		match := line.FindStringSubmatch(text)
		if match == nil || !strings.HasPrefix(match[1], "github.com/MichaelKinsy/PiG") {
			continue
		}
		bytes, _ := strconv.Atoi(match[2])
		count, _ := strconv.Atoi(match[3])
		work = append(work, initWork{match[1], bytes, count})
		totalBytes += bytes
		totalAllocs += count
	}
	if len(work) < 20 {
		t.Fatalf("parsed %d PiG package initializers from:\n%s", len(work), stderr.String())
	}
	if totalBytes > packageInitBytesBudget || totalAllocs > packageInitAllocsBudget {
		slices.SortFunc(work, func(a, b initWork) int { return b.bytes - a.bytes })
		var report strings.Builder
		for _, item := range work[:min(8, len(work))] {
			report.WriteString("\n  " + item.pkg + ": " + strconv.Itoa(item.bytes) + " bytes, " + strconv.Itoa(item.count) + " allocs")
		}
		t.Fatalf("PiG package initializers use %d bytes in %d allocations, budget %d bytes in %d allocations. Largest:%s",
			totalBytes, totalAllocs, packageInitBytesBudget, packageInitAllocsBudget, report.String())
	}
}
