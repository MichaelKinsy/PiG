package app

// pi: packages/chord/src/node/app/data.ts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
)

// Ports packages/chord/test/node/app/app.test.ts "export and import per address" (Pi 8a5da3ce6; the cases about the server's records and rows are in app_export_host_test.go): a running worker packs its own
// store without stopping, the worker's data object is the server's only, an export without a worker holds its lock (a hold arriving
// meanwhile starts the worker after it), a failed address stays failed after an export, and the round trip with its refusals. The
// round trip's file is the plugin's `attach`, read by the copy's `file`.

// workerStatus is the status of the worker at key in the workers' state: "" when it has none.
func workerStatus(t *testing.T, found *workers.Workers, key string) string {
	t.Helper()
	entry, _ := found.State().Value().(*delta.JsonObject).Value(key).(*delta.JsonObject)
	if entry == nil {
		return ""
	}
	status, _ := entry.Value("status").(string)
	return status
}

// awaitStatus waits, on the workers' state watch, until the status of key satisfies ready.
func awaitStatus(t *testing.T, found *workers.Workers, key string, ready func(string) bool) {
	t.Helper()
	changed := make(chan struct{}, 1)
	handle, err := found.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	handle.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	defer func() { _, _ = handle.Stop() }()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for !ready(workerStatus(t, found, key)) {
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("worker %s stayed %q", key, workerStatus(t, found, key))
		}
	}
}

func write(t *testing.T, reference *host.Reference, text string) {
	t.Helper()
	if _, err := reference.Call("write", text, background); err != nil {
		t.Fatal(err)
	}
}

func TestAppExportsARunningWorkersDataWithoutStoppingItTheWorkerPacksItsOwnStore(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	defer p1.Release()
	write(t, p1, "before")
	found := s.app.Workers["project"]
	entry := func() delta.JsonValue {
		return found.State().Value().(*delta.JsonObject).Value("project|p1")
	}
	pid := entry().(*delta.JsonObject).Value("pid")
	archive := filepath.Join(t.TempDir(), "p1.tar")
	exported := make(chan error, 1)
	go func() {
		exported <- s.app.ExportData(project.Address(delta.JsonObjectOf("id", "p1")), archive, background)
	}()
	// The worker goes on meanwhile: a write during the export.
	write(t, p1, "during")
	if err := <-exported; err != nil {
		t.Fatal(err)
	}
	if status := workerStatus(t, found, "project|p1"); status != "running" || entry().(*delta.JsonObject).Value("pid") != pid {
		t.Fatalf("worker after the export: %v", entry())
	}
	if got := member(t, p1, "text"); got != "during" {
		t.Fatalf("text = %v", got)
	}
	write(t, p1, "after")
	if err := s.app.ImportData(archive, project.Address(delta.JsonObjectOf("id", "p2")), background); err != nil {
		t.Fatal(err)
	}
	copied := holdNotes(t, s, "p2")
	defer copied.Release()
	// One consistent point: before or during, never after.
	if got := member(t, copied, "text"); got != "before" && got != "during" {
		t.Fatalf("copy text = %v", got)
	}
}

func TestAppRefusesTheWorkersDataObjectToAnyPrincipalButTheServers(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	defer p1.Release()
	_, err := s.app.Host.Root(func(ctx host.Ctx) (any, error) {
		return ctx.Get(HostData, delta.JsonObjectOf("address", AddressParam(project.Address(delta.JsonObjectOf("id", "p1")))), host.GetOptions{})
	}, background, host.RootOptions{Principal: testOwner})
	if chordErr, ok := errors.AsType[*data.ChordError](err); !ok || chordErr.Code != data.CodeForbidden {
		t.Fatalf("owner get: %v", err)
	}
}

