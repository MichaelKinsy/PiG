package main

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// literalDeadlineAllowed lists, by file and enclosing function, the test sites where a literal duration is the behavior under test or the bound on a build, not the bound on a hang.
var literalDeadlineAllowed = map[string]string{
	"build_binary_test.go:compilePigBinary":                                                           "bounds one go build of the shared test binary",
	"session_extension_actions_exec_test.go:buildGoSDKFixture":                                        "bounds one go build of the exec fixture",
	"auth_embedded_packed_test.go:buildRustPackedAuthFixture":                                         "bounds a packed extension build",
	"interactive_signal_lifetime_test.go:TestInteractiveSignalsRetainHandlersThroughDisposalAndDrain": "negative window: the process must stay alive for 1.5s after the second signal",
	"package_capture_test.go:BenchmarkPackageCapture":                                                 "a benchmark has no *testing.T for testbudget.Wait; the child exits at once",
}

// hangBoundArgument maps a call that bounds a wait to the index of its duration argument. A package-qualified key is `pkg.Func`; an unqualified key matches a method or package-local function of that name.
var hangBoundArgument = map[string]int{
	"time.After":          0,
	"time.NewTimer":       0,
	"context.WithTimeout": 1,
	"waitQuiet":           3,
	"runWithTimeout":      1,
}

// A deadline that bounds an event the test controls must come from testbudget, which lets the event decide when the wait ends and caps a hang below the package timeout. A literal one-second-or-longer deadline expires under load: the failure "RPC dump notification timed out after 1.4s" and the 30s Bedrock/Azure waits were fixed wall-clock bounds on events that were still progressing.
func TestTestWaitsUseTheTestBudgetNotLiteralDeadlines(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	used := map[string]bool{}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			sites := literalHangBounds(fn.Body)
			if _, ok := literalDeadlineAllowed[file+":"+fn.Name.Name]; ok {
				used[file+":"+fn.Name.Name] = len(sites) > 0
				continue
			}
			for _, site := range sites {
				t.Errorf("%s: %s uses a literal deadline of one second or more; use testbudget.Wait(t) or testbudget.Context(t)", fset.Position(site.pos), site.what)
			}
		}
	}
	for site := range literalDeadlineAllowed {
		if !used[site] {
			t.Errorf("literalDeadlineAllowed[%q] allows no literal deadline; delete the entry", site)
		}
	}
}

func TestLiteralHangBoundsDetectsEveryForm(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{"time.After(2 * time.Second)", 1},
		{"time.After(time.Second * 2)", 1},
		{"time.After(time.Minute)", 1},
		{"time.After(1500 * time.Millisecond)", 1},
		{"time.After((3) * time.Second)", 1},
		{"time.After(time.Duration(4) * time.Second)", 1},
		{"time.NewTimer(5 * time.Second)", 1},
		{"context.WithTimeout(ctx, 10*time.Second)", 1},
		{"output.waitQuiet(0, needle, 100*time.Millisecond, 10*time.Second)", 1},
		{"runWithTimeout(cmd, time.Minute)", 1},
		{"deadline := time.Now().Add(90 * time.Second)", 1},
		{"p := &rpcProcess{budget: time.Second}", 1},
		{"time.After(999 * time.Millisecond)", 0},
		{"time.After(testbudget.Wait(t))", 0},
		{"output.waitQuiet(0, needle, 2*time.Second, testbudget.Wait(t))", 0},
		{"expires := time.Now().Add(time.Hour).UnixMilli()", 0},
		{"old := time.Now().Add(-time.Minute)", 0},
		{"time.Sleep(2 * time.Second)", 0},
	} {
		body, err := parser.ParseExpr("func() {" + tc.src + "}")
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got := len(literalHangBounds(body.(*ast.FuncLit).Body)); got != tc.want {
			t.Errorf("%s: %d sites, want %d", tc.src, got, tc.want)
		}
	}
}

type hangBoundSite struct {
	pos  token.Pos
	what string
}

// literalHangBounds reports each literal duration of one second or more that bounds a wait in body: a hang-bound call argument, a `budget:` field, or a `time.Now().Add` deadline. A `time.Now().Add(...)` whose result is converted with Unix or UnixMilli is a timestamp, not a deadline.
func literalHangBounds(body ast.Node) []hangBoundSite {
	timestamps := map[ast.Node]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Unix" || selector.Sel.Name == "UnixMilli") {
			timestamps[ast.Unparen(selector.X)] = true
		}
		return true
	})
	var sites []hangBoundSite
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && key.Name == "budget" && literalSecondOrLonger(node.Value) {
				sites = append(sites, hangBoundSite{node.Pos(), "budget"})
			}
		case *ast.CallExpr:
			name := callName(node)
			if name == "Add" && isTimeNow(node.Fun) && !timestamps[node] && len(node.Args) == 1 && literalSecondOrLonger(node.Args[0]) {
				sites = append(sites, hangBoundSite{node.Pos(), "time.Now().Add"})
			}
			if index, ok := hangBoundArgument[name]; ok && index < len(node.Args) && literalSecondOrLonger(node.Args[index]) {
				sites = append(sites, hangBoundSite{node.Pos(), name})
			}
		}
		return true
	})
	return sites
}

// callName returns `pkg.Func` for a package-qualified call to time or context and the bare function or method name otherwise.
func callName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok && (pkg.Name == "time" || pkg.Name == "context") {
			return pkg.Name + "." + fun.Sel.Name
		}
		return fun.Sel.Name
	}
	return ""
}

func isTimeNow(fun ast.Expr) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	now, ok := selector.X.(*ast.CallExpr)
	return ok && callName(now) == "time.Now"
}

// literalSecondOrLonger reports whether expr is a constant duration built from number literals and time units whose value is at least one second.
func literalSecondOrLonger(expr ast.Expr) bool {
	value, ok := constantDuration(expr)
	return ok && value >= time.Second
}

func constantDuration(expr ast.Expr) (time.Duration, bool) {
	units := map[string]time.Duration{"Nanosecond": time.Nanosecond, "Microsecond": time.Microsecond, "Millisecond": time.Millisecond, "Second": time.Second, "Minute": time.Minute, "Hour": time.Hour}
	switch node := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		if node.Kind != token.INT && node.Kind != token.FLOAT {
			return 0, false
		}
		value, ok := constant.Float64Val(constant.ToFloat(constant.MakeFromLiteral(node.Value, node.Kind, 0)))
		return time.Duration(value), ok
	case *ast.SelectorExpr:
		pkg, ok := node.X.(*ast.Ident)
		if !ok || pkg.Name != "time" {
			return 0, false
		}
		unit, ok := units[node.Sel.Name]
		return unit, ok
	case *ast.CallExpr:
		if callName(node) == "time.Duration" && len(node.Args) == 1 {
			return constantDuration(node.Args[0])
		}
	case *ast.BinaryExpr:
		if node.Op != token.MUL {
			return 0, false
		}
		x, okX := constantDuration(node.X)
		y, okY := constantDuration(node.Y)
		return x * y, okX && okY
	}
	return 0, false
}
