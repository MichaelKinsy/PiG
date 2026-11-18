//go:build !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_python && !pig_strip_extension_sdk_rust

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigdocs"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Issue #200: with XDG_CONFIG_HOME set, no PIG_HOME and Pi-sharing mode (PIG_USE_PI_DIRS=1), a fresh CLI writes its documentation bundle (before
// its first tool call) and stages the extension SDKs (`pig reload`, or before its first extension) under $XDG_CONFIG_HOME/pig, never under ~/.pig. Pi-sharing mode moves only the agent directory (PI_CODING_AGENT_DIR) and the
// project directory (D2). The staged Python SDK then names the same root through Context.config_home, where it used to answer ~/.pig.
func TestFreshCLIInPiSharingModeUsesTheXDGConfigRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, xdg, piAgent, cwd := filepath.Join(root, "home"), filepath.Join(root, "xdg"), filepath.Join(root, "pi-agent"), filepath.Join(root, "cwd")
	for _, dir := range []string{home, xdg, piAgent, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + xdg,
		"PIG_USE_PI_DIRS=1", "PI_CODING_AGENT_DIR=" + piAgent,
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "PI_OFFLINE=1",
	}
	ctx := testbudget.Context(t)
	// A run writes the documentation bundle before its first tool call, and `pig reload` stages the SDKs an extension load would stage.
	for _, args := range [][]string{
		{"--model", "test-faux/faux-1", "--no-extensions", "--tools", "ls", "--print", "Run: ls here"},
		{"reload"},
	} {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Env = cwd, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pig %q: %v\n%s", args, err, out)
		}
	}

	pigRoot := filepath.Join(xdg, "pig")
	for _, want := range []string{
		filepath.Join(pigdocs.DocsDir(pigRoot), "README.md"),
		filepath.Join(stagedSDKDir(t, pigRoot, "go"), "go.mod"),
		filepath.Join(stagedSDKDir(t, pigRoot, "python"), "pig_sdk", "__init__.py"),
		filepath.Join(stagedSDKDir(t, pigRoot, "rust"), "Cargo.toml"),
		filepath.Join(piAgent, "auth.json"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("fresh CLI did not write %s: %v", want, err)
		}
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("the fresh CLI wrote into HOME: %v, %v", entries, err)
	}

	// sys.stdout.write, not print: on Windows Python's text-mode stdout turns print's "\n" into "\r\n", which is the console's line ending and
	// not part of config_home.
	py := exec.CommandContext(ctx, "python3", "-c", "import sys, pig_sdk; sys.stdout.write(pig_sdk.Context(extension=None).config_home)")
	py.Env = append(slices.Clone(env), "PYTHONPATH="+stagedSDKDir(t, pigRoot, "python"), "PYTHONDONTWRITEBYTECODE=1")
	out, err := py.Output()
	if err != nil {
		t.Fatalf("python SDK: %v", err)
	}
	if got := string(out); got != pigRoot {
		t.Errorf("the staged Python SDK's config_home = %q, want %q", got, pigRoot)
	}
}
