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
	"strings"
	"testing"
)

// themeSelectors are the tui functions that replace the process-global theme state a test can leave changed: the active theme, the registry it is looked up in,
// and the terminal capabilities its color mode follows. Every test in this package shares them, so a test that leaves its own set is seen by whichever test runs
// next (under -shuffle, a narrower -run or a different shard).
var themeSelectors = []string{
	"SetTheme", "SetThemeByName", "SetThemeByNameChecked", "SetThemeInstance", "SetThemeRegistry", "SetThemeSetting", "SetThemeSettingPresence",
	"SetCapabilities", "SetCapabilityOverrides", "ResetCapabilitiesCache",
}

type themeEventKind int

const (
	themeSelects themeEventKind = iota
	themeRestores
	themeCalls
)

// themeEvent is one thing a function body does to the global theme state, in source order.
type themeEvent struct {
	kind   themeEventKind
	callee string
}

func isThemeSelector(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "tui" && slices.Contains(themeSelectors, sel.Sel.Name)
}

// themeKey names a function by package, so a helper of package codingagent_test is not mistaken for one of package codingagent that shares its name. A method is
// keyed by its bare name, because the receiver's type is not known without type information; a call of any method of that name is taken to be a call of it.
func themeKey(pkg, name string, method bool) string {
	if method {
		return "." + name
	}
	return pkg + "." + name
}

func themeEventsOf(pkg string, body ast.Node) []themeEvent {
	var events []themeEvent
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == "restoreStartupTheme" {
				events = append(events, themeEvent{kind: themeRestores})
			} else {
				events = append(events, themeEvent{kind: themeCalls, callee: themeKey(pkg, fun.Name, false)})
			}
		case *ast.SelectorExpr:
			if isThemeSelector(call) {
				events = append(events, themeEvent{kind: themeSelects})
			} else if fun.Sel.Name == "RestoreStartupTheme" {
				// export_test.go's name for restoreStartupTheme, for the tests of package codingagent_test.
				events = append(events, themeEvent{kind: themeRestores})
			} else {
				events = append(events, themeEvent{kind: themeCalls, callee: themeKey(pkg, fun.Sel.Name, true)})
			}
		}
		return true
	})
	return events
}

// TestNoTestLeavesAThemeSelected is a static guard, as TestNoTestConstructsSubprocessHostWithoutPinningPIGHome is for PIG_HOME. The active theme, the theme registry
// and the terminal capabilities are process-global, so a test that changes one and does not put the previous one back changes every test that runs after it. A leak is
// invisible in the default order (later tests happen to select their own theme) and shows up as a failure in an unrelated test under -shuffle, a narrower -run or
// another shard: TestMermaidThemedTransformMatchesUpstream failed after TestExtensionErrorStackMatchesPi ended on the light theme, and two render tests fail every time
// after TestReloadDisposesEveryExtensionDialog left the system theme selected.
//
// Every Test function in this package's _test.go files that changes the theme state (tui.SetTheme, SetThemeByName, SetThemeByNameChecked, SetThemeInstance,
// SetThemeRegistry, SetThemeSetting, SetThemeSettingPresence, SetCapabilities, SetCapabilityOverrides or ResetCapabilitiesCache), directly or through a package
// function or method it calls by name, must call restoreStartupTheme (RestoreStartupTheme, from package codingagent_test) before the first change. Nothing else counts as a restore, and selecting "dark" is a change like any
// other: the process starts with dark built in truecolor, and a test that selects it again builds it in whatever color depth the capabilities report, which is 256
// colors without COLORTERM. restoreStartupTheme puts back the name, color mode, registry and capabilities of the theme it found, and a restore called after the change would
// put back the changed state.
//
// What this does not see is a change made by production code a test starts (a startup prompt replaces the registry, a reload selects the system theme): no call in the
// test's source names it. TestThemeStateIsAsTheProcessStarted, and the check TestMain runs after the last test, see those at run time.
func TestNoTestLeavesAThemeSelected(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	// A function defined in several files (build-tagged variants) has several bodies; it leaks if any one does.
	bodies := map[string][][]themeEvent{}
	type testFunc struct{ key, name string }
	var tests []testFunc
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
			if !ok || fn.Body == nil {
				continue
			}
			key := themeKey(f.Name.Name, fn.Name.Name, fn.Recv != nil)
			bodies[key] = append(bodies[key], themeEventsOf(f.Name.Name, fn.Body))
			if fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				tests = append(tests, testFunc{key, fmt.Sprintf("%s: %s", fset.Position(fn.Pos()), fn.Name.Name)})
			}
		}
	}

	// run follows the events of key in source order from a state that is restored or not, and reports whether a change was made before a restore, and whether
	// the function restored.
	type result struct{ leaks, restored bool }
	memo := map[[2]any]result{}
	var run func(key string, restored bool, visiting map[string]bool) result
	run = func(key string, restored bool, visiting map[string]bool) result {
		if r, ok := memo[[2]any{key, restored}]; ok {
			return r
		}
		if visiting[key] {
			return result{restored: restored}
		}
		visiting[key] = true
		defer delete(visiting, key)
		out := result{restored: true}
		for _, events := range bodies[key] {
			state := restored
			for _, ev := range events {
				switch ev.kind {
				case themeRestores:
					state = true
				case themeSelects:
					out.leaks = out.leaks || !state
				case themeCalls:
					if _, known := bodies[ev.callee]; known {
						r := run(ev.callee, state, visiting)
						out.leaks = out.leaks || r.leaks
						state = state || r.restored
					}
				}
			}
			out.restored = out.restored && state
		}
		memo[[2]any{key, restored}] = out
		return out
	}

	var leaks []string
	for _, fn := range tests {
		if run(fn.key, false, map[string]bool{}).leaks {
			leaks = append(leaks, fn.name)
		}
	}
	sort.Strings(leaks)
	if len(leaks) > 0 {
		t.Errorf("%d test(s) change the theme, its registry or the terminal capabilities without restoreStartupTheme(t) (startup_ui_theme_test.go) called first:\n  %s", len(leaks), strings.Join(leaks, "\n  "))
	}
}
