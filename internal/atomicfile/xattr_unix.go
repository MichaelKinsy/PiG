//go:build linux || darwin

package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// listAttributes names the extended attributes of the file at path; tests inject a file system that keeps none.
var listAttributes = listXattrs

// carryAttributes copies every extended attribute of the file at from onto temp: on Linux they carry the POSIX ACLs (system.posix_acl_access) and the
// security labels, on macOS the user's own attributes. A file system that keeps no attributes has none to copy; any other failure to list, read or
// set one is returned, because dropping an ACL silently is worse than writing the file in place. An attribute the new file already has with the same
// value (a security label the file system assigns by itself) is not set again.
func carryAttributes(temp *os.File, from string) error {
	names, err := listAttributes(from)
	if err != nil {
		if unsupported(err) {
			return nil
		}
		return err
	}
	for _, name := range names {
		value, err := getXattr(from, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if current, err := getXattr(temp.Name(), name); err == nil && bytes.Equal(current, value) {
			continue
		}
		if err := unix.Setxattr(temp.Name(), name, value, 0); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func unsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}

func listXattrs(path string) ([]string, error) {
	for range 4 {
		size, err := unix.Listxattr(path, nil)
		if err != nil || size == 0 {
			return nil, err
		}
		buf := make([]byte, size)
		n, err := unix.Listxattr(path, buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var names []string
		for name := range strings.SplitSeq(string(buf[:n]), "\x00") {
			if name != "" {
				names = append(names, name)
			}
		}
		return names, nil
	}
	return nil, unix.ERANGE
}

func getXattr(path, name string) ([]byte, error) {
	for range 4 {
		size, err := unix.Getxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size)
		n, err := unix.Getxattr(path, name, buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	return nil, unix.ERANGE
}
