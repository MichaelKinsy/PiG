// SPDX-License-Identifier: MIT

package plan_test

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade/plan"
)

// Every type of package plan is the type of that name in package upgrade: a value passes between the two unconverted.
var (
	_ upgrade.Change      = plan.Change{}
	_ upgrade.Rule        = plan.Rule{}
	_ upgrade.Diagnostic  = plan.Diagnostic{}
	_ upgrade.Drift       = plan.Drift{}
	_ upgrade.FileSource  = plan.FileSource(nil)
	_ upgrade.LoadOptions = plan.LoadOptions{}
	_ upgrade.Rewrite     = plan.Rewrite{}
	_ upgrade.FileChange  = plan.FileChange{}
	_ upgrade.Skipped     = plan.Skipped{}
	_ *upgrade.Result     = (*plan.Result)(nil)
	_ upgrade.Reply       = plan.Reply{}
)

// A program that imports package plan links no type checker (docs/parity/divergences/D272.md). The dependencies of this package
// hold none of the packages the record names, and not package upgrade, which imports them.
func TestPlanDoesNotImportTheTypeChecker(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	if !strings.Contains(string(out), "/extensions/sdk/upgrade/internal/upgrade\n") {
		t.Fatalf("go list -deps does not list the internal package:\n%s", out)
	}
	for dependency := range strings.FieldsSeq(string(out)) {
		switch {
		case dependency == "go/types", dependency == "go/importer", dependency == "go/printer", dependency == "go/format", strings.HasPrefix(dependency, "go/internal/"), strings.HasSuffix(dependency, "/extensions/sdk/upgrade"):
			t.Errorf("package plan depends on %s", dependency)
		}
	}
}

// The functions of package plan are those of package upgrade: one build output classifies to the same drift, a rule is
// the same rule, a diff the same text.
func TestPlanFunctionsAreTheUpgradeFunctions(t *testing.T) {
	output := []byte("# example.com/ext\n./main.go:12:9: assignment mismatch: 1 variable but ctx.GetSessionID returns 2 values\n./main.go:14:2: undefined: nothing\n")
	diagnostics := plan.ParseDiagnostics(output)
	if want := upgrade.ParseDiagnostics(output); len(diagnostics) != 2 || !reflect.DeepEqual(diagnostics, want) {
		t.Fatalf("ParseDiagnostics = %+v, package upgrade gives %+v", diagnostics, want)
	}
	drift := plan.Classify(diagnostics, nil)
	if want := upgrade.Classify(diagnostics, nil); len(drift) != 1 || !reflect.DeepEqual(drift, want) {
		t.Fatalf("Classify = %+v, package upgrade gives %+v", drift, want)
	}
	for _, rule := range upgrade.Rules() {
		if got, ok := plan.RuleByID(rule.ID); !ok || !reflect.DeepEqual(got, rule) {
			t.Errorf("RuleByID(%q) = %+v, %v; want %+v", rule.ID, got, ok, rule)
		}
	}
	before, after := []byte("a\nb\nc\n"), []byte("a\nB\nc\n")
	if got, want := plan.Diff("main.go", before, after), upgrade.Diff("main.go", before, after); got == "" || got != want {
		t.Errorf("Diff = %q, package upgrade gives %q", got, want)
	}
}
