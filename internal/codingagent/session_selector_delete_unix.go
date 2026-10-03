//go:build !windows

// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (deleteSessionFile).
package codingagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// runTrash follows libuv's PATH search, including relative/empty PATH entries and retry after EACCES/ENOENT/ENOTDIR. On Linux libuv calls glibc execvp, which defaults PATH to CS_PATH, also retries after ESTALE/ENODEV/ETIMEDOUT, and runs executable text without a shebang through /bin/sh. On Darwin libuv emulates execvp with posix_spawn (src/unix/process.c uv__spawn_and_init_child_posix_spawn): PATH defaults to _PATH_DEFPATH and ENOEXEC is returned without a shell fallback.
func runTrash(ctx context.Context, args []string, output *trashOutput) (*exec.Cmd, error) {
	path, set := os.LookupEnv("PATH")
	if !set {
		path = "/usr/bin:/bin"
		if runtime.GOOS == "linux" {
			path = "/bin:/usr/bin"
		}
	}
	var denied error
	for dir := range strings.SplitSeq(path, string(os.PathListSeparator)) {
		candidate, err := filepath.Abs(filepath.Join(dir, "trash"))
		if err != nil {
			return nil, err
		}
		cmd, err := runTrashCommand(ctx, candidate, args, output)
		if cmd.Process != nil {
			return cmd, err
		}
		switch {
		case errors.Is(err, syscall.ENOEXEC) && runtime.GOOS != "darwin":
			return runTrashCommand(ctx, "/bin/sh", append([]string{candidate}, args...), output)
		case errors.Is(err, syscall.EACCES):
			denied = err
		case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ENOTDIR):
		case runtime.GOOS == "linux" && (errors.Is(err, syscall.ESTALE) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ETIMEDOUT)):
		default:
			return cmd, err
		}
	}
	if denied != nil {
		return nil, denied
	}
	return nil, exec.ErrNotFound
}

func trashSystemErrorCode(err error) string {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return unix.ErrnoName(errno)
	}
	return ""
}

func terminateTrash(process *os.Process) error { return process.Signal(syscall.SIGTERM) }

// unlinkSessionFile is Node's fs.promises.unlink: unlink(2), which never removes a directory.
func unlinkSessionFile(path string) error {
	if err := syscall.Unlink(path); err != nil {
		return &os.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}
