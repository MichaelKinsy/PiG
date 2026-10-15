//go:build !race

// The race detector is off for these type-checking tests: packages.Load type-checks the module's packages concurrently, and go/types (Go
// 1.27.2) races with itself there (Named.unpack against Checker.isComplete), which fails the tests intermittently under -race.
// The tests share no state with the code they check, so -race finds nothing in them.

package sessionentry_test

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const sessionentryPath = "github.com/MichaelKinsy/PiG/internal/sessionentry"

// funnelMembers are the members whose assertion tells what a session entry is; only the funnel may name them on a SessionEntry, because a
// LazyEntry is a message entry that has not been read.
var funnelMembers = map[string]bool{"MessageEntry": true, "RawEntry": true, "LazyEntry": true}

// memberAssertions reports, for each type assertion or type switch in files whose operand is a sessionentry.SessionEntry or FileEntry (or
// an alias of one) and that names a funnel member, its position.
func memberAssertions(fset *token.FileSet, info *types.Info, files []*ast.File) []string {
	isUnion := func(expr ast.Expr) bool {
		t := info.TypeOf(expr)
		if t == nil {
			return false
		}
		named, ok := types.Unalias(t).(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != sessionentryPath {
			return false
		}
		return named.Obj().Name() == "SessionEntry" || named.Obj().Name() == "FileEntry"
	}
	names := func(expr ast.Expr) bool {
		if expr == nil {
			return false
		}
		if star, ok := expr.(*ast.StarExpr); ok {
			expr = star.X
		}
		named, ok := types.Unalias(info.TypeOf(expr)).(*types.Named)
		return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == sessionentryPath && funnelMembers[named.Obj().Name()]
	}
	var found []string
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeAssertExpr:
				if n.Type != nil && isUnion(n.X) && names(n.Type) {
					found = append(found, fset.Position(n.Pos()).String())
				}
			case *ast.TypeSwitchStmt:
				var operand ast.Expr
				switch assign := n.Assign.(type) {
				case *ast.ExprStmt:
					operand = assign.X.(*ast.TypeAssertExpr).X
				case *ast.AssignStmt:
					operand = assign.Rhs[0].(*ast.TypeAssertExpr).X
				}
				if !isUnion(operand) {
					return true
				}
				for _, clause := range n.Body.List {
					for _, expr := range clause.(*ast.CaseClause).List {
						if names(expr) {
							found = append(found, fset.Position(expr.Pos()).String())
						}
					}
				}
			}
			return true
		})
	}
	return found
}

// Code learns what a session entry is only through the funnel in lazy.go (AsMessage, Materialize, IsUndescribed, ReadRaw): an assertion of
// MessageEntry, RawEntry or LazyEntry on a SessionEntry anywhere else would take a LazyEntry, which is a message entry, for something else.
// The test type-checks every package of the module and fails on any such assertion or type switch outside lazy.go.
func TestOnlyTheFunnelAssertsMessageMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the module")
	}
	pkgs := modulePackages(t)
	checked := 0
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 || pkg.TypesInfo == nil {
			continue
		}
		checked++
		for _, at := range memberAssertions(pkg.Fset, pkg.TypesInfo, pkg.Syntax) {
			if filepath.Base(strings.SplitN(at, ":", 2)[0]) == "lazy.go" && pkg.PkgPath == sessionentryPath {
				continue
			}
			t.Errorf("%s: asserts a message member on a session entry outside the funnel (sessionentry.AsMessage, Materialize, IsUndescribed)", at)
		}
	}
	if checked < 100 {
		t.Fatalf("type-checked only %d packages", checked)
	}
}

var (
	moduleOnce sync.Once
	moduleRoot string
	modulePkgs []*packages.Package
	moduleErr  error
)

// modulePackages is every package of the module, type-checked once for the tests of this file.
func modulePackages(t *testing.T) []*packages.Package {
	t.Helper()
	moduleRoot = testenv.ModuleRoot(t)
	moduleOnce.Do(func() {
		cfg := &packages.Config{Dir: moduleRoot, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}
		modulePkgs, moduleErr = packages.Load(cfg, "./...")
	})
	if moduleErr != nil {
		t.Fatal(moduleErr)
	}
	return modulePkgs
}

