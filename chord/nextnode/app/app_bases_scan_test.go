package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Ports the two tests of the scan of the server's own code held on a read of packages/chord/test/node/app/app.test.ts ("base code",
// Pi 284e63d9): `answers before the scan of its own code finished: the scan runs after the start` and `closes without waiting for a
// scan of its own code stalled on a read`; the second is the forced Go lifetime difference below. Pi holds the scan on a named pipe its start script imports; the Go server's own code is its
// executable (D603), so the scan is the step that names it (`AppOptions.executable`, the test seam), and the test holds that step: the
// same stall, at the first thing the scan does, on every platform.
//
// Forced Go lifetime difference (not a numbered divergence): Pi's `#close` does not wait for the scan; the Go close joins it, last,
// bounded by its context (AppOptions.draining marks the point it begins waiting). A close whose context ends first returns that
// error and abandons the scan, whose late result is dropped as Pi's is.

// stalledScan is an app whose scan of its own code is held: it has begun (entered closed) and ends when release is closed.
type stalledScan struct {
	started
	entered chan struct{}
	release chan struct{}
	// draining is closed when the close begins to wait for the scan.
	draining chan struct{}
}

// startWithAStalledScan starts an app whose scan of the server's own code waits for release, its bases not awaited.
func startWithAStalledScan(t *testing.T) *stalledScan {
	t.Helper()
	program := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(program, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	scan := &stalledScan{entered: make(chan struct{}), release: make(chan struct{}), draining: make(chan struct{})}
	// Closed whatever the test did, so a scan still held never outlives it.
	t.Cleanup(func() {
		select {
		case <-scan.release:
		default:
			close(scan.release)
		}
	})
	began := time.Now()
	scan.started = startUnbuilt(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) {
			close(scan.entered)
			<-scan.release
			return program, nil
		}
		// The program is older than the process: not an edit since its start, compared by its bytes.
		o.processStart = func() time.Time { return began.Add(time.Hour) }
		o.draining = func() { close(scan.draining) }
	})
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
	close(scan.release)
	<-scan.app.BaseWatched()
	if reported := scan.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v, want none", reported)
	}
}

// closeIn closes the app in the background: what it returns.
func (s *stalledScan) closeIn(ctx context.Context) <-chan error {
	closed := make(chan error, 1)
	go func() { closed <- s.app.Close(ctx) }()
	return closed
}

// untilDraining returns once the close began to wait for the scan; the close ending first (it did not wait) fails the test.
func (s *stalledScan) untilDraining(t *testing.T, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		t.Fatalf("the close returned (%v) while the scan was held: it did not wait for it", err)
	case <-s.draining:
	}
}

func TestAppCloseWaitsForAScanOfItsOwnCodeStalledOnAReadAndReturnsOnceItEndsItsLateResultIsDropped(t *testing.T) {
	scan := startWithAStalledScan(t)
	<-scan.entered
	closed := scan.closeIn(background)
	scan.untilDraining(t, closed)
	// Closed: HTTP answers no more while the scan is still stalled, and the close is still waiting for it.
	if conn, err := socketDial(scan.port); err == nil {
		_ = conn.Close()
		t.Fatal("HTTP answers after the close")
	}
	select {
	case <-scan.app.BaseWatched():
		t.Fatal("the base code is watched while its scan is held")
	default:
	}
	// The scan's read ends only now; its result is dropped: nothing reported, nothing recorded.
	close(scan.release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	select {
	case <-scan.app.BaseWatched():
	default:
		t.Fatal("the close returned before the scan ended")
	}
	if reported := scan.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v after the close, want none", reported)
	}
	if scan.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by a scan that ended after the close")
	}
}

func TestAppCloseAbandonsAScanStalledOnAReadWhenItsContextEndsAndReturnsTheContextsError(t *testing.T) {
	scan := startWithAStalledScan(t)
	<-scan.entered
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	closed := scan.closeIn(ctx)
	scan.untilDraining(t, closed)
	cancel()
	if err := <-closed; !errors.Is(err, context.Canceled) {
		t.Fatalf("close = %v, want the context's error", err)
	}
	select {
	case <-scan.app.BaseWatched():
		t.Fatal("the base code is watched while its scan is held")
	default:
	}
	// The abandoned scan ends on its own; its result is dropped.
	close(scan.release)
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
	draining := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)
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
		o.draining = func() { close(draining) }
	})
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.app.Close(background) }()
	select {
	case err := <-closed:
		t.Fatalf("the close returned (%v) while the scan was held: it did not wait for it", err)
	case <-draining:
	}
	// Every step ran, the plugins' watcher closed; the scan records now.
	letGo()
	if err := <-closed; err != nil {
		t.Fatal(err)
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