func TestAppExportsWithoutAWorkerUnderItsLockAHoldArrivingMeanwhileStartsTheWorkerAfterIt(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	write(t, p1, "one")
	p1.Release()
	found := s.app.Workers["project"]
	address := project.Address(delta.JsonObjectOf("id", "p1"))
	if err := found.Stop(address, background); err != nil {
		t.Fatal(err)
	}
	if err := found.Retry(address, background); err != nil {
		t.Fatal(err)
	}
	// A large file makes the pack take a while.
	files := filepath.Join(s.dataDir, "workers", "project", "project%7Cp1", "files")
	if err := os.MkdirAll(files, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "big.bin"), make([]byte, 200*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := workerStatus(t, found, "project|p1"); status != "" {
		t.Fatalf("status before the export = %q", status)
	}
	lockName := workerLock(s.app.options.Name, s.dataDir, address.Key)
	// Prove by construction that a worker waits on the address's process lock before starting: hold p1's lock directly, start a
	// hold on p1, and concurrently start and reach `running` on unlocked sibling addresses p2 and p3 (no sleep or arbitrary wait).
	// While p1's lock is held, p1 must remain `starting` and never reach `running` even after p2 and p3 have booted to `running`;
	// once p1's lock is released, p1 finishes starting.
	directLock, err := acquireProcessLock(lockName, background)
	if err != nil {
		t.Fatal(err)
	}
	var directHeld atomic.Bool
	directHeld.Store(true)
	var releaseDirect sync.Once
	releaseDirectLock := func() error {
		var releaseErr error
		releaseDirect.Do(func() {
			directHeld.Store(false)
			releaseErr = releaseProcessLock(directLock)
		})
		return releaseErr
	}
	done := make(chan struct{})
	var ranWhileDirectLocked atomic.Bool
	starting, running, watching := observe(t, found, "project|p1", done, func(status string) {
		if status == "running" && directHeld.Load() {
			ranWhileDirectLocked.Store(true)
		}
	})
	heldDirect := make(chan *host.Reference, 1)
	heldDirectDone := make(chan struct{})
	go func() {
		defer close(heldDirectDone)
		reference, err := s.app.Host.Get(background, notes, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
		if err != nil {
			t.Error(err)
		}
		heldDirect <- reference
	}()
	// Cleanup runs LIFO: releaseDirectLock runs before waiting for heldDirectDone so an early failure never deadlocks.
	t.Cleanup(func() {
		<-heldDirectDone
		select {
		case ref := <-heldDirect:
			if ref != nil {
				ref.Release()
			}
		default:
		}
	})
	t.Cleanup(func() { _ = releaseDirectLock() })
	awaitStatus(t, found, "project|p1", func(status string) bool { return status == "starting" || status == "running" })
	if workerStatus(t, found, "project|p1") == "starting" {
		starting.Store(true)
	}
	// Boot two unlocked sibling workers (p2, p3) sequentially to `running` while p1's lock is held: a worker that skipped
	// ProcessLock boots concurrently with p2 and p3 and reaches `running` / resolves heldDirect well before both finish.
	for _, sibling := range []string{"p2", "p3"} {
		other := holdNotes(t, s, sibling)
		awaitStatus(t, found, "project|"+sibling, func(status string) bool { return status == "running" })
		write(t, other, "sibling")
		other.Release()
		if err := found.Stop(project.Address(delta.JsonObjectOf("id", sibling)), background); err != nil {
			t.Fatal(err)
		}
		if err := found.Retry(project.Address(delta.JsonObjectOf("id", sibling)), background); err != nil {
			t.Fatal(err)
		}
	}
	// Verify p1's Host.Get has not resolved while directLock is held, and its status is still `starting`, not `running`.
	select {
	case ref := <-heldDirect:
		if ref != nil {
			ref.Release()
		}
		t.Fatal("the worker ran while its lock was held")
	default:
	}
	if workerStatus(t, found, "project|p1") == "running" || ranWhileDirectLocked.Load() {
		t.Fatal("the worker ran while its lock was held")
	}
	if err := releaseDirectLock(); err != nil {
		t.Fatal(err)
	}
	refDirect := <-heldDirect
	if refDirect == nil {
		return
	}
	if got := member(t, refDirect, "text"); got != "one" {
		refDirect.Release()
		t.Fatalf("text = %v", got)
	}
	refDirect.Release()
	awaitStatus(t, found, "project|p1", func(status string) bool { return status == "running" })
	close(done)
	<-watching
	if ranWhileDirectLocked.Load() {
		t.Fatal("the worker ran while its lock was held")
	}
	if !starting.Load() || !running.Load() {
		t.Fatalf("direct-lock hold: worker was starting %v, running %v", starting.Load(), running.Load())
	}
	// Stop and clear the worker again, then run the full ExportData + concurrent Host.Get scenario: ExportData holds the same
	// process lock across PackData, and a hold arriving once the export holds the lock enters `starting` and reaches `running`
	// only after PackData has placed the archive and ExportData has released the lock.
	if err := found.Stop(address, background); err != nil {
		t.Fatal(err)
	}
	if err := found.Retry(address, background); err != nil {
		t.Fatal(err)
	}
	if status := workerStatus(t, found, "project|p1"); status != "" {
		t.Fatalf("status before the export = %q", status)
	}
	archiveDir := t.TempDir()
	archive := filepath.Join(archiveDir, "p1.tar")
	const maxAttempts = 5
	var exported chan error
	observed := false
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		exported = make(chan error, 1)
		exportedDone := make(chan struct{})
		go func(ch chan error, done chan struct{}) {
			defer close(done)
			ch <- s.app.ExportData(address, archive, background)
		}(exported, exportedDone)
		t.Cleanup(func() { <-exportedDone })
		// Wait until PackData has entered packStores (creating its ".chord-export-*" staging directory inside archiveDir), which only
		// happens after exportWorker acquires the address's process lock. Unlike a ProcessLock loop, this never contends with
		// ExportData for the lock. A stalled runner can miss the whole of packStores between two ReadDirs; retry a missed window
		// rather than treating it as a lock failure.
		observedPacking, exportErr, exportReturned := awaitExportPacking(t, archiveDir, archive, exported)
		if observedPacking {
			t.Logf("export packing observed on attempt %d", attempt)
			observed = true
			break
		}
		if !exportReturned {
			exportErr = <-exported
		}
		if exportErr != nil {
			t.Fatalf("export attempt %d failed: %v", attempt, exportErr)
		}
		<-exportedDone
		if err := os.Remove(archive); err != nil {
			t.Fatalf("removing missed-window archive: %v", err)
		}
		if status := workerStatus(t, found, "project|p1"); status != "" {
			t.Fatalf("status after missed export attempt %d = %q", attempt, status)
		}
	}
	if !observed {
		t.Fatalf("the export's packing was not observed in %d attempts", maxAttempts)
	}
	doneExport := make(chan struct{})
	var ranWhileExportLocked atomic.Bool
	// Start the watcher only for the observed attempt so missed attempts leave no status or hook state behind. PackData renames
	// archive into place strictly before exportWorker calls lock.Release(); seeing `running` while archive does not yet exist means
	// the worker ran while the export still held its lock. Re-checking os.Stat(archive) after seeing `running` avoids the
	// post-release race where ExportData releases the lock and the worker becomes `running` before ExportData returns.
	startingExport, runningExport, watchingExport := observe(t, found, "project|p1", doneExport, func(status string) {
		if status == "running" {
			if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
				ranWhileExportLocked.Store(true)
			}
		}
	})
	// Issue the hold as soon as packing is observed, before the 20 ms lock probe, so the hold arrives while the export still holds
	// its lock and the worker enters `starting` on that lock while the probe runs.
	held := make(chan *host.Reference, 1)
	heldDone := make(chan struct{})
	go func() {
		defer close(heldDone)
		reference, err := s.app.Host.Get(background, notes, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
		if err != nil {
			t.Error(err)
		}
		held <- reference
	}()
	t.Cleanup(func() {
		<-heldDone
		select {
		case ref := <-held:
			if ref != nil {
				ref.Release()
			}
		default:
		}
	})
	probeCtx, cancelProbe := context.WithTimeout(background, 20*time.Millisecond)
	probeLock, probeErr := acquireProcessLock(lockName, probeCtx)
	cancelProbe()
	if probeErr == nil {
		_ = releaseProcessLock(probeLock)
		if _, statErr := os.Stat(archive); errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("export started packing without holding its worker lock")
		}
	} else if probeCtx.Err() == nil {
		t.Fatalf("probing worker lock: %v", probeErr)
	}
	awaitStatus(t, found, "project|p1", func(status string) bool { return status == "starting" || status == "running" })
	switch workerStatus(t, found, "project|p1") {
	case "starting":
		startingExport.Store(true)
	case "running":
		if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
			ranWhileExportLocked.Store(true)
		}
	}
	if err := <-exported; err != nil {
		t.Fatal(err)
	}
	reference := <-held
	if reference == nil {
		return
	}
	defer reference.Release()
	if got := member(t, reference, "text"); got != "one" {
		t.Fatalf("text = %v", got)
	}
	awaitStatus(t, found, "project|p1", func(status string) bool { return status == "running" })
	close(doneExport)
	<-watchingExport
	if ranWhileExportLocked.Load() {
		t.Fatal("the worker ran while the export held its lock")
	}
	if !startingExport.Load() || !runningExport.Load() {
		t.Fatalf("the worker was starting %v, running %v", startingExport.Load(), runningExport.Load())
	}
}

