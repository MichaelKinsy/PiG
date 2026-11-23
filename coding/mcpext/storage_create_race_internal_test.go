package mcpext

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestFileAuthStorageBackendCreateNeverOverwritesCompletedWrite proves T2: when
// caller B observes mcp-auth.json missing on its pre-lock existence probe, and
// caller A creates and writes credentials before B executes its initial "{}"
// create, B must not truncate or overwrite A's file, and A's credentials must
// survive intact.
func TestFileAuthStorageBackendCreateNeverOverwritesCompletedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-auth.json")
	backendA := NewFileAuthStorageBackend(path)
	backendB := NewFileAuthStorageBackend(path)

	bObservedMissing := make(chan struct{})
	aFinished := make(chan struct{})
	var pauseOnce atomic.Bool

	prevStat := statStoragePath
	statStoragePath = func(name string) (os.FileInfo, error) {
		info, err := prevStat(name)
		if name == path && os.IsNotExist(err) && pauseOnce.CompareAndSwap(false, true) {
			close(bObservedMissing)
			<-aFinished
		}
		return info, err
	}
	t.Cleanup(func() { statStoragePath = prevStat })

	const payloadA = `{"serverA":{"tokens":{"accessToken":"token-a"}}}`
	var errA, errB error
	var bSaw string

	var wg sync.WaitGroup
	wg.Go(func() {
		// B enters first, sees the missing file, and pauses before creating "{}".
		errB = backendB.WithLock(func(current string, _ bool) (*string, error) {
			bSaw = current
			return nil, nil
		})
	})
	wg.Go(func() {
		// Wait until B has observed ENOENT, then run A's complete write to completion.
		<-bObservedMissing
		defer close(aFinished)
		errA = backendA.WithLock(func(string, bool) (*string, error) {
			next := payloadA
			return &next, nil
		})
	})
	wg.Wait()

	if errA != nil {
		t.Fatalf("writer A failed: %v", errA)
	}
	if errB != nil {
		t.Fatalf("caller B failed: %v", errB)
	}
	if bSaw != payloadA {
		t.Fatalf("caller B read %q after writer A completed, want %q (pre-lock create truncated existing file)", bSaw, payloadA)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payloadA {
		t.Fatalf("on-disk mcp-auth.json = %q, want %q", got, payloadA)
	}
}

// TestFileAuthStorageBackendCreatesOwnerOnlyAndFollowsDanglingSymlink checks
// that exclusive creation creates a new mcp-auth.json with owner-only access
// (0600 on POSIX, owner-only DACL on Windows) and materializes the target of a
// dangling symlink without replacing the link.
func TestFileAuthStorageBackendCreatesOwnerOnlyAndFollowsDanglingSymlink(t *testing.T) {
	t.Run("owner-only new file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp-auth.json")
		err := NewFileAuthStorageBackend(path).WithLock(func(current string, exists bool) (*string, error) {
			if !exists || current != "{}" {
				t.Errorf("initial read = (%q, %v), want ({}, true)", current, exists)
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		private, err := ownerfile.OwnerOnly(path, info)
		if err != nil || !private {
			t.Fatalf("mcp-auth.json owner-only = %v, err = %v", private, err)
		}
	})
	t.Run("dangling symlink target", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "shared-mcp-auth.json")
		link := filepath.Join(dir, "mcp-auth.json")
		testenv.Symlink(t, target, link)

		next := `{"s":{"tokens":{"accessToken":"ok"}}}`
		if err := NewFileAuthStorageBackend(link).WithLock(func(current string, exists bool) (*string, error) {
			if !exists || current != "{}" {
				t.Errorf("initial read through link = (%q, %v), want ({}, true)", current, exists)
			}
			return &next, nil
		}); err != nil {
			t.Fatal(err)
		}
		linkInfo, err := os.Lstat(link)
		if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("link was replaced: mode=%v err=%v", linkInfo, err)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != next {
			t.Fatalf("target = %q, err=%v; want %q", got, err, next)
		}
	})
}
