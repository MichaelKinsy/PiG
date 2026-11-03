//go:build !windows

package configvalue

// pi: packages/coding-agent/src/core/resolve-config-value.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

func TestPortWave11ResolveConfigValueConfiguredStdin(t *testing.T) {
	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:105-120. Pi runs this on Unix, mocks getShellConfig and selects win32 dispatch, but executes the real /bin/bash.
	t.Run("uses stdin when the configured Windows shell requires it", func(t *testing.T) {
		setupConfigValueTest(t)
		previous := getShellConfig
		t.Cleanup(func() { getShellConfig = previous })
		calls := 0
		getShellConfig = func() (shellconfig.Config, error) {
			calls++
			return shellconfig.Config{Path: "/bin/bash", Args: []string{"-s"}, CommandTransport: "stdin"}, nil
		}
		// runtime.GOOS cannot be reassigned like process.platform. Select the same production dispatcher called by runShellCommand on Windows instead of substituting a fake executor.
		t.Cleanup(SetExecutorForTest(runShellCommandWithConfiguredShell))

		if got := ResolveUncached(`!name='World'; echo "Hello, ${name}!"`, nil); got != "Hello, World!" {
			t.Fatalf("resolveConfigValueUncached with stdin transport = %q, want %q", got, "Hello, World!")
		}
		if calls != 1 {
			t.Fatalf("shell configuration lookups = %d, want one uncached execution", calls)
		}
	})
}
