//go:build !windows

package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

const deletedCwdChildEnv = "PIG_TEST_DELETED_CWD_CHILD"

// deletedCwdMessage is the line Pi's main() prints when process.cwd() throws ENOENT (main.ts, 88ffcdc7, #11511).
const deletedCwdMessage = "Error: The current working directory no longer exists. Change to an existing directory and try again."

// Ports packages/coding-agent/test/deleted-cwd.test.ts: a deleted working directory is reported in one line with exit code 1, for
// --version and --help too, where Pi (and pig) used to crash or carry on without a directory. The child removes its own working directory
// and runs the real command line.
func TestMainReportsDeletedWorkingDirectory(t *testing.T) {
	if args, ok := os.LookupEnv(deletedCwdChildEnv); ok {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cwd); err != nil {
			t.Fatal(err)
		}
		Main(strings.Fields(args), nil)
		return
	}
	for _, args := range []string{"--version", "--help", "--no-extensions -p hello"} {
		t.Run(args, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainReportsDeletedWorkingDirectory$")
			root := t.TempDir()
			command.Env = append(os.Environ(), "PIG_HOME="+root+"/pighome", "PIG_CODING_AGENT_DIR="+root+"/agent", "PIG_OFFLINE=1", deletedCwdChildEnv+"="+args)
			command.Dir = t.TempDir()
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("exit = %v, want exit status 1\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if !strings.Contains(stderr.String(), deletedCwdMessage) {
				t.Errorf("stderr = %q, want it to carry %q", &stderr, deletedCwdMessage)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", &stdout)
			}
		})
	}
}

const cwdErrorChildEnv = "PIG_TEST_CWD_ERROR_CHILD"

// Ports packages/coding-agent/test/deleted-cwd.test.ts as Pi writes it, stubbing process.cwd() (here processCwd) to fail:
// "prints an actionable error and exits 1 instead of throwing" for ENOENT and "rethrows unrelated cwd errors" for EACCES. Pi's main()
// lets the EACCES error escape as an uncaught exception, which ends the process with its message and a non-zero status; pig reports it
// as `Error: get cwd: ...` and exits 1, without the deleted-directory message.
func TestMainHandlesCwdErrors(t *testing.T) {
	if name, ok := os.LookupEnv(cwdErrorChildEnv); ok {
		errno := map[string]syscall.Errno{"ENOENT": syscall.ENOENT, "EACCES": syscall.EACCES}[name]
		processCwd = func() (string, error) { return "", &fs.PathError{Op: "uv_cwd", Path: ".", Err: errno} }
		Main([]string{"--no-extensions", "--version"}, nil)
		return
	}
	for _, tc := range []struct {
		errno       string
		wantMessage string
		deleted     bool
	}{
		{"ENOENT", deletedCwdMessage, true},
		{"EACCES", "get cwd: uv_cwd .: permission denied", false},
	} {
		t.Run(tc.errno, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainHandlesCwdErrors$")
			root := t.TempDir()
			command.Env = append(os.Environ(), "PIG_HOME="+root+"/pighome", "PIG_CODING_AGENT_DIR="+root+"/agent", "PIG_OFFLINE=1", cwdErrorChildEnv+"="+tc.errno)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("exit = %v, want exit status 1\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if !strings.Contains(stderr.String(), tc.wantMessage) {
				t.Errorf("stderr = %q, want it to carry %q", &stderr, tc.wantMessage)
			}
			if got := strings.Contains(stderr.String(), deletedCwdMessage); got != tc.deleted {
				t.Errorf("deleted-directory message printed = %v, want %v: %q", got, tc.deleted, &stderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", &stdout)
			}
		})
	}
}
