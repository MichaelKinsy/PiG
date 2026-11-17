package codingagent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// A ModelRegistry owns a sync.WaitGroup for the model work it runs, and a WaitGroup belongs to one side of a synctest bubble edge: an Add made inside a
// bubble while a task started outside is still running, or the reverse, ends the process ("WaitGroup.Add called from inside and outside synctest
// bubble"). Production has no bubbles, so the owner stays a plain WaitGroup. A test that starts model work on a registry before it enters a bubble
// (RefreshCatalogs, YieldToRegistrationRefresh) settles the registry first: the work signals its result before its goroutine returns, so a
// result that reached the caller does not mean the owner is idle. Settle before the bubble only: Go's WaitGroup.Wait called outside a bubble on a
// WaitGroup that a bubble used dereferences the current goroutine's missing bubble (runtime synctest_isAssociated), so a registry a bubble used is
// closed inside it.

// settleModelTasks waits until the registry's task owner holds no work, without closing it. Call it after a test starts model work outside a bubble
// and before the registry enters one.
func settleModelTasks(registry *ModelRegistry) { registry.modelTasks.work.Wait() }

func TestSettledRegistryMayEnterASynctestBubble(t *testing.T) {
	registry := &ModelRegistry{}
	entered, release := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	if !registry.StartModelTask(t.Context(), func(context.Context) { close(entered); <-release; finished.Store(true) }) {
		t.Fatal("open owner refused work")
	}
	<-entered
	close(release)
	settleModelTasks(registry)
	if !finished.Load() {
		t.Fatal("settle returned before the outside task ended")
	}
	synctest.Test(t, func(t *testing.T) {
		ran := make(chan struct{})
		if !registry.StartModelTask(t.Context(), func(context.Context) { close(ran) }) {
			t.Fatal("open owner refused work inside the bubble")
		}
		<-ran
		registry.CloseModelTasks()
	})
}

// modelTaskStarters are the registry calls tests make that run work on its task owner and can return before the owner is idle.
var modelTaskStarters = map[string]bool{
	"RefreshCatalogs": true, "RefreshModelCatalogs": true, "StartModelTask": true, "AwaitModelTasks": true,
	"YieldToRegistrationRefresh": true, "StartRegistrationRefresh": true, "GetProviderAuthChecks": true,
	"GetAvailableModelDataContext": true, "GetAvailableAllModelDataContext": true,
}

// TestTestsSettleModelTasksBeforeEnteringABubble keeps the fixture rule from being forgotten by copy: in one function body, a call that starts model work
// and is followed by a synctest.Test call in the same scope needs settleModelTasks between them. Work started inside the bubble's own function needs nothing.
func TestTestsSettleModelTasksBeforeEnteringABubble(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "synctest.Test(") {
			continue
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		var scopes []ast.Node
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				scopes = append(scopes, fn.Body)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok {
				scopes = append(scopes, lit.Body)
			}
			return true
		})
		for _, scope := range scopes {
			var starters, bubbles, settles []token.Pos
			// The scope's own statements: a nested function literal is a scope of its own.
			ast.Inspect(scope, func(n ast.Node) bool {
				if _, nested := n.(*ast.FuncLit); nested && n != scope {
					return false
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fun.Name == "settleModelTasks" {
						settles = append(settles, call.Pos())
					}
				case *ast.SelectorExpr:
					if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "synctest" && fun.Sel.Name == "Test" {
						bubbles = append(bubbles, call.Pos())
					} else if modelTaskStarters[fun.Sel.Name] {
						starters = append(starters, call.Pos())
					}
				}
				return true
			})
			for _, starter := range starters {
				for _, bubble := range bubbles {
					if bubble < starter {
						continue
					}
					settled := false
					for _, settle := range settles {
						settled = settled || (settle > starter && settle < bubble)
					}
					if !settled {
						t.Errorf("%s: model work started before the synctest bubble at %s is not settled (call settleModelTasks(registry) between them)", fset.Position(starter), fset.Position(bubble))
					}
					break
				}
			}
		}
	}
}
