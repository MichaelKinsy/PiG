package codingagent

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

// anonymousFile creates a file in dir that Windows deletes when its handle closes, also when the process ends.
func anonymousFile(dir string) (*os.File, error) {
	//portlint:allow clock the time only makes the snapshot file name unique (CREATE_NEW fails on a clash); no time value reaches Pi-facing output
	name := filepath.Join(dir, ".pig-snapshot-"+strconv.FormatInt(time.Now().UnixNano(), 36)+"-"+strconv.Itoa(os.Getpid()))
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_TEMPORARY|windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(handle), name), nil
}

// cloneFile reports that the snapshot is a copy: block cloning (ReFS) is not used.
func cloneFile(*os.File, string) (*os.File, bool) { return nil, false }

// snapshotDir is where a snapshot of the session file at path is created. A Windows snapshot is a copy, never a clone, so nothing gains
// from placing it beside the session file, and the file is listed in its directory while the handle is open: the temporary directory
// keeps the session directory to the files Pi writes there.
func snapshotDir(string) string { return os.TempDir() }
