package coding

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/testenv"

	// A binary that embeds the documentation bundle registers its name; the system prompt names the docs only then (D22).
	_ "github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// shellWithoutTimeout runs a `!command` credential as the default executor does (/bin/sh -c, trimmed stdout; a failed exit or no output is no value) and waits for
// it for as long as it runs. The default gives the command ten seconds, as Pi's execSync does (resolve-config-value.ts, timeout: 10000), and on an overloaded host a
// shell that starts late leaves the credential without a key: no test here is about that bound, and the registry oracle would then compare what each side's host
// happened to manage (the same executor as ai's TestMain).
func shellWithoutTimeout(ctx context.Context, payload string) (string, bool) {
	out, err := exec.CommandContext(context.WithoutCancel(ctx), "/bin/sh", "-c", payload).Output()
	value := strings.TrimSpace(string(out))
	if err != nil || value == "" {
		return "", false
	}
	return value, true
}

func TestMain(m *testing.M) {
	if runtime.GOOS != "windows" {
		configvalue.SetExecutorForTest(shellWithoutTimeout)
	}
	testRoot, err := os.MkdirTemp("", "pig-coding-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test config root:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PIG_CODING_AGENT_SESSION_DIR"} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "clear inherited %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	if err := os.Setenv("PIG_HOME", filepath.Join(testRoot, "pig")); err != nil {
		fmt.Fprintln(os.Stderr, "set isolated PIG_HOME:", err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	code := testenv.RunWithoutGoroutineLeaks(m)
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test config root:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}
