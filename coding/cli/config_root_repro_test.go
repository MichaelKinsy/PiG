package cli

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// With no PIG_HOME, no XDG_CONFIG_HOME and no home directory, the CLI reports an error and writes nothing: it never falls back to a path relative to the
// working directory. A command that needs no config root still runs.
func TestCLIWithoutAHomeDirectoryReportsAnErrorInsteadOfWritingARelativePath(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	cwd := t.TempDir()
	run := func(args ...string) (string, int) {
		cmd := exec.CommandContext(t.Context(), bin, args...)
		cmd.Dir, cmd.Env = cwd, []string{"PATH=" + os.Getenv("PATH"), "PIG_OFFLINE=1", "PI_OFFLINE=1"}
		out, err := cmd.CombinedOutput()
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return string(out), exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}
	out, code := run("docs", "path")
	if code != 1 || !strings.Contains(out, "locate home directory") || strings.Contains(out, "panic") {
		t.Fatalf("pig docs path without a home directory: exit %d, output %q; want exit 1 and the error", code, out)
	}
	out, code = run("--print", "hi")
	if code != 1 || !strings.Contains(out, "cannot resolve PiG's config directories") || strings.Contains(out, "panic") {
		t.Fatalf("pig --print without a home directory: exit %d, output %q; want exit 1 and the error", code, out)
	}
	if out, code := run("--version"); code != 0 || !strings.Contains(out, UpstreamPackageVersion) {
		t.Fatalf("pig --version without a home directory: exit %d, output %q", code, out)
	}
	if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 0 {
		t.Fatalf("the CLI wrote into the working directory: %v, %v", entries, err)
	}
}
