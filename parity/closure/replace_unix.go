//go:build !windows

package closure

import "os"

func publishStore(from, to string) error {
	return os.Rename(from, to)
}
