//go:build linux || darwin

package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// accessACL is the attribute that holds the access ACL of a file on Linux.
const accessACL = "system.posix_acl_access"

// listAttributes names the extended attributes of the file at path; tests inject a file system that keeps none.
var listAttributes = listXattrs

// getxattr is getxattr(2), which gives the size of an attribute when called with no buffer; tests inject an attribute that grows between two calls.
var getxattr = unix.Getxattr

// carryAttributes makes the extended attributes of temp those of the file at from: on Linux they carry the POSIX ACLs (system.posix_acl_access) and the
// security labels, on macOS the user's own attributes. A file system that keeps no attributes has none to copy; any other failure to list, read or
// set one is returned, because dropping an ACL silently is worse than writing the file in place. An attribute the new file already has with the same
// value (a security label the file system assigns by itself) is not set again. The access ACL the new file inherited from its directory's default ACL
// is removed where the file it replaces has none: the replacement has exactly the access of the original.
func carryAttributes(temp *os.File, from string) error {
	names, err := listAttributes(from)
	if err != nil {
		if unsupported(err) {
			return nil
		}
		return err
	}
	// A default ACL on the directory gives the temporary file an access ACL at creation: its named entries, once chmod sets the mask to the group bits,
	// grant what the replaced file never granted. A file without an access ACL has none to keep, so the replacement must have none either.
	if runtime.GOOS == "linux" && !slices.Contains(names, accessACL) {
		if err := unix.Removexattr(temp.Name(), accessACL); err != nil && !errors.Is(err, unix.ENODATA) && !unsupported(err) {
			return fmt.Errorf("%s: %w", accessACL, err)
		}
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
		size, err := getxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size)
		n, err := getxattr(path, name, buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// An empty buffer is a size query again: an attribute that grew from nothing since the first call answers its new size, not ERANGE.
		if n > len(buf) {
			continue
		}
		return buf[:n], nil
	}
	return nil, unix.ERANGE
}
