package nodefs

import "golang.org/x/sys/windows"

// Access is fs.accessSync(path, F_OK) and reports whether it succeeds. libuv's fs__access asks GetFileAttributesW for the
// path's own attributes and nothing else, so a runnable Microsoft Store alias (an app execution link under
// %LOCALAPPDATA%\Microsoft\WindowsApps, nodejs/node#36790) passes here although [Exists], whose follow-up stat of the
// target fails for it, does not.
//
// Ports packages/coding-agent/src/utils/shell.ts (the accessSync(path, F_OK) existence check of shellPathExists).
func Access(name string) bool {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	_, err = windows.GetFileAttributes(path)
	return err == nil
}
