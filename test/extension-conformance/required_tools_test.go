package extensionconformance

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestRequiredSDKToolsFailWhenMissing(t *testing.T) {
	const helperEnv = "PIG_TEST_MISSING_CONFORMANCE_TOOL"
	if helper := os.Getenv(helperEnv); helper != "" {
		switch helper {
		case "node-isolated":
			makeSubprocessNodeHarness(t)
		case "node-packed":
			makeSubprocessNodePackedHarness(t)
		case "python":
			buildPythonSDKFixture(t)
		case "rust":
			t.Setenv("PIG_TEST_RUST_SDK_FIXTURE_BIN", "")
			buildRustSDKFixture(t)
		default:
			t.Fatalf("unknown missing-tool helper: %q", helper)
		}
		t.Fatal("missing-tool check returned without failing")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct{ name, tool string }{
		{"node-isolated", "node"},
		{"node-packed", "node"},
		{"python", testPythonExecutable()},
		{"rust", "cargo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(helperEnv, tc.name)
			if tc.name == "rust" {
				// The grouped verifier supplies an existing prebuilt fixture. The missing-Cargo probe must still exercise source-build preflight.
				t.Setenv("PIG_TEST_RUST_SDK_FIXTURE_BIN", executable)
			}
			cmd := exec.CommandContext(testbudget.Context(t), executable, "-test.run=^TestRequiredSDKToolsFailWhenMissing$", "-test.v")
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(output), tc.tool+" is required") || strings.Contains(string(output), "--- SKIP:") {
				t.Fatalf("missing %s must fail acceptance with a diagnostic, got %v:\n%s", tc.tool, err, output)
			}
		})
	}
}
