package codingagent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// themeSelectors are the tui functions that replace the process-global active theme, or the registry it is looked up in. Every test in this package
// shares them, so a test that leaves its own selected is seen by whichever test runs next (under -shuffle, a narrower -run or a different shard).
var themeSelectors = []string{"SetTheme", "SetThemeByName", "SetThemeByNameChecked", "SetThemeInstance", "SetThemeRegistry"}

// themeCallFacts is what one function body does to the global theme itself, and which package functions it calls by name.
type themeCallFacts struct {
	selects  bool
	restores bool
	calls    []string
}

func isThemeSelector(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "tui" && slices.Contains(themeSelectors, sel.Sel.Name)
}

// isDarkSelection reports whether call selects the built-in dark theme by name, which is the theme a process starts with and the one the cleanups in this package put back.
func isDarkSelection(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !slices.Contains([]string{"SetTheme", "SetThemeByName", "SetThemeByNameChecked"}, sel.Sel.Name) || len(call.Args) == 0 {
		return false
	}
	name, ok := call.Args[0].(*ast.BasicLit)
	if !ok || name.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(name.Value)
	return err == nil && value == "dark"
}

func containsThemeSelector(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		found = found || isThemeSelector(n)
		return !found
	})
	return found
}

func themeFactsOf(body ast.Node) themeCallFacts {
	var facts themeCallFacts
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.DeferStmt:
			// defer tui.SetTheme(previous) and defer func() { tui.SetTheme(previous) }() put a theme back.
			if containsThemeSelector(n.Call) {
				facts.restores = true
			}
		case *ast.CallExpr:
			switch fun := n.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "restoreStartupTheme" {
					facts.restores = true
				} else {
					facts.calls = append(facts.calls, fun.Name)
				}
			case *ast.SelectorExpr:
				if isThemeSelector(n) && !isDarkSelection(n) {
					facts.selects = true
				}
				// t.Cleanup(func() { tui.SetTheme(previous) }) puts a theme back.
				if fun.Sel.Name == "Cleanup" && slices.ContainsFunc(n.Args, func(arg ast.Expr) bool { return containsThemeSelector(arg) }) {
					facts.restores = true
				}
			}
		}
		return true
	})
	return facts
}

// TestNoTestLeavesAThemeSelected is a static guard, as TestNoTestConstructsSubprocessHostWithoutPinningPIGHome is for PIG_HOME. The active theme and the theme
// registry are process-global, so a test that selects a theme and does not put the previous one back changes every test that runs after it. A leak is invisible
// in the default order (later tests happen to select their own theme) and shows up as a failure in an unrelated test under -shuffle, a narrower -run or another
// shard: TestMermaidThemedTransformMatchesUpstream failed after TestExtensionErrorStackMatchesPi ended on the light theme.
//
// Every Test function in this package's _test.go files that selects a theme other than the built-in dark one (tui.SetTheme, SetThemeByName, SetThemeByNameChecked,
// SetThemeInstance or SetThemeRegistry), directly or through a package-level function it calls by name, must also call restoreStartupTheme or register a cleanup or
// defer that selects a theme, directly or through such a function. Selecting "dark" by name is not a leak: it is the theme the process starts with and the one the
// existing cleanups put back. The guard does not see a selection made by production code a test starts (a settings change or a startup prompt), and it does not see the
// color depth a selected theme was built in, so a test that needs a depth sets the terminal capabilities itself.
func TestNoTestLeavesAThemeSelected(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	facts := map[string]themeCallFacts{}
	var tests []*ast.FuncDecl
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			facts[fn.Name.Name] = themeFactsOf(fn.Body)
			if strings.HasPrefix(fn.Name.Name, "Test") {
				tests = append(tests, fn)
			}
		}
	}

	// A function selects or restores a theme when its own body does or when a package function it calls does.
	type effect struct{ selects, restores bool }
	memo := map[string]effect{}
	var resolve func(name string, visiting map[string]bool) effect
	resolve = func(name string, visiting map[string]bool) effect {
		if e, ok := memo[name]; ok {
			return e
		}
		f, ok := facts[name]
		if !ok || visiting[name] {
			return effect{}
		}
		visiting[name] = true
		e := effect{f.selects, f.restores}
		for _, callee := range f.calls {
			c := resolve(callee, visiting)
			e.selects = e.selects || c.selects
			e.restores = e.restores || c.restores
		}
		delete(visiting, name)
		memo[name] = e
		return e
	}

	var leaks []string
	for _, fn := range tests {
		if e := resolve(fn.Name.Name, map[string]bool{}); e.selects && !e.restores {
			leaks = append(leaks, fmt.Sprintf("%s: %s", fset.Position(fn.Pos()), fn.Name.Name))
		}
	}
	sort.Strings(leaks)
	if len(leaks) > 0 {
		t.Errorf("%d test(s) select a theme other than dark and never put the previous one back; start each with restoreStartupTheme(t) (startup_ui_theme_test.go):\n  %s", len(leaks), strings.Join(leaks, "\n  "))
	}
}
