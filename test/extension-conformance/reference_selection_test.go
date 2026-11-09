package extensionconformance

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestComparisonReferencesRecordWhateverRowsRunSelects: a comparison test compares each SDK row with a reference row, and `-run` selects rows by name. A selection
// that leaves the reference row out (`-run '/(lisp|python)/shared-ok'` selects no go or subprocess-node row) used to leave the other rows comparing with the
// reference's zero value: the liveness test diffed python against an empty Go recording, the number-argument test failed with "the Node reference ran 0 of 28
// inputs", the two transport tests skipped, and the system-prompt-options test compared with nothing and passed. This runs each of those tests in a child process that
// selects only its fused-go row, which is never the reference, and requires that row to run and pass after its test recorded the reference.
func TestComparisonReferencesRecordWhateverRowsRunSelects(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the Go and Rust SDK fixtures and runs five conformance tests")
	}
	t.Parallel()

	tests := []string{
		"TestLivenessConformanceSDKsMatch",
		"TestConformance_NumberAndKeyArgumentsAgreeAcrossSDKs",
		"TestConformance_TransportsMatch",
		"TestConformance_OAuthTransportsMatch",
		"TestSystemPromptOptionsAcrossIdenticalAndPartialStatePushes",
	}
	const row = "fused-go"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.DeadlineContext(t)
	// The child shares this process's fixture builds instead of building its own.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^("+strings.Join(tests, "|")+")$/^"+regexp.QuoteMeta(row)+"$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN="+buildSDKFixture(t),
		"PIG_TEST_RUST_SDK_FIXTURE_BIN="+buildRustSDKFixture(t),
	)
	output, err := cmd.CombinedOutput()
	if expired := testbudget.Expired(ctx); expired != "" {
		t.Fatalf("%s:\n%s", expired, output)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	if err != nil {
		t.Errorf("selecting only the %s row of the comparison tests: %v", row, err)
	}
	for _, test := range tests {
		if !strings.Contains(string(output), "--- PASS: "+test+"/"+row+" ") {
			t.Errorf("%s/%s did not run and pass when only that row was selected", test, row)
		}
	}
	if strings.Contains(string(output), "--- SKIP:") {
		t.Error("a comparison row skipped when only that row was selected: its reference row was not recorded")
	}
	// A row that passes compared with something only if it recorded the reference first: it says so when it does.
	if got := strings.Count(string(output), "reference row has not recorded"); got != len(tests) {
		t.Errorf("%d rows recorded their test's reference, want %d: a row that did not record it compared with nothing", got, len(tests))
	}
	if t.Failed() {
		t.Logf("child output:\n%s", output)
	}
}
