package codingagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// exclusiveCreate makes the file at name and fails with fs.ErrExist when the name is taken, as Windows CREATE_NEW and clonefile do.
func exclusiveCreate(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// Snapshot file names come from no clock, so loads that start in one clock tick (a Windows clock ticks every half to fifteen milliseconds)
// each take a name of their own: 64 at once all succeed with 64 names.
func TestSnapshotFilesTakenAtOnceAllSucceedWithNamesOfTheirOwn(t *testing.T) {
	dir := t.TempDir()
	const n = 64
	files := make([]*os.File, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			files[i], errs[i] = createSnapshotFile(dir, exclusiveCreate)
		})
	}
	close(start)
	wg.Wait()
	names := map[string]int{}
	for i := range n {
		if errs[i] != nil {
			t.Errorf("snapshot %d: %v", i, errs[i])
			continue
		}
		names[files[i].Name()]++
		_ = files[i].Close()
	}
	if len(names) != n-countErrs(errs) {
		t.Fatalf("%d snapshot files took %d names", n-countErrs(errs), len(names))
	}
}

func countErrs(errs []error) int {
	n := 0
	for _, err := range errs {
		if err != nil {
			n++
		}
	}
	return n
}

// A name that is taken by a file of another process (one that had this process's id) is no failure: the next name is tried, and the file that
// held the first is left as it was.
func TestSnapshotFileSkipsANameAnotherFileHolds(t *testing.T) {
	dir := t.TempDir()
	var tried []string
	file, err := createSnapshotFile(dir, func(name string) (*os.File, error) {
		tried = append(tried, name)
		if len(tried) == 1 {
			if err := os.WriteFile(name, []byte("another process's file"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return exclusiveCreate(name)
	})
	if err != nil {
		t.Fatalf("a taken name failed the snapshot: %v", err)
	}
	defer func() { _ = file.Close() }()
	if len(tried) != 2 || tried[0] == tried[1] || file.Name() != tried[1] {
		t.Fatalf("names tried %q, file %q: want a second, different name", tried, file.Name())
	}
	if data, err := os.ReadFile(tried[0]); err != nil || string(data) != "another process's file" {
		t.Fatalf("the file that held the first name is now %q (%v)", data, err)
	}
}

// An error that is not "the name exists" ends the snapshot at once: no other name is tried.
func TestSnapshotFileStopsOnAnyOtherError(t *testing.T) {
	boom := errors.New("no room")
	calls := 0
	file, err := createSnapshotFile(t.TempDir(), func(string) (*os.File, error) {
		calls++
		return nil, boom
	})
	if file != nil || !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("file %v, error %v after %d tries, want the error of the first", file, err, calls)
	}
}

// Sessions loaded at once each get a snapshot of their own of the file they read, on the platform's own file creation (O_TMPFILE, a removed
// temporary file, or a Windows delete-on-close file): the snapshots are held open together, as the sessions hold them.
func TestSnapshotsOfSmallFilesTakenAtOnceAllSucceed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const n = 64
	snapshots := make([]*sessionSnapshot, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			snapshot, err := newSessionSnapshot(path)
			if err == nil {
				_, err = snapshot.add([]byte(fmt.Sprintf(`{"n":%d}`, i)))
			}
			snapshots[i], errs[i] = snapshot, err
		})
	}
	close(start)
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Errorf("snapshot %d: %v", i, errs[i])
			continue
		}
		want := fmt.Sprintf("{\"n\":%d}\n", i)
		got := make([]byte, len(want))
		if _, err := snapshots[i].ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) || string(got) != want {
			t.Errorf("snapshot %d holds %q (%v), want %q", i, got, err, want)
		}
		snapshots[i].close()
	}
}
