//go:build parity

package runner

import "runtime"

// tempDirVar is the environment variable os.TempDir reads first, which
// chooses the snapshot root: TMPDIR on Unix, and TMP on Windows, where TMPDIR
// has no effect.
var tempDirVar = func() string {
	if runtime.GOOS == "windows" {
		return "TMP"
	}
	return "TMPDIR"
}()
