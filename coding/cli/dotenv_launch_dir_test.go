package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream 1ffb6bd6 (#10473) builds the standalone binary with --no-compile-autoload-dotenv: `.env`, `.env.local` and
// `.env.development` in the launch directory never reach the process environment. A Go binary reads no such file; the
// probe is PIG_TEST_FAUX, which makes the test-faux provider resolvable only when it is in the environment.
func TestLaunchDirectoryEnvFilesAreNotLoaded(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	run := func(t *testing.T, envFiles bool, env ...string) (string, int) {
		t.Helper()
		agentDir, cwd := t.TempDir(), t.TempDir()
		if envFiles {
			for _, name := range []string{".env", ".env.local", ".env.development"} {
				if err := os.WriteFile(filepath.Join(cwd, name), []byte("PIG_TEST_FAUX=1\nPIG_TEST_FAUX_SCENARIO=parity-basic\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--model", "test-faux/faux-1", "--no-extensions", "--print", "reply with exactly: dotenv-ok")
		cmd.Dir = cwd
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PIG_TEST_FAUX") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PIG_HOME="+t.TempDir(), "PI_HOME="+t.TempDir(), "HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir)
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.Output()
		code := 0
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}

	// The probe is live: the variable in the environment selects the faux provider.
	if out, code := run(t, false, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"); code != 0 || out != "dotenv-ok\n" {
		t.Fatalf("control run: exit %d, stdout %q; want the faux reply", code, out)
	}
	// The same variables in the three launch-directory env files are ignored.
	if out, code := run(t, true); code == 0 || strings.Contains(out, "dotenv-ok") {
		t.Fatalf("env-file run: exit %d, stdout %q; the launch directory's env files were loaded", code, out)
	}
}
