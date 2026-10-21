package bunruntime

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// installLockName is the advisory lock file in the slot root. The lock file is never removed: unlinking it while a process waits on it would let two processes hold "the" lock on different files.
const installLockName = ".bun-install.lock"

const installLockPoll = 25 * time.Millisecond

// withInstallLock runs fn holding the slot's cross-process lock, so that one process's install, remove or status check never overlaps another's replacement of <root>/bun (the replacement renames the old directory aside before the new one in, and a reader between the renames finds no Bun). A slot root that does not exist yet has nothing to replace, and when create is false fn runs without the lock.
func withInstallLock(ctx context.Context, root string, create bool, fn func() error) error {
	if create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(filepath.Join(root, installLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) && !create {
			return fn()
		}
		return err
	}
	defer func() { _ = file.Close() }() // closing the file releases the lock
	for {
		locked, err := tryLockFile(file)
		if err != nil {
			return err
		}
		if locked {
			defer unlockFile(file)
			return fn()
		}
		timer := time.NewTimer(installLockPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
