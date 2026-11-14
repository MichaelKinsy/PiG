package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The host's fused-source warning (D31) names a member that is loaded: it is shown as a warning, at a start and after a reload,
// while a failed load stays an error with its path.
func TestFusedSourceWarningIsAWarningDiagnostic(t *testing.T) {
	for _, issue := range []string{
		`extension "review" (fused): source at ./review differs from the fused copy; no Go toolchain: running the fused copy: cannot build go extension: the "go" toolchain is not installed; install it, or run this extension from a Piglet Binary or prebuilt package (which need no toolchain)`,
		`./review: source at ./review cannot be read as the Go module of the fused copy: running the fused copy: open ./review/go.mod: no such file or directory`,
	} {
		if got := extensionLoadIssueDiagnostic(issue); got.Type != extension.DiagnosticWarning || got.Message != issue || got.Path != "" {
			t.Errorf("diagnostic of %q = %+v, want a warning with the whole line", issue, got)
		}
	}
	failed := extensionLoadIssueDiagnostic("./bad: Failed to load extension: boom")
	if failed.Type != extension.DiagnosticError || failed.Path != "./bad" || failed.Message != "Failed to load extension: boom" {
		t.Errorf("a failed load = %+v", failed)
	}
}
