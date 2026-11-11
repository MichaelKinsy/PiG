//go:build unix

package workers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// pi: packages/chord/src/next-node/workers/lock.ts
// A temporary directory too deep for sun_path does not break the lock: the socket goes to a private per-user directory under
// /tmp (lock.ts `socketPath`).
func TestASocketFileLockFallsBackToAPrivateDirectoryWhenTheTempDirIsTooLongForASocketPath(t *testing.T) {
	deep := t.TempDir()
	for len(deep) <= maxSocketPathBytes {
		deep = filepath.Join(deep, "a-directory-name-that-makes-the-path-long")
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", deep)
	file := fmt.Sprintf("chord-lock-test-%d-%d.sock", os.Getpid(), time.Now().UnixNano())
	address, err := socketPath(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(address) > maxSocketPathBytes || strings.HasPrefix(address, deep) {
		t.Fatalf("socketPath = %q (%d bytes), want a path under /tmp that fits in sun_path", address, len(address))
	}
	held, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
}
