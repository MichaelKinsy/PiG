//go:build parity

package runner

import (
	"slices"
	"testing"
)

func TestValidatePerBinaryExitCodes(t *testing.T) {
	pigCode, piCode, common := 2, 0, 1
	valid := &Scenario{Name: "valid", Driver: "cli-mode", Covers: []string{"x"}, Assert: AssertSpec{PigExitCode: &pigCode, PiExitCode: &piCode}}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	missingPair := &Scenario{Name: "missing", Driver: "cli-mode", Covers: []string{"x"}, Assert: AssertSpec{PigExitCode: &pigCode}}
	if err := validate(missingPair); err == nil {
		t.Fatal("single per-binary exit code accepted")
	}
	mixed := &Scenario{Name: "mixed", Driver: "cli-mode", Covers: []string{"x"}, Assert: AssertSpec{ExitCode: &common, PigExitCode: &pigCode, PiExitCode: &piCode}}
	if err := validate(mixed); err == nil {
		t.Fatal("common and per-binary exit codes accepted together")
	}
}

// A step waits for the shared visible rows plus the rows of the binary it drives.
func TestTmuxStepVisibleContainsAddsTheBinarysOwnRows(t *testing.T) {
	step := TmuxStep{WaitVisibleContains: []string{"shared"}, WaitVisibleContainsPig: []string{"pig row"}, WaitVisibleContainsPi: []string{"pi row"}}
	if got := step.visibleContains("pig"); !slices.Equal(got, []string{"shared", "pig row"}) {
		t.Errorf("pig waits for %q", got)
	}
	if got := step.visibleContains("pi"); !slices.Equal(got, []string{"shared", "pi row"}) {
		t.Errorf("pi waits for %q", got)
	}
	if got := (TmuxStep{}).visibleContains("pi"); len(got) != 0 {
		t.Errorf("an empty step waits for %q", got)
	}
}
