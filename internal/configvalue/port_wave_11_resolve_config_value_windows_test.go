//go:build windows

package configvalue

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

func TestPortWave11ResolveConfigValueWindows(t *testing.T) {
	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:105-123. Assert Windows dispatch natively while mocking only the same shell-configuration boundary as Pi.
	t.Run("uses stdin when the configured Windows shell requires it", func(t *testing.T) {
		bash, err := shellconfig.Default()
		if err != nil {
			t.Fatalf("resolve the real Windows bash (install Git Bash on the runner): %v", err)
		}
		setupConfigValueTest(t)

		previous := getShellConfig
		t.Cleanup(func() { getShellConfig = previous })
		getShellConfig = func() (shellconfig.Config, error) {
			// upstream: packages/coding-agent/src/utils/shell.ts:15-21. Use the legacy stdin configuration with the runner's real bash, as Pi's mock does with /bin/bash.
			return shellconfig.Config{Path: bash.Path, Args: []string{"-s"}, CommandTransport: "stdin"}, nil
		}

		if got := ResolveUncached(`!name='World'; echo "Hello, ${name}!"`, nil); got != "Hello, World!" {
			t.Fatalf("resolveConfigValueUncached with stdin transport = %q, want %q", got, "Hello, World!")
		}
	})
}
