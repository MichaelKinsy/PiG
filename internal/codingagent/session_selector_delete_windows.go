// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (deleteSessionFile).
package codingagent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// lookTrash resolves `trash` as libuv's Windows spawn search does: each PATH directory in order, trying trash.com and then trash.exe. It does not implicitly search the current directory or run batch files. An explicit relative PATH entry is resolved before passing it to Go's executable lookup.
func lookTrash() (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, name := range []string{"trash.com", "trash.exe"} {
			candidate, err := filepath.Abs(filepath.Join(dir, name))
			if err != nil {
				return "", err
			}
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

func runTrash(ctx context.Context, args []string, output *trashOutput) (*exec.Cmd, error) {
	path, err := lookTrash()
	if err != nil {
		return nil, err
	}
	return runTrashCommand(ctx, path, args, output)
}

func trashSystemErrorCode(err error) string { return tools.NodeErrorCode(nodeErrno(err)) }

func terminateTrash(process *os.Process) error { return process.Kill() }

// unlinkSessionFile is libuv's Windows unlink: a directory that is not a symbolic link or junction fails with ERROR_ACCESS_DENIED, which Node reports as EPERM, and a read-only file is still removed.
func unlinkSessionFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
		return &os.PathError{Op: "unlink", Path: path, Err: windows.ERROR_ACCESS_DENIED}
	}
	return os.Remove(path)
}

// windowsNodeErrno is libuv's uv_translate_sys_error for the Windows errors a trash spawn or unlink reports, expressed as the errno values Node names.
var windowsNodeErrno = map[syscall.Errno]syscall.Errno{
	windows.ERROR_FILE_NOT_FOUND:       syscall.ENOENT,
	windows.ERROR_PATH_NOT_FOUND:       syscall.ENOENT,
	windows.ERROR_INVALID_NAME:         syscall.ENOENT,
	windows.ERROR_BAD_PATHNAME:         syscall.ENOENT,
	windows.ERROR_ACCESS_DENIED:        syscall.EPERM,
	windows.ERROR_PRIVILEGE_NOT_HELD:   syscall.EPERM,
	windows.ERROR_SHARING_VIOLATION:    syscall.EBUSY,
	windows.ERROR_LOCK_VIOLATION:       syscall.EBUSY,
	windows.ERROR_WRITE_PROTECT:        syscall.EROFS,
	windows.ERROR_FILENAME_EXCED_RANGE: syscall.ENAMETOOLONG,
}

// nodeErrno translates a Windows error to the errno Node reports for it, or returns err unchanged.
func nodeErrno(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if translated, ok := windowsNodeErrno[errno]; ok {
			return translated
		}
	}
	return err
}
