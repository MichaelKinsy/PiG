//go:build windows

package daemon

// Ports packages/env/daemon/src/sys/windows.rs

import (
	"io/fs"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const pathSeparator = `\`

// pathSeparators are the characters that separate path components.
const pathSeparators = `\/`

const windowsOS = true

// openReadOnly opens path for reading like libuv: it shares read, write and delete access, so the file can be renamed
// or removed while open, and it opens directories, which the caller then refuses. Windows has no O_NOFOLLOW, so the
// caller checks the final component itself.
func openReadOnly(path string, _ bool) (*os.File, error) { return openForRead(path) }

// isLinkRefusal is never true: Windows has no O_NOFOLLOW.
func isLinkRefusal(error) bool { return false }

// openForRead is Node's open(path, "r") through CreateFileW, as libuv's fs__open.
func openForRead(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// identity is the creation time of the file or directory, which tells a replaced one apart; reading a file index needs
// a handle per file.
func identity(_ string, info os.FileInfo, _ bool) (dev, ino uint64) {
	if attributes, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return 0, uint64(attributes.CreationTime.Nanoseconds())
	}
	return 0, 0
}

// tmpdir is Node's os.tmpdir() on Windows.
func tmpdir() string {
	variable := func(key string) string { return os.Getenv(key) }
	path := variable("TEMP")
	if path == "" {
		path = variable("TMP")
	}
	if path == "" {
		root := variable("SystemRoot")
		if root == "" {
			root = variable("windir")
		}
		path = root + `\temp`
	}
	if len(path) > 1 && strings.HasSuffix(path, `\`) && !strings.HasSuffix(path, `:\`) {
		path = path[:len(path)-1]
	}
	return path
}

// home is libuv's uv_os_homedir: USERPROFILE first.
func home() string { return os.Getenv("USERPROFILE") }

// driveCwds is the per-drive working directories of the `=C:` variables, which Node's path.resolve uses for
// drive-relative paths.
func driveCwds() Object {
	result := Object{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry[min(1, len(entry)):], "=")
		if !ok || entry[0] != '=' || len(key) != 2 || key[1] != ':' || !isASCIILetter(key[0]) {
			continue
		}
		result[strings.ToUpper(key)] = value
	}
	return result
}

func isASCIILetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// rawName is nil: Windows listings keep the file system's order and nothing sorts by raw names.
func rawName(string) []int { return nil }
