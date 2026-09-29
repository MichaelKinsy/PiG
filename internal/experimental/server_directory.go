package experimental

// Ports packages/coding-agent/src/experimental/server.ts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

const activationTimeout = 10_000 * time.Millisecond

// EnsurePrivateServerDirectory rejects symlinks and foreign ownership before setting the Unix socket directory's private mode.
func EnsurePrivateServerDirectory(directory string) error {
	if os.Getuid() < 0 {
		return errors.New("Unix socket directory requires a POSIX user ID")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Unix socket directory is not a directory: %s", directory)
	}
	if !serverDirectoryOwned(info) {
		return fmt.Errorf("Unix socket directory is not owned by the current user: %s", directory)
	}
	return os.Chmod(directory, 0o700)
}

// AcquireServerActivation serializes cold activators independently of the launcher's profile lock.
func AcquireServerActivation(ctx context.Context, directory, serverID string) (func() error, error) {
	lock, err := pilock.AcquireWithOptions(ctx, filepath.Join(directory, "activation-"+serverID), pilock.AcquireOptions{Stale: activationTimeout * 2, Update: activationTimeout, Retry: 25 * time.Millisecond, Wait: activationTimeout, OnCompromised: lockCompromised})
	if err != nil {
		return nil, err
	}
	return releaseOnce(lock.Release), nil
}
