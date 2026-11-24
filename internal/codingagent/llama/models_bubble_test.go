package llama

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// ai.Models owns sync.WaitGroups (owned and operations) for the work it runs, and a WaitGroup belongs to one side of a synctest bubble edge: an Add made
// inside a bubble while a task started outside has not yet called Done, or the reverse, ends the process ("WaitGroup.Add called from inside and outside
// synctest bubble"). Production has no bubbles, so Models keeps plain WaitGroups. A test that starts Models work outside a bubble (for example
// commandFixture calling configuredClient -> GetProviderAuth -> Models.GetAuth) settles Models before entering synctest.Test: awaitModelsOperation delivers
// its result on a channel before its goroutine returns and runs defer owned.Done(). Settle before the bubble only: Go's WaitGroup.Wait called outside a
// bubble on a WaitGroup that a bubble used dereferences the current goroutine's missing bubble (runtime synctest_isAssociated).

// settleModels waits until every operation started on models has finished (joining both operations and owned, since goOwned runs defer owned.Done() inside
// operations.Go), without setting draining. Call it after a test starts Models work outside a bubble and before models enters one.
func settleModels(models *ai.Models) {
	if models != nil {
		models.Close()
	}
}

// settleCommandSession settles the ai.Models backing session.registry when it is a testRegistry.
func settleCommandSession(session *commandSession) {
	if session == nil {
		return
	}
	if reg, ok := session.registry.(testRegistry); ok {
		settleModels(reg.Models)
	}
}

func TestSettledModelsMayEnterASynctestBubble(t *testing.T) {
	models := ai.CreateModels()
	entered, release := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	var calls atomic.Int32
	models.SetProvider(&ai.ModelsProvider{
		ID:   "bubble-test",
		Name: "bubble-test",
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{
			Name: "test",
			Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
					finished.Store(true)
				}
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "k"}}, nil
			},
		}},
		RefreshModels: func(ai.RefreshModelsContext) error { return nil },
	})
	authDone := make(chan error, 1)
	go func() {
		_, err := models.GetAuth(t.Context(), "bubble-test")
		authDone <- err
	}()
	<-entered
	close(release)
	if err := <-authDone; err != nil {
		t.Fatalf("outside GetAuth = %v", err)
	}
	settleModels(models)
	if !finished.Load() {
		t.Fatal("settle returned before the outside Models operation ended")
	}
	synctest.Test(t, func(t *testing.T) {
		result := models.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"bubble-test"}})
		if result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("inside Refresh = %+v", result)
		}
		models.Close()
	})
}

// modelsWorkStarters are the names llama tests use to start work on an ai.Models collection that can return before its owned/operations WaitGroups are idle.
var modelsWorkStarters = map[string]bool{
	"GetAuth": true, "GetModelAuth": true, "CheckAuth": true, "CheckAuthWith": true,
	"GetAvailable": true, "GetAvailableOfType": true, "GetAllAvailable": true,
	"GetAvailableWith": true, "GetAvailableOfTypeWith": true, "GetAllAvailableWith": true,
	"Refresh": true, "GetProviderAuth": true, "configuredClient": true, "syncCatalog": true,
}