func acquireProcessLock(name string, ctx context.Context) (*workers.ProcessLockHandle, error) {
	return workers.ProcessLock(name, ctx)
}

func releaseProcessLock(lock *workers.ProcessLockHandle) error {
	return lock.Release()
}

// awaitExportPacking waits until PackData has entered packStores (creating its ".chord-export-*" staging directory inside
// archiveDir), which only happens after exportWorker acquires the address's process lock. It returns observed=true when the
// staging directory is seen, or observed=false when the archive appears first with no staging directory seen (a missed poll
// window); returned is true if the export's error was already received from exported.
func awaitExportPacking(t *testing.T, archiveDir, archive string, exported <-chan error) (observed bool, exportErr error, returned bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(archiveDir)
		if err == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".chord-export-") {
					return true, nil, false
				}
			}
		}
		if _, err := os.Stat(archive); err == nil {
			return false, nil, false
		}
		select {
		case exportErr = <-exported:
			if exportErr == nil {
				if _, err := os.Stat(archive); err == nil {
					return false, nil, true
				}
			}
			t.Fatalf("export ended before packing: %v", exportErr)
		default:
		}
		runtime.Gosched()
	}
	t.Fatal("timed out waiting for export to start packing")
	return false, nil, false
}

// observe watches the status of the worker at key until done is closed: whether it was ever `starting` and `running`. Optional
// hooks run on each observed status in the watcher goroutine.
func observe(t *testing.T, found *workers.Workers, key string, done <-chan struct{}, hooks ...func(string)) (starting, running *atomic.Bool, finished <-chan struct{}) {
	t.Helper()
	starting, running = new(atomic.Bool), new(atomic.Bool)
	changed := make(chan struct{}, 1)
	handle, err := found.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	handle.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	ended := make(chan struct{})
	// A caller that fails before it closes done must not leave the watcher running: it ends with the test.
	halt := make(chan struct{})
	t.Cleanup(func() {
		close(halt)
		<-ended
	})
	go func() {
		defer close(ended)
		defer func() { _, _ = handle.Stop() }()
		record := func() {
			status := workerStatus(t, found, key)
			switch status {
			case "starting":
				starting.Store(true)
			case "running":
				running.Store(true)
			}
			for _, hook := range hooks {
				hook(status)
			}
		}
		for {
			record()
			select {
			case <-changed:
			case <-done:
				record()
				return
			case <-halt:
				return
			}
		}
	}()
	return starting, running, ended
}

