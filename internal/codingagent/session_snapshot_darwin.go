package codingagent

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// anonymousFile creates a file in dir and removes its name at once.
func anonymousFile(dir string) (*os.File, error) { return unlinkedTemp(dir) }

// cloneFile returns a file beside path that shares the blocks of src, the file opened from path (APFS clonefile), with its name removed at once.
func cloneFile(src *os.File, path string) (*os.File, bool) {
	//portlint:allow clock the time is a unique-name nonce for a file removed at once, not a clock reading a test controls
	name := filepath.Join(filepath.Dir(path), ".pig-snapshot-"+strconv.FormatInt(time.Now().UnixNano(), 36)+"-"+strconv.Itoa(os.Getpid()))
	if unix.Fclonefileat(int(src.Fd()), unix.AT_FDCWD, name, 0) != nil {
		return nil, false
	}
	file, err := os.OpenFile(name, os.O_RDWR, 0) //nolint:gosec // G304: the path names an output or state file that the user or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so writing it grants nothing the user lacks
	_ = os.Remove(name)
	if err != nil {
		return nil, false
	}
	return file, true
}
