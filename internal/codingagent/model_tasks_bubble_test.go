package codingagent

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

// A ModelRegistry owns a sync.WaitGroup for the model work it runs, and a WaitGroup belongs to one side of a synctest bubble edge: an Add made inside a
// bubble while a task started outside is still running, or the reverse, ends the process ("WaitGroup.Add called from inside and outside synctest
// bubble"). Production has no bubbles, so the owner stays a plain WaitGroup. A test that starts model work on a registry before it enters a bubble
// (RefreshCatalogs, YieldToRegistrationRefresh) settles the registry first: the work signals its result before its goroutine returns, so a
// result that reached the caller does not mean the owner is idle. Settle before the bubble only: Go's WaitGroup.Wait called outside a bubble on a
// WaitGroup that a bubble used dereferences the current goroutine's missing bubble (runtime synctest_isAssociated), so a registry a bubble used is
// closed inside it.

// settleModelTasks waits until the registry's task owner and any backing ai.Models collections hold no work, without closing or draining them. Call it
// after a test starts model work outside a bubble and before the registry enters one.
func settleModelTasks(registry *ModelRegistry) {
	registry.modelTasks.work.Wait()
	registry.remoteMu.Lock()
	remote := registry.remoteCollection
	registry.remoteMu.Unlock()
	if remote != nil {
		remote.Close()
	}
	registry.nativeMu.Lock()
	native := registry.nativeModels
	registry.nativeMu.Unlock()
	if native != nil {
		native.Close()
	}
}

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

// TestSettleModelTasksJoinsNativeAndRemoteModels verifies that settleModelTasks joins owned work on both registry.nativeModels and
// registry.remoteCollection (in addition to registry.modelTasks.work) before the registry enters a synctest bubble. Without joining
// those collections, an outside owned task whose goroutine has not yet called owned.Done() when the bubble adds to the same collection
// ends the process with "WaitGroup.Add called from inside and outside synctest bubble".
func TestSettleModelTasksJoinsNativeAndRemoteModels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote bool
	}{
		{"nativeModels", false},
		{"remoteCollection", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewModelRegistry(t.TempDir())
			models := ai.CreateModels()
			entered, release := make(chan struct{}), make(chan struct{})
			var finished atomic.Bool
			var calls atomic.Int32
			models.SetProvider(&ai.ModelsProvider{
				ID:   "bubble-owned",
				Name: "bubble-owned",
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
			if tc.remote {
				registry.remoteMu.Lock()
				registry.remoteCollection = models
				registry.remoteMu.Unlock()
			} else {
				registry.nativeMu.Lock()
				registry.nativeModels = models
				registry.nativeMu.Unlock()
			}
			authDone := make(chan error, 1)
			go func() {
				_, err := models.GetAuth(t.Context(), "bubble-owned")
				authDone <- err
			}()
			<-entered
			close(release)
			if err := <-authDone; err != nil {
				t.Fatalf("outside GetAuth = %v", err)
			}
			settleModelTasks(registry)
			if !finished.Load() {
				t.Fatal("settleModelTasks returned before the outside collection operation ended")
			}
			synctest.Test(t, func(t *testing.T) {
				defer settleModelTasks(registry)
				result := models.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"bubble-owned"}})
				if result.Aborted || len(result.Errors) != 0 {
					t.Fatalf("inside Refresh = %+v", result)
				}
			})
		})
	}
}

// modelTaskStarters are the names tests use to run work on a registry's task owner that can return before the owner is idle: registry methods and the plain
// function RefreshModelCatalogs.
var modelTaskStarters = map[string]bool{
	"RefreshCatalogs": true, "RefreshModelCatalogs": true, "StartModelTask": true, "AwaitModelTasks": true,
	"YieldToRegistrationRefresh": true, "StartRegistrationRefresh": true, "GetProviderAuthChecks": true,
	"GetAvailableModelDataContext": true, "GetAvailableAllModelDataContext": true,
}