// A test that fails before it closes `done` leaves no watcher behind: observe's goroutine ends with its test, so the leak checks of
// the tests after it do not fail for it (one failed export test took 88 others down through it).
func TestObserveEndsItsWatcherWithItsTestWhenTheCallerNeverClosesDone(t *testing.T) {
	t.Run("caller that fails before closing done", func(t *testing.T) {
		s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
		p1 := holdNotes(t, s, "p1")
		defer p1.Release()
		observe(t, s.app.Workers["project"], "project|p1", make(chan struct{}))
	})
	noGoroutinesOf(t, "chord/nextnode/app.observe")
}

func TestAppLeavesAFailedAddressFailedAfterAnExport(t *testing.T) {
	var failing atomic.Bool
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, func(o *AppOptions) {
		kind := testWorker(t, project, "")
		inner := kind.Start
		kind.Start = func(address contracts.Address, ctx context.Context) (delta.JsonValue, error) {
			if failing.Load() {
				return nil, errors.New("no start today")
			}
			return inner(address, ctx)
		}
		o.Workers = []AppWorkerKind{kind}
	})
	p1 := holdNotes(t, s, "p1")
	write(t, p1, "kept")
	p1.Release()
	found := s.app.Workers["project"]
	address := project.Address(delta.JsonObjectOf("id", "p1"))
	if err := found.Stop(address, background); err != nil {
		t.Fatal(err)
	}
	if err := found.Retry(address, background); err != nil {
		t.Fatal(err)
	}
	failing.Store(true)
	hold, err := s.app.Host.Hold(background, notes, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Release()
	awaitStatus(t, found, "project|p1", func(status string) bool { return status == "failed" })
	// The failed start is reported with the app's full error (the public one is "worker failed to start").
	s.errors.expect()
	if got := s.errors.kinds(); !slices.Contains(got, AppErrorWorkers) {
		t.Fatalf("reported %v, want the worker's failed start (workers)", got)
	}
	if err := s.app.ExportData(address, filepath.Join(t.TempDir(), "p1.tar"), background); err != nil {
		t.Fatal(err)
	}
	if status := workerStatus(t, found, "project|p1"); status != "failed" {
		t.Fatalf("status after the export = %q", status)
	}
}

