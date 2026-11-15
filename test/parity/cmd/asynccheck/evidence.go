// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/test/parity/internal/gotests"
)

// The classes of an evidence entry that does not resolve. An entry is kind:path[#name]; what each kind must name is in
// test/parity/README.md (Async contract evidence).
const (
	// classInvalid is an entry that is not kind:path, or carries a #name its kind does not take.
	classInvalid = "invalid-evidence"
	// classKind is a kind, or a file type under a kind, that the checker has no resolver for.
	classKind = "unresolvable-kind"
	// classFile is a cited file that is not in the tree.
	classFile = "missing-file"
	// classUnnamed is a test file cited without the test in it.
	classUnnamed = "unnamed-test"
	// classNotFound is a named test, case or heading that the cited file does not declare.
	classNotFound = "name-not-found"
	// classSubtest is a subtest that is not a string literal of a t.Run of its parent: it may exist under a computed name,
	// which this checker cannot tell from one that was renamed or removed. Cite the parent instead.
	classSubtest = "subtest-not-found-statically"
	// classSkipped is a test that is declared but does not run: it begins with a skip, or is marked skipped or ignored.
	classSkipped = "skipped-evidence"
	// classScenario is a scenario that does not decode or does not list the row's source file in covers.
	classScenario = "scenario-not-covering"
)

const scenarioRoot = "test/parity/scenarios/"

// evidenceChecker resolves the evidence entries of the ledger against the repository, reading each cited file once.
type evidenceChecker struct {
	repoRoot string
	goFiles  map[string]goTestFile
	texts    map[string]string
}

type goTestFile struct {
	gotests.File
	err error
	// testenv is the name internal/testenv is imported under, empty when the file does not import it.
	testenv string
}

func newEvidenceChecker(repoRoot string) *evidenceChecker {
	return &evidenceChecker{repoRoot: repoRoot, goFiles: map[string]goTestFile{}, texts: map[string]string{}}
}

// check resolves one evidence entry of the row for the upstream file row.
func (c *evidenceChecker) check(row, entry string) []problem {
	class, reason := c.resolve(row, entry)
	if class == "" {
		return nil
	}
	return []problem{{Class: class, Text: fmt.Sprintf("%s evidence %q: %s", row, entry, reason)}}
}

func (c *evidenceChecker) resolve(row, entry string) (class, reason string) {
	kind, cited, ok := strings.Cut(entry, ":")
	if !ok {
		return classInvalid, "is not kind:path[#name]"
	}
	if kind != "test" && kind != "scenario" && kind != "probe" {
		return classKind, fmt.Sprintf("evidence kind %q is not test, scenario or probe", kind)
	}
	file, name, named := strings.Cut(cited, "#")
	full := filepath.Join(c.repoRoot, filepath.FromSlash(file))
	info, err := os.Stat(full)
	if err != nil {
		return classFile, err.Error()
	}
	if info.IsDir() {
		return classKind, kind + " evidence in a directory"
	}
	ext := path.Ext(file)
	switch kind {
	case "scenario":
		switch {
		case ext != ".toml":
			return classKind, fmt.Sprintf("scenario evidence in a %q file: a scenario is a TOML file under %s", ext, scenarioRoot)
		case !strings.HasPrefix(file, scenarioRoot):
			return classKind, "scenario evidence outside " + strings.TrimSuffix(scenarioRoot, "/") + ": the parity runner does not load it"
		case named:
			return classInvalid, "a scenario is cited as a whole file, without #name"
		}
		var scenario struct {
			Covers []string `toml:"covers"`
		}
		if _, err := toml.DecodeFile(full, &scenario); err != nil {
			return classScenario, err.Error()
		}
		if !slices.Contains(scenario.Covers, row) {
			return classScenario, "does not cover the audited path"
		}
		return "", ""
	case "probe":
		if ext != ".md" {
			return classKind, fmt.Sprintf("probe evidence in a %q file: a probe is a Markdown document", ext)
		}
		if name == "" {
			return "", ""
		}
		return c.inText(full, func(text string) (string, string) { return markdownHeading(file, text, name) })
	}

	var resolveTest func(text string) (string, string)
	base := path.Base(file)
	switch {
	case strings.HasSuffix(base, "_test.go"):
	case ext == ".go":
		return classKind, `test evidence in a ".go" file that is not a test file (_test.go)`
	case slices.Contains([]string{".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs"}, ext):
		if !strings.Contains(base, ".test.") && !strings.Contains(base, ".spec.") {
			return classKind, fmt.Sprintf("test evidence in a %q file that is not a test file (.test. or .spec.)", ext)
		}
		resolveTest = func(text string) (string, string) { return typeScriptCase(file, text, name) }
	case ext == ".py":
		resolveTest = func(text string) (string, string) { return pythonTest(file, text, name) }
	case ext == ".rs":
		resolveTest = func(text string) (string, string) { return rustTest(file, text, name) }
	case ext == ".lisp":
		resolveTest = func(text string) (string, string) { return lispTest(file, text, name) }
	default:
		return classKind, fmt.Sprintf("test evidence in a %q file: the checker resolves Go, TypeScript, Python, Rust and Lisp tests", ext)
	}
	if name == "" {
		return classUnnamed, "names no test: write " + file + "#Name"
	}
	if resolveTest == nil {
		return c.goTest(file, full, name)
	}
	return c.inText(full, resolveTest)
}

