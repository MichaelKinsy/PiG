// SPDX-License-Identifier: MIT

package testenv

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// gateRecorder stands in for the *testing.T a gate fails: it records what the gate did and makes Fatal stop the goroutine as t.Fatal does.
type gateRecorder struct {
	testing.TB
	mu     sync.Mutex
	errors []string
	logs   []string
}

func (r *gateRecorder) Helper()      {}
func (r *gateRecorder) Name() string { return "TestRecorded" }
func (r *gateRecorder) Error(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, args[0].(string))
}
func (r *gateRecorder) Fatal(args ...any) {
	r.Error(args...)
	runtime.Goexit()
}
func (r *gateRecorder) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, format)
}

func runGate(fatal, failed bool) (r *gateRecorder, afterGate bool) {
	r = &gateRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if fatal {
			TimingGateFatal(r, failed, "took %d ms, bar %d", 9, 5)
		} else {
			TimingGate(r, failed, "took %d ms, bar %d", 9, 5)
		}
		afterGate = true
	}()
	<-done
	return r, afterGate
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	run()
	os.Stdout = original
	_ = writer.Close()
	out, _ := io.ReadAll(reader)
	return string(out)
}

func TestTimingGateBlocksUnlessReportOnly(t *testing.T) {
	t.Setenv(TimingReportOnlyEnv, "")
	for _, fatal := range []bool{false, true} {
		r, after := runGate(fatal, true)
		if len(r.errors) != 1 || r.errors[0] != "took 9 ms, bar 5" {
			t.Errorf("fatal=%v: blocking gate recorded %q, want the message", fatal, r.errors)
		}
		if after == fatal {
			t.Errorf("fatal=%v: code after the gate ran = %v; Error continues, Fatal stops", fatal, after)
		}
		if r, _ := runGate(fatal, false); len(r.errors) != 0 {
			t.Errorf("fatal=%v: a met bar failed the test: %q", fatal, r.errors)
		}
	}
	t.Setenv(TimingReportOnlyEnv, "0")
	if r, _ := runGate(false, true); len(r.errors) != 1 {
		t.Error("only the value 1 makes a timing gate report-only")
	}
}

func TestTimingGateReportOnlyWarnsAndPasses(t *testing.T) {
	t.Setenv(TimingReportOnlyEnv, "1")
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	for _, fatal := range []bool{false, true} {
		var r *gateRecorder
		var after bool
		out := captureStdout(t, func() { r, after = runGate(fatal, true) })
		if len(r.errors) != 0 || !after {
			t.Errorf("fatal=%v: report-only gate failed the test (errors %q, continued %v)", fatal, r.errors, after)
		}
		if want := "::warning title=timing gate::TestRecorded: took 9 ms, bar 5\n"; out != want {
			t.Errorf("fatal=%v: stdout = %q, want %q", fatal, out, want)
		}
	}
	if data, _ := os.ReadFile(summary); strings.Count(string(data), "::warning title=timing gate::") != 2 {
		t.Errorf("step summary = %q, want both warnings", data)
	}
	out := captureStdout(t, func() { _, _ = runGate(false, false) })
	if out != "" {
		t.Errorf("a met bar printed %q", out)
	}
	if got := escapeWorkflowCommand("50% a\r\nb"); got != "50%25 a%0D%0Ab" {
		t.Errorf("escapeWorkflowCommand = %q", got)
	}
}

// In report-only mode the step summary is where CI shows the miss; a summary that cannot be written must fail the test, not drop the miss.
func TestTimingGateReportOnlyFailsWhenTheSummaryCannotBeWritten(t *testing.T) {
	t.Setenv(TimingReportOnlyEnv, "1")
	t.Setenv("GITHUB_STEP_SUMMARY", t.TempDir()) // a directory: opening it for append fails
	var r *gateRecorder
	captureStdout(t, func() { r, _ = runGate(false, true) })
	if len(r.errors) != 1 || !strings.HasPrefix(r.errors[0], "timing gate: write step summary: ") {
		t.Errorf("unwritable step summary recorded %q, want one write-step-summary error", r.errors)
	}
}

// A timing gate that no release perf run executes is a gate nobody enforces: every test that reaches TimingGate, TimingGateFatal or the
// SDK module's copy of it must be named in automation/perf/timing-gates.json, which make test-timing runs with the gates blocking, and every
// listed name must be a test. The SDK module cannot import this package, so its copy must keep this one's environment variable and annotation.
func TestTimingGatesAreListed(t *testing.T) {
	root, err := ModuleRootPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "automation/perf/timing-gates.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Package string   `json:"package"`
		Tests   []string `json:"tests"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, entry := range entries {
		for _, name := range entry.Tests {
			if key := entry.Package + " " + name; listed[key] {
				t.Errorf("timing-gates.json lists %s twice", key)
			} else {
				listed[key] = true
			}
		}
	}
	used := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "third_party", ".upstream", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(source), "imingGate") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			return err
		}
		pkg := "./" + filepath.ToSlash(filepath.Dir(mustRel(t, root, path)))
		if pkg == "./internal/testenv" {
			return nil // this package's own tests exercise the gate with recorders
		}
		// A helper that calls the gate makes every test of the file that calls the helper a gate.
		gates := map[string]bool{}
		calls := func(body ast.Node, names map[string]bool) bool {
			found := false
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := call.Fun.(type) {
				case *ast.SelectorExpr:
					found = found || (names[fn.Sel.Name] && isTestenv(fn.X))
				case *ast.Ident:
					found = found || names[fn.Name]
				}
				return true
			})
			return found
		}
		direct := map[string]bool{"TimingGate": true, "TimingGateFatal": true, "timingGate": true}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && !strings.HasPrefix(fn.Name.Name, "Test") && calls(fn.Body, direct) {
				gates[fn.Name.Name] = true
			}
		}
		for name := range direct {
			gates[name] = true
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && strings.HasPrefix(fn.Name.Name, "Test") && fn.Recv == nil && calls(fn.Body, gates) {
				used[pkg+" "+fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range used {
		if !listed[key] {
			t.Errorf("%s reaches a timing gate but is not in automation/perf/timing-gates.json", key)
		}
	}
	for key := range listed {
		if !used[key] {
			t.Errorf("automation/perf/timing-gates.json lists %s, which reaches no timing gate", key)
		}
	}
	copyOfGate, err := os.ReadFile(filepath.Join(root, "extensions/sdk/json/timing_gate_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"` + TimingReportOnlyEnv + `"`, `"::warning title=timing gate::"`, `"GITHUB_STEP_SUMMARY"`, `"%", "%25", "\r", "%0D", "\n", "%0A"`} {
		if !strings.Contains(string(copyOfGate), want) {
			t.Errorf("extensions/sdk/json/timing_gate_test.go does not contain %s", want)
		}
	}
	if !slices.Contains(strings.Fields(string(data)), `"./extensions/sdk/json",`) {
		t.Error("timing-gates.json must list the SDK json package")
	}
}

func isTestenv(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "testenv"
}

func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