const codingagentPath = "github.com/MichaelKinsy/PiG/internal/codingagent"

// lazyRoutes are the names by which a LazyEntry leaves a Session, and lazyRouteFiles the files allowed to use them: each passes the entries
// it gets only to the error-returning funnel (ReadRaws, MaterializeAll) or to Base, so no LazyEntry reaches asMessage or Raw, which panic on a
// record that changed on disk.
var (
	lazyRoutes     = map[string]bool{"HandleReads": true, "entryHandles": true, "branchHandles": true, "treeHandles": true, "entryHandlesLocked": true}
	lazyRouteFiles = map[string]bool{
		"internal/codingagent/session_lazy.go":           true,
		"internal/codingagent/session.go":                true,
		"internal/codingagent/extension_session_read.go": true,
		"coding/cli/rpc_mode.go":                         true,
		"coding/session.go":                              true,
	}
)

// A LazyEntry leaves a Session only by HandleReads and the unexported handle reads, and only the reviewed files use them: a new use is a new
// route by which a handle could reach code that panics on a changed record, and has to be reviewed and listed here.
func TestLazyEntriesLeaveOnlyByTheReviewedRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the module")
	}
	pkgs := modulePackages(t)
	found := 0
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for ident, obj := range pkg.TypesInfo.Uses {
			if obj.Pkg() == nil || obj.Pkg().Path() != codingagentPath || !lazyRoutes[obj.Name()] {
				continue
			}
			found++
			pos := pkg.Fset.Position(ident.Pos())
			rel, err := filepath.Rel(moduleRoot, pos.Filename)
			if err != nil || !lazyRouteFiles[filepath.ToSlash(rel)] {
				t.Errorf("%s: uses %s, a route by which LazyEntry values leave the Session, outside the reviewed files", pos, obj.Name())
			}
		}
	}
	if found == 0 {
		t.Fatal("found no use of the handle reads")
	}
}

// The check sees an assertion and a type switch on a SessionEntry and on an alias of it, and passes the funnel.
func TestTheFunnelCheckFindsAssertions(t *testing.T) {
	const source = `package probe

import "github.com/MichaelKinsy/PiG/internal/sessionentry"

type Entry = sessionentry.SessionEntry

func a(e sessionentry.SessionEntry) bool { _, ok := e.(sessionentry.MessageEntry); return ok }
func b(e Entry) bool { _, ok := e.(*sessionentry.LazyEntry); return ok }
func c(e sessionentry.FileEntry) int {
	switch e.(type) {
	case sessionentry.RawEntry:
		return 1
	case sessionentry.CompactionEntry:
		return 2
	}
	return 0
}
func d(e sessionentry.SessionEntry) bool { _, ok := e.(sessionentry.CompactionEntry); return ok }
func f(e sessionentry.SessionEntry) bool { _, ok, _ := sessionentry.AsMessage(e); return ok }
func g(v any) bool { _, ok := v.(sessionentry.MessageEntry); return ok }
`
	root := testenv.ModuleRoot(t)
	cfg := &packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Overlay: map[string][]byte{filepath.Join(root, "internal", "sessionentry", "probe_overlay", "probe.go"): []byte(source)}}
	pkgs, err := packages.Load(cfg, "./internal/sessionentry/probe_overlay")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || len(pkgs[0].Errors) > 0 {
		t.Fatalf("probe: %v", pkgs[0].Errors)
	}
	found := memberAssertions(pkgs[0].Fset, pkgs[0].TypesInfo, pkgs[0].Syntax)
	var lines []string
	for _, at := range found {
		parts := strings.Split(at, ":")
		lines = append(lines, parts[len(parts)-2])
	}
	if got := strings.Join(lines, ","); got != "7,8,11" {
		t.Fatalf("found assertions on lines %s, want 7,8,11", got)
	}
}
