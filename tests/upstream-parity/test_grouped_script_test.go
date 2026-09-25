package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestGroupedTestsPropagateGoListFailure(t *testing.T) {
	root := pigRepoRoot(t)
	fakeBin := t.TempDir()
	writeExecutable(t, filepath.Join(fakeBin, "go"), "#!/bin/sh\nexit 17\n")

	cmd := exec.Command(testenv.Bash(t), filepath.Join(root, "automation", "ci", "test-grouped.sh"), "report")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	if err := cmd.Run(); exitCode(err) != 17 {
		t.Fatalf("go list failure exit = %v, want 17", err)
	}
}

func TestGroupedTestsPropagateFixtureBuildFailure(t *testing.T) {
	root := pigRepoRoot(t)
	fakeBin := t.TempDir()
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/bin/sh
case "$1" in
  list)
    case "$*" in
      *"./..."*) echo github.com/MichaelKinsy/PiG/fake ;;
      *)
        echo github.com/MichaelKinsy/PiG/coding/extension/host/subprocess
        echo github.com/MichaelKinsy/PiG/tests/extension-conformance
        ;;
    esac
    exit 0
    ;;
  build) exit 23 ;;
  test) exit 0 ;;
esac
exit 24
`)

	cmd := exec.Command(testenv.Bash(t), filepath.Join(root, "automation", "ci", "test-grouped.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	if err := cmd.Run(); exitCode(err) != 23 {
		t.Fatalf("fixture build failure exit = %v, want 23", err)
	}
}

func pigRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}
