package codingagent

import (
	"os"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// pathExists is Node's fs.existsSync: whether the path, followed through links, can be read at all.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// acquireSyncLockWithRetry shares Pi's settings/trust directory-lock
// protocol with independently imported Node stores. Like Pi's
// acquireLockSyncWithRetry (settings-manager.ts:241-265) and
// acquireTrustLockSync (trust-manager.ts:137-164), it returns
// proper-lockfile's error unchanged, ELOCKED included once the retries are
// spent.
var acquireSyncLockWithRetry = func(path string) (release func() error, err error) {
	lease, err := pilock.AcquireSync(path)
	if err != nil {
		return nil, err
	}
	return lease.Release, nil
}

// releaseSyncLock releases a settings or trust lock as Pi's finally blocks do
// (settings-manager.ts:291-295, trust-manager.ts:169-176): a release failure
// replaces the operation's result or error.
func releaseSyncLock(release func() error, err *error) {
	if releaseErr := release(); releaseErr != nil {
		*err = releaseErr
	}
}
