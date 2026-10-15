// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/ai"
)

var portsRe = regexp.MustCompile(`packages/[A-Za-z0-9_./-]+\.ts`)

// portedSources lists the upstream TypeScript files the file claims to port.
func (p *Pass) portedSources(pos token.Pos) []string {
	f := p.fileOf(pos)
	if f == nil {
		return nil
	}
	var out []string
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "// Ports ") {
				out = append(out, portsRe.FindAllString(c.Text, -1)...)
			}
		}
	}
	return out
}

var (
	orderingOnce  sync.Once
	orderingFiles map[string]bool
)

// orderingSensitive loads, from test/parity/async-contracts.toml, the upstream files whose contract includes microtask-ordering.
func orderingSensitive(dir string) map[string]bool {
	orderingOnce.Do(func() {
		orderingFiles = map[string]bool{}
		for d := dir; ; d = filepath.Dir(d) {
			path := filepath.Join(d, "test", "parity", "async-contracts.toml")
			if _, err := os.Stat(path); err == nil {
				var ledger struct {
					Files []struct {
						Path      string   `toml:"path"`
						Contracts []string `toml:"contracts"`
					} `toml:"files"`
				}
				if _, err := toml.DecodeFile(path, &ledger); err == nil {
					for _, f := range ledger.Files {
						if slices.Contains(f.Contracts, "microtask-ordering") {
							orderingFiles[f.Path] = true
						}
					}
				}
				return
			}
			if d == filepath.Dir(d) {
				return
			}
		}
	})
	return orderingFiles
}

// AsyncOrder flags a goroutine hand-off with no synchronization point in code whose upstream contract is ordering-sensitive.
var AsyncOrder = newCheck("asyncorder",
	"go statement with no later join (WaitGroup.Wait, channel receive, select) in a file whose upstream async contract is microtask-ordering",
	High, "TaskAbort setTimeout(0) precondition; durable reservation order",
	func(p *Pass) {
		p.Funcs(func(_ string, _ *ast.FuncType, body *ast.BlockStmt) {
			var gos []*ast.GoStmt
			joined := false
			ast.Inspect(body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncLit:
					return n.Body == body
				case *ast.GoStmt:
					gos = append(gos, n)
				case *ast.SelectStmt:
					joined = true
				case *ast.UnaryExpr:
					if n.Op == token.ARROW {
						joined = true
					}
				case *ast.RangeStmt:
					joined = joined || strings.Contains(p.TypesInfo.TypeOf(n.X).String(), "chan")
				case *ast.CallExpr:
					if sel, ok := n.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Wait" || sel.Sel.Name == "Go") {
						joined = true
					}
				}
				return true
			})
			if joined || len(gos) == 0 {
				return
			}
			for _, g := range gos {
				if p.IsTest(g.Pos()) {
					continue
				}
				dir := filepath.Dir(p.Fset.Position(g.Pos()).Filename)
				for _, src := range p.portedSources(g.Pos()) {
					if orderingSensitive(dir)[src] {
						p.Reportf(g.Pos(), "goroutine hand-off in code ported from %s (microtask-ordering) with no join or ordering point", src)
						break
					}
				}
			}
		})
	})

var providerIDs = sync.OnceValue(func() map[string]bool {
	out := map[string]bool{}
	for _, id := range ai.ListProviders() {
		out[id] = true
	}
	return out
})