func (c *evidenceChecker) inText(full string, resolve func(text string) (string, string)) (class, reason string) {
	text, ok := c.texts[full]
	if !ok {
		body, err := os.ReadFile(full)
		if err != nil {
			return classFile, err.Error()
		}
		text = string(body)
		c.texts[full] = text
	}
	return resolve(text)
}

// goTest resolves Name or Name/sub/... in a Go test file: Name is a test or fuzz target the file declares, each subtest is a
// string literal of a t.Run in the body of its parent, and none of them begins with a skip.
func (c *evidenceChecker) goTest(file, full, name string) (class, reason string) {
	parsed, ok := c.goFiles[full]
	if !ok {
		parsed.File, parsed.err = gotests.Parse(full)
		if parsed.err == nil {
			parsed.testenv = importName(parsed.Syntax, "/internal/testenv")
		}
		c.goFiles[full] = parsed
	}
	if parsed.err != nil {
		return classNotFound, parsed.err.Error()
	}
	top, subtests, _ := strings.Cut(name, "/")
	fn := parsed.Tests[top]
	if fn == nil {
		return classNotFound, fmt.Sprintf("%s declares no test %s", file, top)
	}
	at, body, param := top, fn.Body, parameterName(fn.Type)
	for {
		if call := leadingSkip(body, param, parsed.testenv); call != "" {
			return classSkipped, fmt.Sprintf("%s begins with %s: a test that skips where the gate runs is not evidence", at, call)
		}
		if subtests == "" {
			return "", ""
		}
		var sub string
		sub, subtests, _ = strings.Cut(subtests, "/")
		lit, found := findSubtest(body, sub)
		if !found {
			return classSubtest, fmt.Sprintf("subtest %q of %s is not a string literal of a t.Run in it (subtest not found statically)", sub, at)
		}
		at += "/" + sub
		if lit == nil {
			if subtests != "" {
				return classSubtest, fmt.Sprintf("subtest %q of %s: its t.Run takes a named function (subtest not found statically)", subtests, at)
			}
			return "", ""
		}
		body, param = lit.Body, parameterName(lit.Type)
	}
}

// importName is the name the file refers to the package whose import path ends in suffix by, empty when it does not import it.
func importName(file *ast.File, suffix string) string {
	for _, imp := range file.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !strings.HasSuffix(importPath, suffix) {
			continue
		}
		if imp.Name == nil {
			return path.Base(importPath)
		}
		if imp.Name.Name != "_" && imp.Name.Name != "." {
			return imp.Name.Name
		}
	}
	return ""
}

