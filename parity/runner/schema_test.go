//go:build parity

package runner

import "testing"

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
