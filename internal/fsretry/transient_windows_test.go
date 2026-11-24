//go:build windows

package fsretry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestTransientRenameClassifiesWindowsErrors(t *testing.T) {
	link := func(err error) error { return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: err} }
	for name, err := range map[string]error{
		"access denied":     link(windows.ERROR_ACCESS_DENIED),
		"sharing violation": link(windows.ERROR_SHARING_VIOLATION),
		"lock violation":    link(windows.ERROR_LOCK_VIOLATION),
		"EPERM":             link(syscall.EPERM),
		"EACCES":            link(syscall.EACCES),
		"EBUSY":             link(syscall.EBUSY),
		"bare errno":        windows.ERROR_ACCESS_DENIED,
		"wrapped":           errors.Join(errors.New("context"), link(windows.ERROR_SHARING_VIOLATION)),
	} {
		if !TransientRename(err) {
			t.Errorf("%s: TransientRename = false, want a retry", name)
		}
	}
	for name, err := range map[string]error{
		"nil":             nil,
		"file not found":  link(windows.ERROR_FILE_NOT_FOUND),
		"path not found":  link(windows.ERROR_PATH_NOT_FOUND),
		"exists":          link(windows.ERROR_ALREADY_EXISTS),
		"disk full":       link(windows.ERROR_DISK_FULL),
		"not same device": link(windows.ERROR_NOT_SAME_DEVICE),
		"ENOENT":          link(syscall.ENOENT),
		"plain":           errors.New("access is denied"),
	} {
		if TransientRename(err) {
			t.Errorf("%s: TransientRename = true, want the error reported unchanged", name)
		}
	}
}

// A file that another process holds open without delete sharing, as Go's
// os.Open does and as anti-virus scanners do, cannot be renamed until the
// handle closes. Rename waits the hold out and then renames the file.
func TestRenameWaitsOutAHandleHeldWithoutDeleteSharing(t *testing.T) {
	dir := t.TempDir()
	oldpath, newpath := filepath.Join(dir, "pig.exe"), filepath.Join(dir, "quarantine", "pig.exe")
	if err := os.WriteFile(oldpath, []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newpath), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(oldpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	var waits []time.Duration
	err = Rename(t.Context(), oldpath, newpath, func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		if len(waits) == 2 {
			_ = held.Close()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Rename after the hold closed: %v", err)
	}
	if len(waits) != 2 || waits[0] != RenameFirstDelay || waits[1] != 2*RenameFirstDelay {
		t.Fatalf("waits = %v, want two doubling delays from %v", waits, RenameFirstDelay)
	}
	if got, err := os.ReadFile(newpath); err != nil || string(got) != "image" {
		t.Fatalf("renamed file = %q (err=%v)", got, err)
	}
}

// A hold that outlasts the budget fails with the rename error unchanged.
func TestRenameReportsAHoldThatOutlastsTheBudget(t *testing.T) {
	dir := t.TempDir()
	oldpath := filepath.Join(dir, "pig.exe")
	if err := os.WriteFile(oldpath, []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(oldpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	var total time.Duration
	err = Rename(t.Context(), oldpath, filepath.Join(dir, "moved.exe"), func(_ context.Context, delay time.Duration) error {
		total += delay
		return nil
	})
	var link *os.LinkError
	if !errors.As(err, &link) || !TransientRename(err) {
		t.Fatalf("Rename = %v, want the rename's transient *os.LinkError", err)
	}
	if total != RenameBudget {
		t.Fatalf("waited %v, want the whole budget %v", total, RenameBudget)
	}
	if _, err := os.Stat(oldpath); err != nil {
		t.Fatalf("source after a failed rename: %v", err)
	}
}

// Cancellation ends the wait and reports both errors.
func TestRenameStopsWaitingWhenTheContextEnds(t *testing.T) {
	dir := t.TempDir()
	oldpath := filepath.Join(dir, "pig.exe")
	if err := os.WriteFile(oldpath, []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(oldpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = Rename(ctx, oldpath, filepath.Join(dir, "moved.exe"), nil)
	if !errors.Is(err, context.Canceled) || !TransientRename(err) {
		t.Fatalf("Rename = %v, want the rename error and context.Canceled", err)
	}
}
