package app

import (
	"os"
	"path/filepath"
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
}

// startWithAStalledScan starts an app whose scan of the server's own code waits for release, its bases not awaited.
func startWithAStalledScan(t *testing.T) *stalledScan {
	t.Helper()
	program := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(program, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	scan := &stalledScan{entered: make(chan struct{}), release: make(chan struct{})}
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

func TestAppClosesWithoutWaitingForAScanOfItsOwnCodeStalledOnAReadItsLateResultIsDropped(t *testing.T) {
	scan := startWithAStalledScan(t)
	<-scan.entered
	closed := make(chan error, 1)
	go func() { closed <- scan.app.Close(background) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		close(scan.release)
		t.Fatal("the close waited for the scan")
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
	// The scan's read ends only now; its result is dropped: nothing reported, nothing recorded.
	close(scan.release)
	<-scan.app.BaseWatched()
	if reported := scan.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v after the close, want none", reported)
	}
	if scan.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by a scan that ended after the close")
	}
}
