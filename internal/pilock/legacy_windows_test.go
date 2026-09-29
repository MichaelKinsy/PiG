package pilock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLegacyWindowsSharingAndByteRangeLocks(t *testing.T) {
	for _, sharedDelete := range []bool{false, true} {
		name := "sharing-violation"
		if sharedDelete {
			name = "byte-range-lock"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.json")
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ageLegacyFixture(t, path+".lock")
			wide, err := windows.UTF16PtrFromString(path + ".lock")
			if err != nil {
				t.Fatal(err)
			}
			share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
			if sharedDelete {
				share |= windows.FILE_SHARE_DELETE
			}
			handle, err := windows.CreateFile(wide, windows.GENERIC_READ|windows.GENERIC_WRITE, share, nil, windows.OPEN_EXISTING, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			file := os.NewFile(uintptr(handle), path+".lock")
			t.Cleanup(func() { _ = file.Close() })
			if sharedDelete {
				// v0.2.0 ai/auth_lock_windows.go:21 locks one byte at offset zero.
				if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := acquire(path, SyncStale); !errors.Is(err, ErrLegacyLocked) {
				t.Fatalf("legacy owner not observed: %v", err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireSync(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A handle that shares delete access and holds no lock (a concurrent upgrader's probe, an indexer, an antivirus scan) must not leave a delete-pending name that fails the directory mkdir with access denied.
func TestLegacyReclaimBesideSharedReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ageLegacyFixture(t, path+".lock")
	wide, err := windows.UTF16PtrFromString(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(handle), path+".lock")
	t.Cleanup(func() { _ = reader.Close() })
	lease, err := AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path + ".lock"); err != nil || !info.IsDir() {
		t.Fatalf("lock beside the reader: %v, %v", info, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}
