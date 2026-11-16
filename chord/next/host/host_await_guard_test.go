package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// GO-ONLY guard of the await seam. awaitTask, awaitTail, awaitAfter and awaitEnding are Pi's `await work()` for a work that
// blocks: work runs on a task, outside any section, and its outcome settles a promise in a section of its own. Pi continues an
// await in the turn in which the awaited promise resolved: no other task runs between the last statement of the awaited
// function and its awaiter's continuation. Here the last statement of a work and the settling section are two sections with
// others between, so a work whose last act reads the host's state in a section, and whose reaction acts on that reading, hands
// the reaction a verdict that may be stale: a reader found not ready that is ready when the reaction follows it never wakes its
// attempt (the hang this guard was written for: openReaders, Load never returned). A function that ends by reading the host's
// state after its last await is awaitTail, which reads it in the settling section.
//
// Every site is listed here with its class and its reason; a new site fails until it is classified, which is when its work and
// its reaction are read against that rule.

type awaitClass string

const (
	// classOwnResult: the outcome is what work did (an error of a store, the closes that threw, a verdict of a timer race):
	// nothing of the host's state that the reaction acts on is read by work's last act.
	classOwnResult awaitClass = "own result"
	// classTail: the function reads the host's state after its last await, and is awaitTail.
	classTail awaitClass = "tail"
	// classStaleSafe: work ends by reading the host's state and the reaction acts on it in a later section, and the action does
	// not need the reading to still hold: the orders the gap admits are orders Pi admits.
	classStaleSafe awaitClass = "stale-safe"
	// classExposed: work ends by reading state that the reaction acts on, and is not awaitTail: a named follow-up.
	classExposed awaitClass = "exposed"
)

type awaitSite struct {
	file, function, callee string
	count                  int
	class                  awaitClass
	reason                 string
}

var awaitSites = []awaitSite{
	{"host_admission.go", "chordObject.rebuild", "awaitTask", 1, classOwnResult, "the failures of the old attempt's close"},
	{"host_attempt.go", "attempt.activateSteps", "awaitTask", 1, classOwnResult, "the error of documents.Load"},
	{"host_attempt.go", "attempt.drainedWithin", "awaitTask", 1, classOwnResult, "the verdict of the drain's race against its timer"},
	{"host_attempt.go", "attempt.loadJobs", "awaitTask", 1, classOwnResult,
		"the error of a job's load: its last act applies what it restored, and its verdict is the error of its loads"},
	{"host_attempt.go", "attempt.loadQueries", "awaitTask", 1, classStaleSafe,
		"LiveQuery.Load ends in a section that reads Failure and the reaction fails the attempt on it, where Pi reads it in the " +
			"turn that resumes the await (live-query.ts:73-75, attempt.ts:1462), so what lands in the gap is a later adoption " +
			"(live-query.ts:124-134, a task of its own after the check), an order Pi admits, and the act does not need the " +
			"verdict to still hold"},
	{"host_attempt.go", "attempt.ownSteps", "awaitTask", 2, classOwnResult, "the error of a followed source's attach and of a native's open"},
	{"host_attempt.go", "attempt.prepareTables", "awaitTask", 1, classOwnResult,
		"the error of PrepareTables; the reaction binds the sets from the live store"},
	{"host_object.go", "awaitEnding", "awaitAfter", 1, classOwnResult, "the ending is its caller's work, run after the previous ending's"},
	{"host_object.go", "chordObject.end", "awaitAfter", 1, classOwnResult, "the failures of the object's teardown closes"},
	{"host_object.go", "chordObject.remove", "awaitAfter", 1, classOwnResult, "the failures of the object's teardown closes"},
	{"host_object.go", "chordObject.endOld", "awaitEnding", 2, classOwnResult, "the failures of the old attempt's close, and the settled drain"},
	{"host_object.go", "chordObject.failLive", "awaitEnding", 2, classOwnResult, "the failures of the live attempt's close"},
	{"host_object.go", "chordObject.failedAttempt", "awaitEnding", 1, classOwnResult, "the failures of the failed attempt's close"},
	{"host_object.go", "chordObject.retryReloaded", "awaitEnding", 1, classOwnResult, "the failures of the old attempt's close"},
	{"host_read_declarations.go", "openReaders", "awaitTail", 1, classTail,
		"how a reader's opening ended is read in the settling section, where the reader is followed when it is not ready " +
			"(read-declarations.ts:43-45)"},
}

// awaitCallees are the constructors of the seam.
var awaitCallees = map[string]bool{"awaitTask": true, "awaitTail": true, "awaitAfter": true, "awaitEnding": true}

// foundAwaitSites are the calls of the seam's constructors in the package's production files, by file, function and callee.
func foundAwaitSites(t *testing.T) map[[3]string]int {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[[3]string]int{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			name := function.Name.Name
			if function.Recv != nil && len(function.Recv.List) == 1 {
				name = receiverName(function.Recv.List[0].Type) + "." + name
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if callee, ok := call.Fun.(*ast.Ident); ok && awaitCallees[callee.Name] {
					found[[3]string{file, name, callee.Name}]++
				}
				return true
			})
		}
	}
	return found
}

// receiverName is the type name of a method's receiver (`*attempt` is `attempt`).
func receiverName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return receiverName(typed.X)
	case *ast.IndexExpr:
		return receiverName(typed.X)
	case *ast.Ident:
		return typed.Name
	}
	return "?"
}

func TestEveryAwaitOfATaskIsClassifiedAgainstTheTurnRule(t *testing.T) {
	found := foundAwaitSites(t)
	listed := map[[3]string]awaitSite{}
	for _, site := range awaitSites {
		key := [3]string{site.file, site.function, site.callee}
		if _, twice := listed[key]; twice {
			t.Errorf("%s %s: %s is listed twice", site.file, site.function, site.callee)
		}
		listed[key] = site
		switch {
		case site.reason == "":
			t.Errorf("%s %s: %s is listed without its reason", site.file, site.function, site.callee)
		case site.class != classOwnResult && site.class != classTail && site.class != classStaleSafe && site.class != classExposed:
			t.Errorf("%s %s: %s has the class %q, want one of the four", site.file, site.function, site.callee, site.class)
		case (site.callee == "awaitTail") != (site.class == classTail):
			t.Errorf("%s %s: %s is class %q: awaitTail is the class tail and only it", site.file, site.function, site.callee, site.class)
		case site.class == classExposed && !strings.HasPrefix(site.reason, "follow-up: "):
			t.Errorf("%s %s: an exposed site names its follow-up (a reason starting `follow-up: `)", site.file, site.function)
		}
	}
	keys := make([][3]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.Join(keys[i][:], "\x00") < strings.Join(keys[j][:], "\x00") })
	for _, key := range keys {
		site, ok := listed[key]
		switch {
		case !ok:
			t.Errorf("%s: %s calls %s and is not in awaitSites: read its work and its reaction against the turn rule (a work that ends by "+
				"reading the host's state in a section hands its reaction a stale verdict: the reaction reads what it acts on), then list it",
				key[0], key[1], key[2])
		case found[key] != site.count:
			t.Errorf("%s: %s calls %s %d times, awaitSites lists %d", key[0], key[1], key[2], found[key], site.count)
		}
	}
	for key := range listed {
		if found[key] == 0 {
			t.Errorf("%s: %s no longer calls %s: remove the entry", key[0], key[1], key[2])
		}
	}
}
