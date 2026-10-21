//go:build !linux && !windows && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package cli

import (
	"context"
	"errors"
)

func waitProcessExit(context.Context, int) error {
	return errors.New("no event-driven process exit wait on this platform")
}