// ProviderLiteral flags a provider ID compared as a literal in shared code. Pi branches on catalog data.
var ProviderLiteral = newCheck("providerliteral",
	"built-in provider ID compared as a string literal outside that provider's own file: Pi uses the model catalog, defaultModelPerProvider and auth metadata",
	Med, "Copilot-only special cases in shared paths",
	func(p *Pass) {
		ids := providerIDs()
		report := func(pos token.Pos, base, lit string) {
			if p.IsTest(pos) {
				return
			}
			flat := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSuffix(p.FileName(pos), ".go")))
			if strings.Contains(flat, strings.ReplaceAll(lit, "-", "")) {
				return
			}
			p.Reportf(pos, "provider literal %q in shared code; use catalog or auth metadata", lit)
		}
		p.Inspect.Preorder([]ast.Node{(*ast.BinaryExpr)(nil), (*ast.SwitchStmt)(nil)}, func(n ast.Node) {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op != token.EQL && n.Op != token.NEQ {
					return
				}
				for _, pair := range [][2]ast.Expr{{n.X, n.Y}, {n.Y, n.X}} {
					if s, ok := StringLit(pair[0]); ok && ids[s] && providerNameRe.MatchString(exprText(pair[1])) {
						report(pair[0].Pos(), "", s)
					}
				}
			case *ast.SwitchStmt:
				if n.Tag == nil || !providerNameRe.MatchString(exprText(n.Tag)) {
					return
				}
				for _, c := range n.Body.List {
					for _, e := range c.(*ast.CaseClause).List {
						if s, ok := StringLit(e); ok && ids[s] {
							report(e.Pos(), "", s)
						}
					}
				}
			}
		})
	})

var providerNameRe = regexp.MustCompile(`(?i)provider`)

var agentEnv = map[string]bool{"HOME": true, "PIG_HOME": true, "PI_HOME": true, "PIG_CODING_AGENT_DIR": true, "PI_CODING_AGENT_DIR": true}

// TestIsolation flags tests that touch the user's real agent state: a package without a scoped TestMain whose tests read or set the home and agent-directory variables unsafely.
var TestIsolation = newToolingCheck("testisolation",
	"test reads or sets HOME/PIG_HOME/PI_HOME/*_CODING_AGENT_DIR with os.Setenv or os.UserHomeDir, or sets only some of them before starting a child process, in a package whose TestMain does not call testenv.ScopeTempDir",
	High, "2026-10-06 lane auth.json wipe; #163 agent-dir guard",
	func(p *Pass) {
		scoped := false
		for _, f := range p.Files {
			if !strings.HasSuffix(p.Fset.Position(f.Pos()).Filename, "_test.go") {
				continue
			}
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "TestMain" && fd.Body != nil {
					ast.Inspect(fd.Body, func(n ast.Node) bool {
						if call, ok := n.(*ast.CallExpr); ok {
							if fn := p.Callee(call); fn != nil && (fn.Name() == "ScopeTempDir" || fn.Name() == "RunScoped" || fn.Name() == "IsolateAgentEnv") {
								scoped = true
							}
						}
						return true
					})
				}
			}
		}
		if scoped || strings.HasSuffix(p.Pkg.Path(), "/internal/testenv") {
			return
		}
		p.Funcs(func(name string, _ *ast.FuncType, body *ast.BlockStmt) {
			if !p.IsTest(body.Pos()) {
				return
			}
			set := map[string]bool{}
			spawns := false
			var first *ast.CallExpr
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch {
				case p.IsFunc(call, "os/exec", "Command"), p.IsFunc(call, "os/exec", "CommandContext"):
					spawns = true
				case p.IsFunc(call, "os", "UserHomeDir"):
					p.Reportf(call.Pos(), "os.UserHomeDir reads the real home directory in a test that does not isolate it")
				case p.IsFunc(call, "os", "Setenv") && len(call.Args) > 0:
					if v, ok := StringLit(call.Args[0]); ok && agentEnv[v] {
						p.Reportf(call.Pos(), "os.Setenv(%q) leaks past the test; use t.Setenv", v)
					}
				case p.IsMethod(call, "testing", "T", "Setenv") && len(call.Args) > 0:
					if v, ok := StringLit(call.Args[0]); ok && agentEnv[v] {
						set[v] = true
						if first == nil {
							first = call
						}
					}
				}
				return true
			})
			if first != nil && spawns && len(set) < len(agentEnv) {
				p.Reportf(first.Pos(), "test sets %d of the 5 home and agent-directory variables; the child process inherits the rest from the shell", len(set))
			}
		})
	})
