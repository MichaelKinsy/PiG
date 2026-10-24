package runtimecell

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var (
	errStandInDenied  = errors.New("stand-in access denied")
	errStandInPending = errors.New("stand-in delete pending")
)

// standInDeletion plays the Windows codes for a walk that runs on any OS.
func standInDeletion(err error) (pending, denied bool) {
	return errors.Is(err, errStandInPending), errors.Is(err, errStandInDenied)
}

func standInTracker() *VanishTracker { return &VanishTracker{deletion: standInDeletion} }

// A directory that a walk found and another process then removed held no source; one that is still there and cannot be read stays an error. Windows reports a directory being deleted as ERROR_DELETE_PENDING or ERROR_ACCESS_DENIED, which the stand-in codes play here.
func TestVanishTrackerClassification(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "dir", Err: errStandInDenied}
	gone := &fs.PathError{Op: "lstat", Path: "dir", Err: fs.ErrNotExist}
	pendingErr := &fs.PathError{Op: "open", Path: "dir", Err: errStandInPending}
	exists := func() error { return nil }
	for _, tc := range []struct {
		name      string
		err       error
		lstat     func() error
		want      bool
		wantAudit int // directories Verify has to look at again
	}{
		{"not exist", &fs.PathError{Op: "open", Path: "dir", Err: fs.ErrNotExist}, exists, true, 0},
		{"delete pending", pendingErr, exists, true, 0},
		{"denied, directory gone", denied, func() error { return gone }, true, 0},
		{"denied, directory still pending deletion", denied, func() error { return denied }, true, 1},
		{"denied, directory there", denied, exists, false, 0},
		{"other error, directory gone", errors.New("input/output error"), func() error { return gone }, false, 0},
	} {
		tracker := standInTracker()
		directoryVanished := tracker.vanished
		if got := directoryVanished("dir", tc.err, tc.lstat); got != tc.want {
			t.Errorf("%s: directoryVanished = %v, want %v", tc.name, got, tc.want)
		}
		if len(tracker.deferred) != tc.wantAudit {
			t.Errorf("%s: %d directories left for Verify, want %d", tc.name, len(tracker.deferred), tc.wantAudit)
		}
	}
	// Without Windows' codes an access denial is only ever an unreadable directory.
	tracker := &VanishTracker{deletion: func(error) (bool, bool) { return false, false }}
	if tracker.vanished("dir", os.ErrPermission, func() error { return gone }) {
		t.Error("a permission error was taken for a vanished directory without a Windows deletion code")
	}
}

// Verify settles the directories skipped while both the open and the Lstat were denied: gone is fine, still denied is delete-pending, and one that now answers Lstat exists, so the hash fails naming it.
func TestVanishTrackerVerifiesDeniedDirectoriesAtTheEnd(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "dir", Err: errStandInDenied}
	for _, tc := range []struct {
		name    string
		later   error
		wantErr bool
	}{
		{"gone at the end", &fs.PathError{Op: "lstat", Path: "dir", Err: fs.ErrNotExist}, false},
		{"still denied at the end", denied, false},
		{"still delete pending at the end", &fs.PathError{Op: "lstat", Path: "dir", Err: errStandInPending}, false},
		{"there at the end", nil, true},
		{"unrelated error at the end", errors.New("input/output error"), true},
	} {
		calls := 0
		lstat := func() error {
			calls++
			if calls == 1 {
				return denied
			}
			return tc.later
		}
		tracker := standInTracker()
		if !tracker.vanished("pkg/sub", denied, lstat) {
			t.Fatalf("%s: a directory denying open and Lstat was not skipped provisionally", tc.name)
		}
		err := tracker.Verify()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Verify = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "pkg/sub") {
			t.Errorf("%s: Verify error %q does not name the directory", tc.name, err)
		}
	}
}

// deniedDirFS denies the directory pkg to every listing, and answers a Stat of it from later after the first.
type deniedDirFS struct {
	fstest.MapFS
	stats int
	later error
}

func (d *deniedDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "pkg" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errStandInDenied}
	}
	return d.MapFS.ReadDir(name)
}

