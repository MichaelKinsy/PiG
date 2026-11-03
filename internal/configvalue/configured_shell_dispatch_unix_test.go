//go:build !windows

package configvalue

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// Supplemental guard for the win32 dispatch rule in resolve-config-value.ts:153-205, run on Unix through the same production dispatcher. An executed command that fails is final (:174-175, :203); only a shell that could not start (ENOENT, :167-169) or a throwing getShellConfig (:180-181) falls back to the default shell.
func TestConfiguredShellDispatchFallback(t *testing.T) {
	useConfiguredShell := func(t *testing.T, config func() (shellconfig.Config, error)) {
		t.Helper()
		setupConfigValueTest(t)
		previous := getShellConfig
		t.Cleanup(func() { getShellConfig = previous })
		getShellConfig = config
		t.Cleanup(SetExecutorForTest(runShellCommandWithConfiguredShell))
	}

	t.Run("an executed failing command does not fall back to the default shell", func(t *testing.T) {
		useConfiguredShell(t, func() (shellconfig.Config, error) {
			return shellconfig.Config{Path: "/bin/sh", Args: []string{"-c"}}, nil
		})
		counter := filepath.Join(t.TempDir(), "counter")
		if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		command := `!count=$(cat "` + counter + `"); echo $((count + 1)) > "` + counter + `"; exit 1`
		if got := ResolveUncached(command, nil); got != "" {
			t.Fatalf("failed configured command = %q, want undefined", got)
		}
		assertConfigCommandCounter(t, counter, "1")
	})

	t.Run("a configured shell that cannot start falls back to the default shell", func(t *testing.T) {
		useConfiguredShell(t, func() (shellconfig.Config, error) {
			return shellconfig.Config{Path: filepath.Join(t.TempDir(), "missing-bash"), Args: []string{"-c"}}, nil
		})
		if got := ResolveUncached("!echo fallback", nil); got != "fallback" {
			t.Fatalf("missing configured shell = %q, want default-shell output", got)
		}
	})

	t.Run("a shell configuration error falls back to the default shell", func(t *testing.T) {
		useConfiguredShell(t, func() (shellconfig.Config, error) {
			return shellconfig.Config{}, errors.New("no bash found")
		})
		if got := ResolveUncached("!echo fallback", nil); got != "fallback" {
			t.Fatalf("shell configuration error = %q, want default-shell output", got)
		}
	})
}