func TestAppRoundTripsAWorkersDocumentsAndTablesAndRefusesAnExistingAddressAndAnotherApp(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	write(t, p1, "buy milk")
	if got, err := p1.Call("visit", "/", background); err != nil || got != 1.0 {
		t.Fatalf("visit = %v, %v", got, err)
	}
	// The worker's files through the plugin's `Files` (Pi's `attach`, then `file` of the copy).
	file, err := p1.Call("attach", "a file of p1", background)
	if err != nil {
		t.Fatalf("attach = %v, %v", file, err)
	}
	p1.Release()
	archive := filepath.Join(t.TempDir(), "p1.tar")
	if err := s.app.ExportData(project.Address(delta.JsonObjectOf("id", "p1")), archive, background); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(archive); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("archive: %v %v", info, err)
	}
	if err := s.app.ImportData(archive, project.Address(delta.JsonObjectOf("id", "p1")), background); err == nil || !strings.Contains(err.Error(), "has data (its worker runs)") {
		t.Fatalf("import over a running worker: %v", err)
	}
	if err := s.app.ImportData(archive, project.Address(delta.JsonObjectOf("id", "p2")), background); err != nil {
		t.Fatal(err)
	}
	copied := holdNotes(t, s, "p2")
	if got := member(t, copied, "text"); got != "buy milk" {
		t.Fatalf("copy text = %v", got)
	}
	if got, err := copied.Call("visit", "/", background); err != nil || got != 2.0 {
		t.Fatalf("copy visit = %v, %v", got, err)
	}
	if got, err := copied.Call("file", file, background); err != nil || got != "a file of p1" {
		t.Fatalf("copy file = %v, %v", got, err)
	}
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Fatalf("workers = %v", got)
	}
	copied.Release()
	// The exported address goes on: its next hold starts its worker again.
	again := holdNotes(t, s, "p1")
	if got := member(t, again, "text"); got != "buy milk" {
		t.Fatalf("p1 text = %v", got)
	}
	again.Release()
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	// Another app refuses the archive.
	other := startTestApp(t, nil, func(o *AppOptions) { o.Name = "other-app"; projectOptions(t)(o) })
	err = other.app.ImportData(archive, project.Address(delta.JsonObjectOf("id", "p1")), background)
	if err == nil || !strings.Contains(err.Error(), `an archive of "chord-test", not "other-app"`) {
		t.Fatalf("import into another app: %v", err)
	}
}