func parameterName(fn *ast.FuncType) string {
	if fn.Params.NumFields() != 1 || len(fn.Params.List[0].Names) != 1 {
		return ""
	}
	return fn.Params.List[0].Names[0].Name
}

// leadingSkip names the call a test body begins with when that call skips the test: t.Skip, t.Skipf or t.SkipNow on the
// test's own parameter, or testenv.RequireLiveEnv, which skips wherever the live credentials are absent (the gate has none).
// A skip under a condition is not one: the test runs where the condition does not hold.
func leadingSkip(body *ast.BlockStmt, param, testenv string) string {
	if len(body.List) == 0 {
		return ""
	}
	var first ast.Expr
	switch stmt := body.List[0].(type) {
	case *ast.ExprStmt:
		first = stmt.X
	case *ast.AssignStmt:
		if len(stmt.Rhs) == 1 {
			first = stmt.Rhs[0]
		}
	}
	call, ok := first.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	method := selector.Sel.Name
	skips := param != "" && receiver.Name == param && (method == "Skip" || method == "Skipf" || method == "SkipNow")
	if skips || (testenv != "" && receiver.Name == testenv && method == "RequireLiveEnv") {
		return receiver.Name + "." + method
	}
	return ""
}

// findSubtest finds the t.Run in body whose name is the string literal name (as written, or as go test prints it). The
// function literal is nil when the subtest runs a named function. A t.Run inside another subtest's literal belongs to that
// subtest and is not searched.
func findSubtest(body *ast.BlockStmt, name string) (fn *ast.FuncLit, found bool) {
	want := subtestName(name)
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Run" || len(call.Args) != 2 {
			return true
		}
		lit, isLit := call.Args[1].(*ast.FuncLit)
		if title, ok := call.Args[0].(*ast.BasicLit); ok && title.Kind == token.STRING {
			if value, err := strconv.Unquote(title.Value); err == nil && subtestName(value) == want {
				fn, found = lit, true
				return false
			}
		}
		return !isLit
	})
	return fn, found
}

// subtestName is the name go test gives a subtest: white space becomes an underscore.
func subtestName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return '_'
		}
		return r
	}, name)
}

// typeScriptCase resolves a case title of a vitest or node:test file: the title is the string-literal first argument of an it,
// test or describe call (with any modifier, such as it.each(...) or it.skipIf(...)), and at least one such call is neither
// skipped (it.skip) nor declared pending.
func typeScriptCase(file, text, title string) (class, reason string) {
	quoted := regexp.QuoteMeta(title)
	call := regexp.MustCompile(`(?m)(?:^|[\s;{(])(it|test|describe)((?:\.[A-Za-z]+(?:\([^()\n]*\))?)*)\s*\(\s*` +
		`(?:"` + quoted + `"|'` + quoted + `'|` + "`" + quoted + "`" + `)`)
	matches := call.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return classNotFound, fmt.Sprintf("no it, test or describe call titled %q in %s", title, file)
	}
	var skipped string
	for _, match := range matches {
		modifiers := strings.Split(match[2], ".")
		switch {
		case slices.Contains(modifiers, "skip"):
			skipped = match[1] + ".skip"
		case slices.Contains(modifiers, "todo"):
			skipped = match[1] + ".todo"
		default:
			return "", ""
		}
	}
	return classSkipped, fmt.Sprintf("%q is declared with %s", title, skipped)
}