func (d *deniedDirFS) Stat(name string) (fs.FileInfo, error) {
	if name == "pkg" {
		d.stats++
		if d.stats == 1 {
			return nil, &fs.PathError{Op: "lstat", Path: name, Err: errStandInDenied}
		}
		if d.later != nil {
			return nil, d.later
		}
	}
	return d.MapFS.Stat(name)
}

// The source of a module that denies a directory to the walk must not drop out of the hash: if that directory is still there when the walk ends the hash fails, and if it is gone (delete-pending ended) the hash is that of the module without it.
func TestHashGoModuleFailsWhenADeniedDirectoryStillExistsAtTheEnd(t *testing.T) {
	files := fstest.MapFS{
		"go.mod":      &fstest.MapFile{Data: []byte("module example.com/m\n")},
		"root.go":     &fstest.MapFile{Data: []byte("package m\n")},
		"pkg/real.go": &fstest.MapFile{Data: []byte("package pkg\n")},
	}
	without := hashGoModuleFS(fstest.MapFS{"go.mod": files["go.mod"], "root.go": files["root.go"]}, "example.com/m")
	for _, tc := range []struct {
		name    string
		later   error
		want    string
		wantErr bool
	}{
		{"gone at the end", &fs.PathError{Op: "lstat", Path: "pkg", Err: fs.ErrNotExist}, without, false},
		{"still denied at the end", &fs.PathError{Op: "lstat", Path: "pkg", Err: errStandInDenied}, without, false},
		{"there at the end", nil, "", true},
	} {
		got := hashGoModuleFSTracked(&deniedDirFS{MapFS: files, later: tc.later}, "example.com/m", standInTracker())
		if tc.wantErr {
			if !strings.HasPrefix(got, "error:") || !strings.Contains(got, "pkg") {
				t.Errorf("%s: a directory that denied the walk and still exists produced reusable hash %q", tc.name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: hash = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The hash of an on-disk module is stable while a build creates and removes directories under a directory of the module that no CACHEDIR.TAG marks (cargo's rmeta temp directories before its target directory's tag exists, or any other tool's output).
func TestHashGoModuleIgnoresConcurrentChurnOfUntaggedDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps a directory that another process is deleting visible to Lstat (it reads the directory entry without opening it) and answers its open with ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION until the last handle closes. A live directory the walk may not list gives the same answers, so a hash cannot tell them apart without waiting, and the walk fails closed on both. A throwaway Windows run of this churn failed 100 of 100 iterations on that, with sharing violations and, after a Lstat re-check at the end of the walk, with the directory still there. What Windows guarantees is the tagged case, which never opens below the tag (the cargo target directory tests), and the classification tests with the real codes.")
	}
	root := t.TempDir()
	for name, content := range goModuleFiles() {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deps := filepath.Join(root, "tmp", "test-fixtures", "rust-target", "release", "deps")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		t.Fatal(err)
	}
	want := hashGoModule(root)
	if strings.HasPrefix(want, "error:") {
		t.Fatal(want)
	}
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		var busy []string
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			temp := filepath.Join(deps, "rmeta"+strconv.Itoa(i))
			if err := os.MkdirAll(temp, 0o755); err != nil {
				done <- err
				return
			}
			if err := os.WriteFile(filepath.Join(temp, "lib.rmeta"), []byte("x"), 0o644); err != nil {
				done <- err
				return
			}
			if err := removeChurnDir(temp, &busy); err != nil {
				done <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := hashGoModule(root); got != want {
			close(stop)
			<-done
			t.Fatalf("hash changed or failed during churn: %q, want %q", got, want)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// removeChurnDir removes the churn directory temp, and any that an earlier call could not remove. Windows refuses to delete an
// entry the hash walk has open (a sharing violation, or an access denial on one pending deletion): that is the operating system's
// delete semantics, not what the test checks, so such a directory is kept for the next call and the t.TempDir cleanup.
func removeChurnDir(temp string, retry *[]string) error {
	pending := append([]string(nil), *retry...)
	pending = append(pending, temp)
	var busy []string
	for _, path := range pending {
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if !churnRemovalBusy(err) {
				return err
			}
			busy = append(busy, path)
		}
	}
	*retry = busy
	return nil
}
