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
