package app

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Ports the two tests of the scan of the server's own code held on a read of packages/chord/test/node/app/app.test.ts ("base code",
// Pi 284e63d9): `answers before the scan of its own code finished: the scan runs after the start` and `closes without waiting for a
// scan of its own code stalled on a read`. Pi holds the scan on a named pipe its start script imports; the Go server's own code is its
// executable (D603), so the scan is the step that names it (`AppOptions.executable`, the test seam), and the test holds that step: the
// same stall, at the first thing the scan does, on every platform.

// stalledScan is an app whose scan of its own code is held: it has begun (entered closed) and ends when release is closed.
type stalledScan struct {
	started
	entered chan struct{}
	release chan struct{}
	letOnce sync.Once
}

// letGo ends the held scan (once); it runs when the test ends, before the app's cleanup joins the scan.
func (s *stalledScan) letGo() { s.letOnce.Do(func() { close(s.release) }) }

// startWithAStalledScan starts an app whose scan of the server's own code waits for release, its bases not awaited.
func startWithAStalledScan(t *testing.T) *stalledScan {
	t.Helper()
	program := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(program, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	scan := &stalledScan{entered: make(chan struct{}), release: make(chan struct{})}
	began := time.Now()
	scan.started = startUnbuilt(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) {
			close(scan.entered)
			<-scan.release
			return program, nil
		}
		// The program is older than the process: not an edit since its start, compared by its bytes.
		o.processStart = func() time.Time { return began.Add(time.Hour) }
	})
	// Registered after the app's cleanups, so it runs before them: whatever the test did, a scan still held never outlives it.
	t.Cleanup(scan.letGo)
	return scan
}

func TestAppAnswersBeforeTheScanOfItsOwnCodeFinishedTheScanRunsAfterTheStart(t *testing.T) {
	scan := startWithAStalledScan(t)
	if got := surfaceGet(t, scan.port, "/_chord/owner", nil).status; got != 200 {
		t.Fatalf("owner page = %d, want 200 while the scan is held", got)
	}
	// The scan runs after the bases are built (it would contend with the builds the first requests wait for): the bases end while
	// it is held, and `baseWatched` waits for it.
	<-scan.app.BasesBuilt()
	<-scan.entered
	select {
	case <-scan.app.BaseWatched():
		t.Fatal("the base code is watched while its scan is held")
	default:
	}
	scan.letGo()
	<-scan.app.BaseWatched()
	if reported := scan.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v, want none", reported)
	}
}

// Pi's "closes without waiting for a scan of its own code stalled on a read": the close returns while the scan is held (before Pi's
// 2 s), HTTP answers no more, and the scan's read ends only later (the process can exit then); its result is dropped: nothing reported,
// nothing recorded, `baseWatched` settles when the scan ends.
func TestAppClosesWithoutWaitingForAScanOfItsOwnCodeStalledOnAReadItsLateResultIsDropped(t *testing.T) {
	scan := startWithAStalledScan(t)
	<-scan.entered
	began := time.Now()
	if err := scan.app.Close(background); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took >= 2*time.Second {
		t.Fatalf("the close took %v while the scan was held", took)
	}
	// Closed: HTTP answers no more while the scan is still stalled.
	if conn, err := socketDial(scan.port); err == nil {
		_ = conn.Close()
		t.Fatal("HTTP answers after the close")
	}
	select {
	case <-scan.app.BaseWatched():
		t.Fatal("the base code is watched while its scan is held")
	default:
	}
	scan.letGo()
	<-scan.app.BaseWatched()
	if reported := scan.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v after the close, want none", reported)
	}
	if scan.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by a scan that ended after the close")
	}
}

// The check-then-act in the scan: the scan found the app open, then the close ran every step, the plugins' watcher's included,
// before the scan recorded its executable. The record then arrives after the close and watches nothing (Pi 284e63d9 watcher.ts
// `recordBase`'s closed guard): the base mark stays unset although the executable was edited since the start. Held at the point
// between the scan's last check of the close and the record (the process start the record's edit test reads).
func TestAppARecordOfTheServersCodeArrivingAfterTheClosedWatcherWatchesNothingAndMarksNothing(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	defer letGo()
	program := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(program, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	s := startUnbuilt(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) { return program, nil }
		// The program was written after the process started: an edit since the start, recorded as differing.
		o.processStart = func() time.Time {
			close(entered)
			<-release
			return began.Add(-time.Hour)
		}
	})
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.app.Close(background) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the close did not return while the scan was held: it waits for it")
	}
	// Every step ran, the plugins' watcher closed; the scan records now.
	letGo()
	if err := s.app.Wait(background); err != nil {
		t.Fatalf("wait = %v, want nil once the scan ended", err)
	}
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by a record that arrived after the watcher closed")
	}
	if reported := s.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v after the close, want none", reported)
	}
}

// The control of the test above: the same scan, held and released with the app open, marks `base`.
func TestAppARecordOfTheServersCodeEditedSinceTheStartMarksBaseWhileTheAppIsOpen(t *testing.T) {
	program := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(program, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	s := startUnbuilt(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) { return program, nil }
		o.processStart = func() time.Time { return began.Add(-time.Hour) }
	})
	<-s.app.BaseWatched()
	if !s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is not marked for an executable edited since the start")
	}
}
