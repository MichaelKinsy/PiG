//go:build parity

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// A helper script of a scenario finds Pi through PIG_PARITY_PI_BIN. When the run did not set it, the runner's own resolved oracle must reach the child.
func TestResolveUpstreamPiBinExportsTheResolvedOracleToTheChild(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "extensions", "sdk-ts", "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extensions", "sdk-ts", "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "pi"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := `{"commit":"` + coding.UpstreamCommit + `","source":"build"}`
	if err := os.WriteFile(filepath.Join(binDir, ".pig-pi-oracle.json"), []byte(stamp), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PIG_PARITY_PI_BIN", "")
	t.Setenv("PIG_PARITY_REAL_AUTH", "")
	t.Setenv("PIG_PARITY_PI_PUBLISHED", "")
	t.Chdir(root)

	got := ResolveUpstreamPiBin(t)
	if !slices.Contains(got.Env, "PIG_PARITY_PI_BIN="+got.Path) {
		t.Fatalf("env %v lacks PIG_PARITY_PI_BIN=%s: a helper script would not find the oracle the runner resolved", got.Env, got.Path)
	}
}

func TestSuspendScriptNamesTheVariableWhenNoPiIsFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper is a bash script")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dirname, err := exec.LookPath("dirname")
	if err != nil {
		t.Skip("no dirname")
	}
	// A PATH that holds only what the script needs before it looks for Pi.
	pathDir := t.TempDir()
	if err := os.Symlink(dirname, filepath.Join(pathDir, "dirname")); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "testdata", "suspend-session-native.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bash, script, "pi")
	command.Env = []string{"PATH=" + pathDir, "HOME=" + t.TempDir()}
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("the script succeeded without a Pi: %s", out)
	}
	if got := strings.TrimSpace(string(out)); got != "suspend-session-native.sh: no Pi oracle: set PIG_PARITY_PI_BIN or put pi on PATH" {
		t.Fatalf("output %q, want the one-line message naming PIG_PARITY_PI_BIN", got)
	}
}