// pythonTest resolves test_name or Class::test_name in a pytest file: a def of that name at the top level (in the class, for a
// method) that pytest collects and that is not decorated @pytest.mark.skip or @unittest.skip.
func pythonTest(file, text, name string) (class, reason string) {
	owner, function, method := strings.Cut(name, "::")
	if !method {
		owner, function = "", name
	}
	if !strings.HasPrefix(function, "test") {
		return classNotFound, function + " is not a test: pytest collects test* functions"
	}
	lines := strings.Split(text, "\n")
	start := 0
	if method {
		classLine := regexp.MustCompile(`^class\s+` + regexp.QuoteMeta(owner) + `\s*[(:]`)
		start = slices.IndexFunc(lines, classLine.MatchString) + 1
		if start == 0 {
			return classNotFound, fmt.Sprintf("no class %s in %s", owner, file)
		}
	}
	def := regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+` + regexp.QuoteMeta(function) + `\s*\(`)
	for i := start; i < len(lines); i++ {
		line := lines[i]
		topLevel := line != "" && !unicode.IsSpace(rune(line[0]))
		if method && topLevel && !strings.HasPrefix(line, "#") {
			break
		}
		if match := def.FindStringSubmatch(line); match == nil || (match[1] == "") == method {
			continue
		}
		for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "@"); j-- {
			decorator, _, _ := strings.Cut(strings.TrimSpace(lines[j]), "(")
			if decorator == "@pytest.mark.skip" || decorator == "@unittest.skip" {
				return classSkipped, fmt.Sprintf("%s is decorated %s", function, decorator)
			}
		}
		return "", ""
	}
	return classNotFound, fmt.Sprintf("no def %s in %s", name, file)
}

var rustTestAttribute = regexp.MustCompile(`^#\[(?:[A-Za-z_][A-Za-z0-9_]*::)*test(?:\(.*\))?\]`)

// rustTest resolves name or module::name in a Rust file: a fn of that name under a #[test] attribute (or a runtime's, such as
// #[tokio::test]) and not under #[ignore].
func rustTest(file, text, name string) (class, reason string) {
	function := name
	if i := strings.LastIndex(name, "::"); i >= 0 {
		function = name[i+2:]
	}
	def := regexp.MustCompile(`^\s*(?:pub(?:\([a-z]+\))?\s+)?(?:async\s+)?fn\s+` + regexp.QuoteMeta(function) + `\s*[(<]`)
	lines := strings.Split(text, "\n")
	ignored := false
	for i, line := range lines {
		if !def.MatchString(line) {
			continue
		}
		isTest, ignore := false, false
		for j := i - 1; j >= 0; j-- {
			attribute := strings.TrimSpace(lines[j])
			if strings.HasPrefix(attribute, "//") {
				continue
			}
			if !strings.HasPrefix(attribute, "#[") {
				break
			}
			isTest = isTest || rustTestAttribute.MatchString(attribute)
			ignore = ignore || strings.HasPrefix(attribute, "#[ignore")
		}
		if isTest && !ignore {
			return "", ""
		}
		ignored = ignored || isTest
	}
	if ignored {
		return classSkipped, function + " is #[ignore]d"
	}
	return classNotFound, fmt.Sprintf("no #[test] fn %s in %s", function, file)
}

// lispTest resolves the name of a (deftest name ...) form, the test macro of extensions/sdk-lisp/tests/harness.lisp.
func lispTest(file, text, name string) (class, reason string) {
	form := regexp.MustCompile(`(?m)^\(deftest\s+` + regexp.QuoteMeta(name) + `(?:[\s)]|$)`)
	if !form.MatchString(text) {
		return classNotFound, fmt.Sprintf("no (deftest %s form in %s", name, file)
	}
	return "", ""
}

// markdownHeading resolves a heading of a document by its text.
func markdownHeading(file, text, name string) (class, reason string) {
	for line := range strings.SplitSeq(text, "\n") {
		if heading := strings.TrimLeft(line, "#"); heading != line && strings.TrimSpace(heading) == name {
			return "", ""
		}
	}
	return classNotFound, fmt.Sprintf("no heading %q in %s", name, file)
}
