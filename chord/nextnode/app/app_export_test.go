package app

// pi: packages/chord/src/node/app/data.ts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// Ports packages/chord/test/node/app/app.test.ts "export and import per address" (Pi main c23b9ec1): a running worker packs its own
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
	var before atomic.Bool
	done := make(chan struct{})
	starting, running, watching := observe(t, found, "project|p1", done)
	archive := filepath.Join(t.TempDir(), "p1.tar")
	exported := make(chan error, 1)
	go func() {
		err := s.app.ExportData(address, archive, background)
		// What the worker did while the export ran is what the watcher has seen by now.
		before.Store(running.Load())
		exported <- err
	}()
	held := make(chan *host.Reference, 1)
	go func() {
		reference, err := s.app.Host.Get(background, notes, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
		if err != nil {
			t.Error(err)
		}
		held <- reference
	}()
	// The hold starts the worker only after the export released the lock: the worker was `starting` (waiting for the lock), never
	// `running`, while the export ran.
	if err := <-exported; err != nil {
		t.Fatal(err)
	}
	if before.Load() {
		t.Fatal("the worker ran while the export held its lock")
	}
	reference := <-held
	if reference == nil {
		return
	}
	defer reference.Release()
	if got := member(t, reference, "text"); got != "one" {
		t.Fatalf("text = %v", got)
	}
	close(done)
	<-watching
	// The hold starts the worker (`starting`) while the export holds its lock; it runs only after it released it: the hold's reference
	// is live now, so the worker ran once the export was done.
	if !starting.Load() || !running.Load() {
		t.Fatalf("the worker was starting %v, running %v", starting.Load(), running.Load())
	}
}

// observe watches the status of the worker at key until done is closed: whether it was ever `starting` and `running`. The first
// `running` frame must come after the export finished; the caller checks that through the order of its own steps.
func observe(t *testing.T, found *workers.Workers, key string, done <-chan struct{}) (starting, running *atomic.Bool, finished <-chan struct{}) {
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
	go func() {
		defer close(ended)
		defer func() { _, _ = handle.Stop() }()
		for {
			switch workerStatus(t, found, key) {
			case "starting":
				starting.Store(true)
			case "running":
				running.Store(true)
			}
			select {
			case <-changed:
			case <-done:
				return
			}
		}
	}()
	return starting, running, ended
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
