package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Run the real acceptance tests with an empty tool search path. A skipped test
// exits zero, so requiring a named failure guards every missing-tool boundary.
func TestRequiredSDKToolsFailWhenMissing(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	cases := []struct{ test, tool string }{
		{"TestSlowNodeFactoryLoads", "node"},
		{"TestSlowAutocompleteProviderShowsItems", "node"},
		{"TestCleanExtensionExitIsReported", "node"},
		{"TestNodeActionsThrowDuringFactory", "node"},
		{"TestUnknownEventHandlerIsAnErrorInEverySDK/node", "node"},
		{"TestUnknownEventHandlerIsAnErrorInEverySDK/python", "python3"},
		{"TestUnknownEventHandlerIsAnErrorInEverySDK/rust", "cargo"},
		{"TestSessionLogLargerThanAPageReachesExtensionWhole", "node"},
		{"TestNodeRuntimeLoaderProvidesUpstreamHelloExampleExports", "node"},
		{"TestNodeRuntimePureHelpersMatchPinnedPi", "node"},
		{"TestNodeRuntimeLoaderResolvesExtensionlessTypeScript", "node"},
		{"TestNodeRuntimeLoaderServesPiAiCompatAndOAuth", "node"},
	}
	for _, tc := range cases {
		t.Run(tc.test, func(t *testing.T) {
			pattern := "^" + strings.ReplaceAll(tc.test, "/", "$/^") + "$"
			cmd := exec.CommandContext(testbudget.Context(t), executable, "-test.run="+pattern, "-test.v")
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(output), tc.tool+" is required") || strings.Contains(string(output), "--- SKIP:") {
				t.Fatalf("missing %s must fail acceptance with a diagnostic, got %v:\n%s", tc.tool, err, output)
			}
		})
	}
}
