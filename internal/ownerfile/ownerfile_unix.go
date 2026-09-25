//go:build !windows

package ownerfile

import "os"

// CreateNew creates the file at path, which must not exist, for reading and
// writing by its owner only (mode 0600) from the moment it exists.
func CreateNew(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// CreateTemp creates a new temporary file in dir, as os.CreateTemp does,
// which already creates it with mode 0600.
func CreateTemp(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}

// OwnerOnly reports whether info grants no access to the group or to others.
func OwnerOnly(_ string, info os.FileInfo) (bool, error) {
	return info.Mode()&0o077 == 0, nil
}
