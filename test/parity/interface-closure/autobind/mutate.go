package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// runMutants applies the mutation check of P1(b) to every library method whose Pi-matched asserting tests have no recorded kill. A
// mutant replaces the method body with its zero results; the first test of the method that passes on the original and fails on the
// mutant is recorded in mutation-checked.json. A mutant that does not compile proves nothing and records nothing. Fields have no mutant.
func (l *library) runMutants(d *detector) error {
	targets := make([]string, 0, len(l.candidates))
	for t := range l.candidates {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	results := map[string][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	tmp, err := os.MkdirTemp("", "autobind-mutants")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for i, target := range targets {
		c := l.candidates[target]
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if killed := l.mutantKills(d, c.sym, c.tests, filepath.Join(tmp, fmt.Sprint(i))); len(killed) > 0 {
				mu.Lock()
				results[target] = killed
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	merged := map[string][]string{}
	for member, refs := range l.mutation {
		for r := range refs {
			merged[member] = append(merged[member], r)
		}
	}
	for member, refs := range results {
		merged[member] = append(merged[member], refs...)
	}
	for _, refs := range merged {
		sort.Strings(refs)
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("mutation: %d of %d candidate methods have a test that fails a mutant\n", len(results), len(targets))
	return os.WriteFile(filepath.Join(l.root, mutationCheckedFile), append(data, '\n'), 0o644)
}

// mutantKills returns the tests that pass on the original method and fail on its mutant.
func (l *library) mutantKills(d *detector, s *sym, tests []string, work string) []string {
	mutant, ok := l.mutantSource(d, s)
	if !ok {
		return nil
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil
	}
	mfile := filepath.Join(work, filepath.Base(s.File))
	overlay := filepath.Join(work, "overlay.json")
	_ = os.WriteFile(mfile, mutant, 0o644)
	ov, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(l.root, s.File): mfile}})
	_ = os.WriteFile(overlay, ov, 0o644)
	var killed []string
	for _, ref := range tests {
		file, name, _ := strings.Cut(strings.TrimPrefix(ref, "test:"), "#")
		pkg := "./" + filepath.ToSlash(filepath.Dir(file))
		run := func(extra ...string) (string, error) {
			args := append([]string{"test", "-count=1", "-run", "^" + name + "$", pkg}, extra...)
			cmd := exec.Command("go", args...)
			cmd.Dir = l.root
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			return out.String(), err
		}
		if _, err := run(); err != nil {
			continue // the test does not pass on the original
		}
		out, err := run("-overlay", overlay)
		if err != nil && !strings.Contains(out, "[build failed]") && !strings.Contains(out, "[setup failed]") {
			killed = append(killed, ref)
			break
		}
	}
	return killed
}

// mutantSource returns the source of the method's file with the method body replaced by its zero results.
func (l *library) mutantSource(d *detector, s *sym) ([]byte, bool) {
	fd := d.methodDecl(s)
	if fd == nil || fd.Body == nil {
		return nil, false
	}
	src, err := os.ReadFile(filepath.Join(l.root, s.File))
	if err != nil {
		return nil, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, s.File, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	var target *ast.FuncDecl
	for _, decl := range f.Decls {
		if x, ok := decl.(*ast.FuncDecl); ok && x.Recv != nil && x.Name.Name == s.Name && recvTypeName(x.Recv.List[0].Type) == s.Owner {
			target = x
		}
	}
	if target == nil {
		return nil, false
	}
	var zero []ast.Expr
	named := false
	if target.Type.Results != nil {
		for _, r := range target.Type.Results.List {
			n := max(len(r.Names), 1)
			if len(r.Names) > 0 {
				named = true
			}
			var tb bytes.Buffer
			_ = format.Node(&tb, fset, r.Type)
			for range n {
				e, err := parser.ParseExpr("*new(" + tb.String() + ")")
				if err != nil {
					return nil, false
				}
				zero = append(zero, e)
			}
		}
	}
	ret := &ast.ReturnStmt{}
	if !named {
		ret.Results = zero
	}
	target.Body = &ast.BlockStmt{List: []ast.Stmt{ret}}
	var out bytes.Buffer
	if err := format.Node(&out, fset, f); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}
