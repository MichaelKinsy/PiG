//go:build !windows

package realpath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// EvalSymlinks is filepath.EvalSymlinks. An absolute path, the case of a working directory, takes walk, which lstats the
// same components in the same order without a FileInfo or a new path string per component.
func EvalSymlinks(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return filepath.EvalSymlinks(path)
	}
	return walk(path)
}

// walk is path/filepath's walkSymlinks for an absolute Unix path, with the result built in one buffer: the same components
// lstatted in the same order, the same lexical "..", the same link limit, and Clean of the result.
func walk(path string) (string, error) {
	volLen := 0
	if volLen < len(path) && path[volLen] == '/' {
		volLen++
	}
	vol := path[:volLen]
	dest := append(make([]byte, 0, len(path)), vol...)
	linksWalked := 0
	var st unix.Stat_t
	var end int
	for start := volLen; start < len(path); start = end {
		for start < len(path) && path[start] == '/' {
			start++
		}
		end = start
		for end < len(path) && path[end] != '/' {
			end++
		}
		// The next path component is in path[start:end].
		if end == start {
			// No more path components.
			break
		}
		name := path[start:end]
		if name == "." {
			continue
		}
		if name == ".." {
			// Back up to the previous component if possible; volLen includes any leading slash.
			r := len(dest) - 1
			for ; r >= volLen; r-- {
				if dest[r] == '/' {
					break
				}
			}
			if r < volLen || string(dest[r+1:]) == ".." {
				// dest has no slash or ends in a ".." that had to be kept: keep this ".." too.
				if len(dest) > volLen {
					dest = append(dest, '/')
				}
				dest = append(dest, ".."...)
			} else {
				dest = dest[:r]
			}
			continue
		}
		if len(dest) > 0 && dest[len(dest)-1] != '/' {
			dest = append(dest, '/')
		}
		dest = append(dest, name...)
		if err := lstatInPlace(dest, &st); err != nil {
			return "", &fs.PathError{Op: "lstat", Path: string(dest), Err: err}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFLNK {
			if st.Mode&unix.S_IFMT != unix.S_IFDIR && end < len(path) {
				return "", syscall.ENOTDIR
			}
			continue
		}
		linksWalked++
		if linksWalked > 255 {
			return "", errors.New("EvalSymlinks: too many links")
		}
		link, err := os.Readlink(string(dest))
		if err != nil {
			return "", err
		}
		path = link + path[end:]
		if len(link) > 0 && link[0] == '/' {
			// A link to an absolute path starts over at the root.
			dest = append(dest[:0], '/')
			end = 1
			vol = "/"
			volLen = 1
		} else {
			// A link to a relative path replaces the last component of dest.
			r := len(dest) - 1
			for ; r >= volLen; r-- {
				if dest[r] == '/' {
					break
				}
			}
			if r < volLen {
				dest = append(dest[:0], vol...)
			} else {
				dest = dest[:r]
			}
			end = 0
		}
	}
	return filepath.Clean(string(dest)), nil
}

// lstatInPlace lstats the path in buf without a path string: unix.Lstat keeps no reference to its argument.
func lstatInPlace(buf []byte, st *unix.Stat_t) error {
	for {
		err := unix.Lstat(unsafe.String(unsafe.SliceData(buf), len(buf)), st) //nolint:gosec // G103: unix.Lstat keeps no reference to its argument, so the buffer is read in place without a copy
		if err != unix.EINTR {
			return err
		}
	}
}
