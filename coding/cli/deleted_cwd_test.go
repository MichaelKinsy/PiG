package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

const deletedCwdChildEnv = "PIG_TEST_DELETED_CWD_CHILD"

// Pi 88ffcdc7 (#11509) test/deleted-cwd.test.ts: main() catches ENOENT from process.cwd(), prints one line telling the user to change to an existing directory, and exits 1.
// The child deletes its own working directory, then runs the command line; --help is handled after the cwd read, as Pi's --version is.
func TestStartupInADeletedWorkingDirectoryReportsItAndExits1(t *testing.T) {
	if os.Getenv(deletedCwdChildEnv) == "1" {
		dir, err := os.MkdirTemp("", "deleted-cwd")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		Main([]string{"--no-extensions", "--help"}, nil)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot remove a process's working directory")
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStartupInADeletedWorkingDirectoryReportsItAndExits1$")
	command.Env = append(os.Environ(), deletedCwdChildEnv+"=1", "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir(), "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("exit = %v, want exit status 1\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	const want = "Error: The current working directory no longer exists. Change to an existing directory and try again."
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", &stderr, want)
	}
}

// Pi rethrows any cwd failure other than ENOENT unchanged; pig reports it with the operating system's own message.
func TestWorkingDirectoryErrorMessageNamesOnlyAMissingDirectory(t *testing.T) {
	if got, want := workingDirectoryErrorMessage(syscall.ENOENT), "The current working directory no longer exists. Change to an existing directory and try again."; got != want {
		t.Errorf("ENOENT message = %q, want %q", got, want)
	}
	if got, want := workingDirectoryErrorMessage(syscall.EACCES), "get cwd: permission denied"; got != want {
		t.Errorf("EACCES message = %q, want %q", got, want)
	}
}
