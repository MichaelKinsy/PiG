package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
        echo github.com/MichaelKinsy/PiG/test/extension-conformance
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
	return testenv.ModuleRoot(t)
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

// changedGoTestLines runs `test-grouped.sh changed` with a fake go and a fake changed-packages.py selection that edits one package, and returns the go test
// command lines it ran. extraEnv entries are added to the environment.
func changedGoTestLines(t *testing.T, extraEnv ...string) []string {
	t.Helper()
	root := pigRepoRoot(t)
	fakeBin := t.TempDir()
	log := filepath.Join(fakeBin, "go-test.log")
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/bin/sh
case "$1" in
  list)
    case "$*" in
      *"./..."*) echo github.com/MichaelKinsy/PiG/fake ;;
    esac
    exit 0
    ;;
  test) echo "$*" >>"$FAKE_GO_LOG"; exit 0 ;;
esac
exit 0
`)
	writeExecutable(t, filepath.Join(fakeBin, "python3"), `#!/bin/sh
case "$1" in
  *changed-packages.py) echo "direct github.com/MichaelKinsy/PiG/fake"; echo "affected github.com/MichaelKinsy/PiG/fake" ;;
  *) exec `+pythonPath(t)+` "$@" ;;
esac
`)
	cmd := exec.Command(testenv.Bash(t), filepath.Join(root, "automation", "ci", "test-grouped.sh"), "changed")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + fakeBin + ":" + os.Getenv("PATH"), "FAKE_GO_LOG=" + log, "TEST_BASE=HEAD", "PIG_RACE_TIMEOUT="}, extraEnv...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test-grouped.sh changed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func pythonPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	return path
}

func TestGroupedChangedRaceRunCarriesTheRaceTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"default", nil, "-timeout 60m"},
		{"override", []string{"PIG_RACE_TIMEOUT=90m"}, "-timeout 90m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := changedGoTestLines(t, tc.env...)
			if len(lines) != 1 || !strings.Contains(lines[0], " -race ") || !strings.Contains(lines[0], tc.want+" ") {
				t.Fatalf("race run = %q, want -race and %q", lines, tc.want)
			}
		})
	}
}

func TestGroupedChangedPlainRunKeepsTheDefaultTimeout(t *testing.T) {
	lines := changedGoTestLines(t, "TEST_CHANGED_RACE=none", "PIG_RACE_TIMEOUT=90m")
	if len(lines) != 1 || strings.Contains(lines[0], "-race") || strings.Contains(lines[0], "-timeout") {
		t.Fatalf("plain run = %q, want neither -race nor -timeout", lines)
	}
}
