// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (deleteSessionFile).
package codingagent

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

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

// trashSystemErrorCode is libuv's code for the Windows error, which internal/nodeerrno translates as uv_translate_sys_error does.
func trashSystemErrorCode(err error) string { return tools.NodeErrorCode(err) }

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
