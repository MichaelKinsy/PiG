package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// strippedTestBinary is cmd/pig built with every Piglet strip build tag, the
// most a Piglet Binary can compile out (D92).
var strippedTestBinary = sync.OnceValues(func() (string, error) {
	binDir := filepath.Join(fixtureRoot, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return "", err
	}
	out := testExecutable(filepath.Join(binDir, "pig-stripped"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-tags", strings.Join(piglet.StripBuildTags(), ","), "-o", out, ".")
	cmd.Dir = filepath.Join(fixtureSourceRoot, "cmd", "pig")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build pig-stripped: %w\n%s", err, data)
	}
	return out, nil
})

// runStrippedPig runs the fully stripped pig with an isolated home and the
// faux provider, and returns its stdout, stderr and exit code.
func runStrippedPig(t *testing.T, cwd string, args ...string) (string, string, int) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a stripped cmd/pig")
	}
	binary, err := strippedTestBinary()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cwd == "" {
		cwd = t.TempDir()
	}
	cmd := exec.CommandContext(testbudget.Context(t), binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "HOME="+home, "PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// goListCmdPig runs `go list [-tags tag] <format args> cmd/pig` and returns its output fields.
func goListCmdPig(t *testing.T, tag string, format ...string) []string {
	t.Helper()
	return goListPackage(t, "github.com/MichaelKinsy/PiG/cmd/pig", tag, format...)
}

// goListPackage is `go list` of one package, with tag as its build tags when tag is not empty.
func goListPackage(t *testing.T, pkg, tag string, format ...string) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("go list of " + pkg + " in -short mode")
	}
	args := []string{"list"}
	if tag != "" {
		args = append(args, "-tags", tag)
	}
	args = append(append(args, format...), pkg)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	return strings.Fields(strings.NewReplacer("[", " ", "]", " ").Replace(string(out)))
}
