package nodefs

import (
	"io"
	"os"
)

// OpenFile is fs.open(path, flags, mode): os.OpenFile, except that on macOS a FIFO is opened where no signal can interrupt the
// open. Node runs fs.open on a libuv worker thread, to which the signals a process receives (SIGCHLD, SIGWINCH and a runtime's
// own) do not go; the calling thread of a Go process takes them, and a signal that ends a blocking open of a FIFO makes macOS
// drop the open from the FIFO as the writer (or reader) it just matched is let through, which loses the writer's bytes. See
// [quietOpen].
func OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	if namesFIFO(name) {
		return quietOpen(name, flag, perm)
	}
	return os.OpenFile(name, flag, perm) //nolint:gosec // G304: the path names an output or state file that the user or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so writing it grants nothing the user lacks
}

// ReadFile is fs.readFile(path): os.ReadFile, with the open of [OpenFile].
func ReadFile(name string) ([]byte, error) {
	if !namesFIFO(name) {
		return os.ReadFile(name) //nolint:gosec // G304: the path names a file that the user, the project config, or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so reading it grants nothing the user lacks
	}
	file, err := quietOpen(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}
