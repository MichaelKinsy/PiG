//go:build !unix

package host

import (
	"errors"
	"os"
)

func openNoFollow(path string) (*os.File, error) { return os.OpenFile(path, os.O_RDONLY, 0) }

func createExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func suspendThisProcess() error { return errors.New("suspend is not supported on this platform") }
