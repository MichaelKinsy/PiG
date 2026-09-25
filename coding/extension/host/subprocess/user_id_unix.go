//go:build !windows

package subprocess

import "os"

func currentUserID() int { return os.Getuid() }