// unsettledModelWork keeps the fixture rule from being forgotten by copy: in one function body, model work started before a synctest.Test call in the same
// scope needs settleModelTasks between the two. It returns one finding per start. Work started inside the bubble's own function needs nothing.
//
// A start is any reference to a name in modelTaskStarters: a call through a receiver (registry.RefreshCatalogs(...)), a call of the plain function
// (RefreshModelCatalogs(...)), the same through a package qualifier (codingagent.RefreshModelCatalogs), and a method value that is not called at once
// (refresh := registry.RefreshCatalogs, or the method passed as an argument), because the later call cannot be placed. A call of a helper declared in a
// test file counts as a start when the helper's own body starts work that no settleModelTasks follows, transitively.
//
// Blind spots: the check is by name and by position in one function body, not by types or flow. It does not see a start (1) in a function literal that is a
// scope of its own, such as a goroutine or a t.Run closure, when the bubble is in the enclosing scope; (2) in a helper declared in a non-test file, or
// reached through an interface or a stored func value; (3) behind a variable that carries the method value out of the scope; (4) that is a differently
// named wrapper outside the list above (add the name there). It reads positions, so a start in an untaken branch or after an early return still counts.
func unsettledModelWork(fset *token.FileSet, files []*ast.File) []string {
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
	// scan reads one scope's own statements: a nested function literal is a scope of its own. helpers names the test helpers that start work.
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
				} else if modelTaskStarters[n.Sel.Name] || helpers[n.Sel.Name] {
					f.starts = append(f.starts, start{n.Sel.Pos(), n.Sel.Name})
				}
			case *ast.Ident:
				if qualified[n] {
					break
				}
				if n.Name == "settleModelTasks" {
					f.settle = append(f.settle, n.Pos())
				} else if modelTaskStarters[n.Name] || helpers[n.Name] {
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
				findings = append(findings, fmt.Sprintf("%s: model work (%s) started before the synctest bubble at %s is not settled (call settleModelTasks(registry) between them)", fset.Position(s.pos), s.name, fset.Position(f.bubbles[bubble])))
			}
		}
	}
	return findings
}

func TestTestsSettleModelTasksBeforeEnteringABubble(t *testing.T) {
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
	for _, finding := range unsettledModelWork(fset, files) {
		t.Error(finding)
	}
}

// The guard rejects each shape that starts model work before a bubble, accepts the settled and the in-bubble forms, and its documented blind spots stay
// visible here: a case named "blind spot" is accepted today and would need the guard extended, not the test deleted.
func TestUnsettledModelWorkGuardShapes(t *testing.T) {
	const bubble = "synctest.Test(t, func(t *testing.T) {})"
	for _, tc := range []struct {
		name, body, helper string
		want               int
	}{
		{"receiver call", "registry.RefreshCatalogs(ctx, opts)\n" + bubble, "", 1},
		{"plain function call", "RefreshModelCatalogs(ctx, registry)\n" + bubble, "", 1},
		{"package qualifier", "codingagent.RefreshModelCatalogs(ctx, registry)\n" + bubble, "", 1},
		{"method value then call", "refresh := registry.RefreshCatalogs\nrefresh(ctx, opts)\n" + bubble, "", 1},
		{"method value as argument", "run(registry.YieldToRegistrationRefresh)\n" + bubble, "", 1},
		{"helper that starts work", "seed(t, registry)\n" + bubble, "func seed(t *testing.T, registry *ModelRegistry) { registry.RefreshCatalogs(ctx, opts) }", 1},
		{"helper that starts work through a helper", "seedAll(t, registry)\n" + bubble, "func seedAll(t *testing.T, r *ModelRegistry) { seed(t, r) }\nfunc seed(t *testing.T, r *ModelRegistry) { RefreshModelCatalogs(ctx, r) }", 1},
		{"settled receiver call", "registry.RefreshCatalogs(ctx, opts)\nsettleModelTasks(registry)\n" + bubble, "", 0},
		{"settled plain function call", "RefreshModelCatalogs(ctx, registry)\nsettleModelTasks(registry)\n" + bubble, "", 0},
		{"helper that settles", "seed(t, registry)\n" + bubble, "func seed(t *testing.T, r *ModelRegistry) { r.RefreshCatalogs(ctx, opts); settleModelTasks(r) }", 0},
		{"start after the bubble", bubble + "\nregistry.RefreshCatalogs(ctx, opts)", "", 0},
		{"start inside the bubble", "synctest.Test(t, func(t *testing.T) { RefreshModelCatalogs(ctx, registry) })", "", 0},
		{"blind spot: start in a goroutine literal", "go func() { RefreshModelCatalogs(ctx, registry) }()\n" + bubble, "", 0},
		{"blind spot: start through a stored func value", "refresh := callbacks.refresh\nrefresh()\n" + bubble, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package codingagent\nfunc TestX(t *testing.T) {\n" + tc.body + "\n}\n" + tc.helper + "\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "case_test.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := unsettledModelWork(fset, []*ast.File{file}); len(got) != tc.want {
				t.Errorf("findings = %q, want %d", got, tc.want)
			}
		})
	}
}
