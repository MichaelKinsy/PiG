package pigsdklock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/filelock"
)

// noLock is the lock of a file system that refuses file locks: an NFS mount without a lock manager answers every flock with ENOLCK.
type noLock struct{ path string }

func (l noLock) fail() error { return &os.PathError{Op: "flock", Path: l.path, Err: syscall.ENOLCK} }

func (l noLock) TryLock() (bool, error) { return false, l.fail() }

func (l noLock) TryRLock() (bool, error) { return false, l.fail() }

func (l noLock) TryLockContext(context.Context, time.Duration) (bool, error) {
	return false, l.fail()
}

func (l noLock) TryRLockContext(context.Context, time.Duration) (bool, error) {
	return false, l.fail()
}

func (l noLock) RLock() error { return l.fail() }

func (l noLock) Unlock() error { return nil }

// Building against a staged SDK takes a shared lease and staging takes an exclusive one. On a file system without file locks the
// failure must name the cause and the remedy, not only the errno of a lock call.
func TestAcquireExplainsAFileSystemWithoutFileLocks(t *testing.T) {
	previous := filelock.New
	filelock.New = func(path string) filelock.Lock { return noLock{path} }
	t.Cleanup(func() { filelock.New = previous })
	configRoot := t.TempDir()
	for name, acquire := range map[string]func(context.Context, string) (func() error, error){"build": AcquireBuild, "stage": AcquireStage} {
		release, err := acquire(context.Background(), configRoot)
		if release != nil {
			t.Fatalf("%s lease on a file system without file locks", name)
		}
		if err == nil {
			t.Fatalf("%s: no error on a file system without file locks", name)
		}
		for _, want := range []string{"does not support file locks", filepath.Join(configRoot, "state", "pigsdk"), "PIG_HOME"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not contain %q", name, err, want)
			}
		}
		if !errors.Is(err, syscall.ENOLCK) {
			t.Errorf("%s: error %q does not wrap the lock failure (ENOLCK)", name, err)
		}
	}
}
