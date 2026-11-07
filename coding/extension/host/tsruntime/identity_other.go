//go:build !(linux || openbsd || dragonfly || solaris || darwin || freebsd || netbsd)

package tsruntime

import (
	"errors"
	"os"
)

// identify reports that this platform has no file identity the digest cache trusts: Verify hashes the executable at every start.
func identify(os.FileInfo) (fileIdentity, error) {
	return fileIdentity{}, errors.New("no file identity on this platform")
}

// ownedByCurrentUser is false: no identity is trusted here, so no cache file is read.
func ownedByCurrentUser(os.FileInfo) bool { return false }

// openRecord opens the digest record. No record is read here, because no identity is trusted.
func openRecord(path string) (*os.File, error) { return os.Open(path) }

// unlinked is false: no identity is trusted here.
func unlinked(os.FileInfo) bool { return false }
