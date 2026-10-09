// Package nodefs ports the directory-order contract of Node's fs.readdirSync.
package nodefs

import "os"

// ReadDir returns the entries of the directory named by name in the order Node's fs.readdirSync returns them. libuv's scandir sorts them
// by name (strcmp) on Unix-like systems and returns them in the file system's own order on Windows, which on NTFS ignores case. On an
// error it returns the entries read before it, as os.ReadDir does.
func ReadDir(name string) ([]os.DirEntry, error) {
	return readDir(name)
}
