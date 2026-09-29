//go:build windows

package codingagent

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// evalCanonicalPath keeps volume mount points in the path and resolves a rooted link target such as \dir on the device of the path that reached the link, as Node's realpathSync does. winsymlink=0 makes Go follow both drive junctions and volume mounts, but libuv treats only drive-target mount-point reparse records as symlinks. filepath.EvalSymlinks instead follows a rooted target on the process drive, so it can end at the wrong file or drop the volume. Ordinary paths retain the standard library's resolution.
// Ports packages/coding-agent/src/utils/paths.ts:canonicalizePath.
func evalCanonicalPath(path string) (string, error) {
	return evalCanonicalPathWith(path, walkCanonicalPath, filepath.EvalSymlinks)
}

func walkCanonicalPath(path string) (string, bool, error) {
	return nodeRealpathWin32(path, osRealpathFS, win32Process{
		cwd: func() string {
			cwd, _ := os.Getwd()
			return cwd
		},
		getenv: os.Getenv,
	})
}

// osRealpathFS reads the filesystem with libuv's link rule. Stat before reading a link rejects dangling and cyclic links just as Node's realpathSync does. A volume-GUID mount point is a directory, so it stays in the path.
var osRealpathFS = nodeRealpathFS{
	lstat: func(path string) (nodeEntryKind, string, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nodeEntryPlain, "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nodeEntryPlain, "", nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return nodeEntryPlain, "", err
		}
		if isVolumeGUIDPath(target) {
			volumeMount, err := isMountPoint(path)
			if err != nil {
				return nodeEntryPlain, "", err
			}
			if volumeMount {
				return nodeEntryMount, "", nil
			}
		}
		return nodeEntryLink, target, nil
	},
	stat: func(path string) error {
		_, err := os.Stat(path)
		return err
	},
}

func isVolumeGUIDPath(path string) bool {
	return strings.HasPrefix(strings.ToLower(path), `\\?\volume{`)
}

// A symbolic link can also name a volume GUID. Only a mount-point reparse record has Node's directory semantics.
func isMountPoint(path string) (bool, error) {
	// FindFirstFile needs an extended-length path even though os.Lstat and os.Readlink already handle long paths themselves.
	if !strings.HasPrefix(path, `\\?\`) {
		if unc, ok := strings.CutPrefix(path, `\\`); ok {
			path = `\\?\UNC\` + unc
		} else {
			path = `\\?\` + path
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(name, &data)
	if err != nil {
		return false, err
	}
	err = windows.FindClose(handle)
	return data.Reserved0 == windows.IO_REPARSE_TAG_MOUNT_POINT, err
}
