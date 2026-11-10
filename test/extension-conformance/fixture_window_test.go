package extensionconformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A harness's first use of an SDK fixture compiles it (a cold Cargo target takes 20 to 60 s on a CI runner, on Windows 30 s and more). A bound that
// starts before that build spends its time on the compiler: TestModelEventPayloadsMatchAcrossSDKs/subprocess-rust started a 30 s context, built the
// fixture in test.make, and emitted its events with the context already expired. Runner.Emit does not return an error for that: recordHandlerError drops
// a handler error that is the dispatch context's own cancellation, so Emit reported success, the events were never delivered, and the test waited out
// its whole 120 s budget for them.
//
// So a context with a deadline starts after the harness is built, never before it in the same block. This guard is a narrow tripwire: it sees only
// that shape, a context assigned in a block and a make or build call in a later statement of the same block. A context that reaches a test any other way
// (a helper, a parent block, a field) is not seen; TestModelEventPayloadsMatchAcrossSDKs fails at once if its emit context ended, whatever the cause.
func TestDeadlineContextsStartAfterTheHarnessBuildsItsFixtures(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var offenders []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, entry.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			var started token.Pos
			for _, stmt := range block.List {
				if started != token.NoPos {
					if call := fixtureBuildingCall(stmt); call != nil {
						offenders = append(offenders, fset.Position(started).String()+" starts a deadline before "+fset.Position(call.Pos()).String())
						started = token.NoPos
					}
				}
				if assign, ok := stmt.(*ast.AssignStmt); ok && started == token.NoPos && len(assign.Rhs) == 1 && isDeadlineContextCall(assign.Rhs[0]) {
					started = assign.Pos()
				}
			}
			return true
		})
	}
	for _, offender := range offenders {
		t.Error(offender)
	}
}

// isDeadlineContextCall reports whether expr is context.WithTimeout(...) or context.WithDeadline(...).
func isDeadlineContextCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context" && (sel.Sel.Name == "WithTimeout" || sel.Sel.Name == "WithDeadline")
}

// fixtureBuildingCall returns the first call in stmt that builds a harness or an SDK fixture: a harnessCase's make, a make*Harness function, or a
// build*SDKFixture function.
func fixtureBuildingCall(stmt ast.Stmt) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(stmt, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		// A function literal is not run where it is written.
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fun.Sel.Name == "make" {
				found = call
			}
		case *ast.Ident:
			if (strings.HasPrefix(fun.Name, "make") && strings.HasSuffix(fun.Name, "Harness")) || (strings.HasPrefix(fun.Name, "build") && strings.HasSuffix(fun.Name, "SDKFixture")) {
				found = call
			}
		}
		return found == nil
	})
	return found
}