// unsettledModelsWork keeps the fixture rule from being forgotten by copy: in one function body, Models work started before a synctest.Test call in the
// same scope needs settleModels or settleCommandSession between the two. It returns one finding per start. Work started inside the bubble's own function
// needs nothing.
//
// Blind spots match internal/codingagent/model_tasks_bubble_test.go: position-based within one function body, not type- or flow-sensitive.
func unsettledModelsWork(fset *token.FileSet, files []*ast.File) []string {
	type scope struct {
		name string
		body *ast.BlockStmt
	}
	var scopes []scope
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				scopes = append(scopes, scope{fn.Name.Name, fn.Body})
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok {
				scopes = append(scopes, scope{"", lit.Body})
			}
			return true
		})
	}
	type start struct {
		pos  token.Pos
		name string
	}
	type facts struct {
		starts          []start
		bubbles, settle []token.Pos
	}
	scan := func(body *ast.BlockStmt, helpers map[string]bool) (f facts) {
		qualified := map[*ast.Ident]bool{}
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.SelectorExpr:
				qualified[n.Sel] = true
				if pkg, ok := n.X.(*ast.Ident); ok && pkg.Name == "synctest" && n.Sel.Name == "Test" {
					f.bubbles = append(f.bubbles, n.Pos())
				} else if modelsWorkStarters[n.Sel.Name] || helpers[n.Sel.Name] {
					f.starts = append(f.starts, start{n.Sel.Pos(), n.Sel.Name})
				}
			case *ast.Ident:
				if qualified[n] {
					break
				}
				if n.Name == "settleModels" || n.Name == "settleCommandSession" {
					f.settle = append(f.settle, n.Pos())
				} else if modelsWorkStarters[n.Name] || helpers[n.Name] {
					f.starts = append(f.starts, start{n.Pos(), n.Name})
				}
			}
			return true
		})
		return f
	}
	unsettled := func(f facts) (out []start) {
		for _, s := range f.starts {
			if !slices.ContainsFunc(f.settle, func(p token.Pos) bool { return p > s.pos }) {
				out = append(out, s)
			}
		}
		return out
	}
	helpers := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, sc := range scopes {
			if sc.name != "" && !helpers[sc.name] && len(unsettled(scan(sc.body, helpers))) > 0 {
				helpers[sc.name] = true
				changed = true
			}
		}
	}
	var findings []string
	for _, sc := range scopes {
		f := scan(sc.body, helpers)
		for _, s := range f.starts {
			bubble := slices.IndexFunc(f.bubbles, func(p token.Pos) bool { return p > s.pos })
			if bubble < 0 {
				continue
			}
			if !slices.ContainsFunc(f.settle, func(p token.Pos) bool { return p > s.pos && p < f.bubbles[bubble] }) {
				findings = append(findings, fmt.Sprintf("%s: Models work (%s) started before the synctest bubble at %s is not settled (call settleModels or settleCommandSession between them)", fset.Position(s.pos), s.name, fset.Position(f.bubbles[bubble])))
			}
		}
	}
	return findings
}

func TestTestsSettleModelsBeforeEnteringABubble(t *testing.T) {
	names, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	for _, finding := range unsettledModelsWork(fset, files) {
		t.Error(finding)
	}
}

func TestUnsettledModelsWorkGuardShapes(t *testing.T) {
	const bubble = "synctest.Test(t, func(t *testing.T) {})"
	for _, tc := range []struct {
		name, body, helper string
		want               int
	}{
		{"receiver call", "models.GetAuth(ctx, id)\n" + bubble, "", 1},
		{"helper that starts work", "session, _ := commandFixture(t, url)\n" + bubble, "func commandFixture(t *testing.T, url string) (*commandSession, *[]notification) { configuredClient(ctx, reg); return nil, nil }", 1},
		{"settled helper call", "session, _ := commandFixture(t, url)\nsettleCommandSession(session)\n" + bubble, "func commandFixture(t *testing.T, url string) (*commandSession, *[]notification) { configuredClient(ctx, reg); return nil, nil }", 0},
		{"settled receiver call", "models.GetAuth(ctx, id)\nsettleModels(models)\n" + bubble, "", 0},
		{"start inside the bubble", "synctest.Test(t, func(t *testing.T) { session.syncCatalog(nil, false) })", "", 0},
		{"start after the bubble", bubble + "\nmodels.Refresh(ctx)", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package llama\nfunc TestX(t *testing.T) {\n" + tc.body + "\n}\n" + tc.helper + "\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "case_test.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := unsettledModelsWork(fset, []*ast.File{file}); len(got) != tc.want {
				t.Errorf("findings = %q, want %d", got, tc.want)
			}
		})
	}
}
