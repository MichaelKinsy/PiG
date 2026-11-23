//go:build unix

package workers

import (
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/nextnode/internal/lockseam"
)

var (
	lockFileDirMu sync.RWMutex
	lockFileDir   string
)

func init() {
	lockseam.ForceSocketFile = func(dir string) func() {
		lockFileDirMu.Lock()
		prev := lockFileDir
		lockFileDir = dir
		lockFileDirMu.Unlock()
		return func() {
			lockFileDirMu.Lock()
			lockFileDir = prev
			lockFileDirMu.Unlock()
		}
	}
}

// forcedLockFileLocation returns a socket-file lockLocation under the test seam's directory when set.
func forcedLockFileLocation(key string) (lockLocation, bool) {
	lockFileDirMu.RLock()
	dir := lockFileDir
	lockFileDirMu.RUnlock()
	if dir == "" {
		return lockLocation{}, false
	}
	return lockLocation{kind: lockFile, address: filepath.Join(dir, "chord-lock-"+key+".sock")}, true
}
