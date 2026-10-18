package ai

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory and the agent-directory variables (testenv.ScopeTempDir), so a test, or a Pi oracle child it starts, never
// reads or writes the credentials and settings of the user running `go test`. Its `!command` credentials run without the ten-second timeout
// (shellWithoutTimeout).
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "pig-ai-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create scoped test temp directory:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		os.Exit(2)
	}
	if runtime.GOOS != "windows" {
		configvalue.SetExecutorForTest(shellWithoutTimeout)
	}
	code := testenv.RunWithoutGoroutineLeaks(m)
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, "remove scoped test temp directory:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}

// shellWithoutTimeout runs a `!command` credential as the default executor does (/bin/sh -c, trimmed stdout; a failed exit or no output is no value) and waits for
// it for as long as it runs. The default gives the command ten seconds, as Pi's execSync does, and a host that is slow to start a shell (59 of 90 starts took longer
// than ten seconds, 17.5 s at the median, with 14 CPU burners and six tests at a time) leaves the credential without a key: no test here is about that bound, and
// the credential tests that compare with Pi's oracle would then differ in what each side's host happened to manage.
func shellWithoutTimeout(ctx context.Context, payload string) (string, bool) {
	out, err := exec.CommandContext(context.WithoutCancel(ctx), "/bin/sh", "-c", payload).Output()
	value := strings.TrimSpace(string(out))
	if err != nil || value == "" {
		return "", false
	}
	return value, true
}

// piCommandsWithoutTimeout is the same for Pi's oracle: execSync of a `!command` credential loses its ten-second timeout. It runs before Pi's auth storage is loaded.
const piCommandsWithoutTimeout = `
const childProcess = (await import("node:child_process")).default;
const { syncBuiltinESMExports } = await import("node:module");
const execSync = childProcess.execSync;
childProcess.execSync = (command, options) => execSync(command, { ...options, timeout: undefined });
syncBuiltinESMExports();
`
