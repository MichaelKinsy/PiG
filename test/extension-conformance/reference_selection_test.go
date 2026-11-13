package extensionconformance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	cmd.Env = append(cmd.Env, childTempEnv(os.TempDir())...)
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

// childTempEnv returns the variables that give the child test binary a temporary directory beside this process's scoped root instead of beneath it. This process's
// os.TempDir() is the pig-conf-* root its TestMain made, and the child's TestMain makes its own pig-conf-* root in whatever os.TempDir() is, so a child that inherits
// the variable nests a second pig-conf-* level inside the first. The liveness harness binds its sockets below os.TempDir() on Windows, where the unnested suite already
// uses most of the 108-byte AF_UNIX path (`D:\a\_temp\pig extension acceptance\pig-conf-<n>\pig-sdk-live-<n>\extension-0.sock`), and the nested level put the
// child's path past it: "listen unix ...: bind: invalid argument". Siblings share one depth, so the child binds at the length the suite does.
func childTempEnv(scopedRoot string) []string {
	parent := filepath.Dir(scopedRoot)
	return []string{"TMPDIR=" + parent, "TMP=" + parent, "TEMP=" + parent}
}

// TestChildTempRootIsASiblingOfTheParentsRoot: whatever the length of the real temporary directory, a child's liveness socket path is as long as the parent's and
// not one pig-conf-* level longer.
func TestChildTempRootIsASiblingOfTheParentsRoot(t *testing.T) {
	const (
		randomName = "4294967295" // the longest os.MkdirTemp suffix
		limit      = 107          // bytes of an AF_UNIX path on Linux and Windows
	)
	socket := func(temp string) string {
		return filepath.Join(temp, "pig-conf-"+randomName, "pig-sdk-live-"+randomName, "extension-0.sock")
	}
	for _, length := range []int{40, 60} {
		sep := string(filepath.Separator)
		realTemp := sep + strings.Repeat("d", length-1) // a temporary directory length bytes long
		parentRoot := filepath.Join(realTemp, "pig-conf-"+randomName)
		env := childTempEnv(parentRoot)
		if len(env) != 3 {
			t.Fatalf("childTempEnv = %q, want TMPDIR, TMP and TEMP", env)
		}
		for _, entry := range env {
			if _, value, _ := strings.Cut(entry, "="); value != realTemp {
				t.Errorf("%s: the child's temporary directory is not the real one %q", entry, realTemp)
			}
		}
		_, childTemp, _ := strings.Cut(env[0], "=")
		childSocket, parentSocket, nested := socket(childTemp), socket(realTemp), socket(parentRoot)
		if len(childSocket) != len(parentSocket) {
			t.Errorf("%d-byte temporary directory: the child's socket path is %d bytes, the parent's %d: %s", length, len(childSocket), len(parentSocket), childSocket)
		}
		if len(nested) <= len(parentSocket) {
			t.Errorf("%d-byte temporary directory: nesting does not lengthen the path (%d bytes), so this test proves nothing", length, len(nested))
		}
		if length == 40 && (len(childSocket) > limit || len(nested) <= limit) {
			t.Errorf("40-byte temporary directory: child path %d bytes, nested path %d bytes; want the child within %d and the nested one over it", len(childSocket), len(nested), limit)
		}
	}
}
