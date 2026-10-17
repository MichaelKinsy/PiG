package aotruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// workerFixture is a worker whose compiler is installed (a fake with the pinned digest) and whose builds are a function the test supplies.
type workerFixture struct {
	w       *worker
	builds  atomic.Int32
	extDir  string
	entry   string
	request Request
}

func newWorkerFixture(t *testing.T, build func(buildOptions) (*buildResult, error)) *workerFixture {
	t.Helper()
	root := t.TempDir()
	f := &workerFixture{extDir: t.TempDir()}
	f.entry = filepath.Join(f.extDir, "index.ts")
	if err := os.WriteFile(f.entry, []byte("export default () => {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.request = Request{Key: key("d"), Members: []Member{{Name: "ext", Entry: f.entry}}, RuntimeTree: t.TempDir()}
	w := newWorker(root, "linux-x64", nil)
	// A release whose archive pins the fake compiler stands in for Pi-Bolt's.
	rel := newRelease(t, "linux-x64", releaseOptions{})
	w.installer = rel.installer(w.store.dir, "linux-x64")
	// The fixture replaces the package's pins; restoring the saved slice keeps the tests of the real pins independent of the order tests run in.
	saved := archives
	t.Cleanup(func() { archives = saved })
	archives = rel.pinned
	w.buildSet = func(_ context.Context, opts buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		return build(opts)
	}
	f.w = w
	f.writeRequest(t)
	return f
}

func (f *workerFixture) writeRequest(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(f.w.store.requestsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(f.request)
	if err := os.WriteFile(filepath.Join(f.w.store.requestsDir(), f.request.Key+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *workerFixture) requestExists() bool {
	_, err := os.Stat(filepath.Join(f.w.store.requestsDir(), f.request.Key+".json"))
	return err == nil
}

func TestWorkerBuildsARequestedSetOnceAndPublishesIt(t *testing.T) {
	f := newWorkerFixture(t, nil)
	f.w.buildSet = func(_ context.Context, opts buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		if opts.Platform != "linux-x64" || opts.Request.Key != f.request.Key || opts.Compiler == "" || opts.Log == nil {
			t.Errorf("build options %+v", opts)
		}
		return fakeBuilt(t, "image", memberInputs(opts.Request.Members)), nil
	}
	f.w.tick(context.Background())
	if f.builds.Load() != 1 {
		t.Fatalf("builds = %d, want 1", f.builds.Load())
	}
	if f.requestExists() {
		t.Error("the request is still pending after its image was published")
	}
	if !f.w.store.haveCurrentImage(f.request.Key) {
		t.Error("the manifest holds no current image for the set")
	}
	if _, err := os.Stat(f.w.store.workDir()); err == nil {
		t.Error("the build's work directory was not removed")
	}
	// A request the host records again for a set with a current image is dropped without a build.
	f.writeRequest(t)
	f.w.tick(context.Background())
	if f.builds.Load() != 1 || f.requestExists() {
		t.Errorf("builds = %d, request exists = %v after a repeated request", f.builds.Load(), f.requestExists())
	}
}

func TestWorkerRebuildsAStaleImageAfterTheSourceChanged(t *testing.T) {
	f := newWorkerFixture(t, nil)
	f.w.buildSet = func(_ context.Context, opts buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		return fakeBuilt(t, "image", memberInputs(opts.Request.Members)), nil
	}
	f.w.tick(context.Background())
	if err := os.WriteFile(f.entry, []byte("export default () => { /* edited */ }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f.w.store.haveCurrentImage(f.request.Key) {
		t.Fatal("the edited source left the image current")
	}
	f.writeRequest(t)
	f.w.tick(context.Background())
	if f.builds.Load() != 2 || !f.w.store.haveCurrentImage(f.request.Key) {
		t.Errorf("builds = %d, current = %v; want a second build that makes the image current", f.builds.Load(), f.w.store.haveCurrentImage(f.request.Key))
	}
	m, _ := f.w.store.readManifest()
	if len(m.Sets) != 1 {
		t.Errorf("the manifest holds %d sets", len(m.Sets))
	}
	if entries, _ := os.ReadDir(f.w.store.imagesDir()); len(entries) != 1 {
		t.Errorf("images/ holds %d directories after a rebuild, want the new image only", len(entries))
	}
}

func TestWorkerRecordsAFailedBuildOnceAndRetriesWhenTheSourceChanges(t *testing.T) {
	f := newWorkerFixture(t, nil)
	f.w.buildSet = func(_ context.Context, opts buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		return nil, errors.New("compiler exploded")
	}
	f.w.tick(context.Background())
	if f.builds.Load() != 1 || f.requestExists() {
		t.Fatalf("builds = %d, request exists = %v", f.builds.Load(), f.requestExists())
	}
	failures := f.w.store.failures()
	if got := failures[f.request.Key]; got.Reason != "build_failed" || !strings.Contains(got.Detail, "compiler exploded") || got.Log == "" {
		t.Fatalf("failure memo = %+v", got)
	}
	if m, _ := f.w.store.readManifest(); len(m.Sets) != 0 {
		t.Errorf("a failed build published %v", sortedKeys(m.Sets))
	}
	f.writeRequest(t)
	f.w.tick(context.Background())
	if f.builds.Load() != 1 || f.requestExists() {
		t.Errorf("an unchanged set built again: builds = %d", f.builds.Load())
	}
	if err := os.WriteFile(f.entry, []byte("export default () => { /* fixed */ }"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeRequest(t)
	f.w.tick(context.Background())
	if f.builds.Load() != 2 {
		t.Errorf("a changed source did not build again: builds = %d", f.builds.Load())
	}
}

func TestWorkerNamesAPathSensitiveRefusal(t *testing.T) {
	f := newWorkerFixture(t, nil)
	f.w.buildSet = func(_ context.Context, _ buildOptions) (*buildResult, error) {
		return nil, errPathSensitive{pathFinding{File: "/ext/locale.ts", Marker: "import.meta.url"}}
	}
	f.w.tick(context.Background())
	got := f.w.store.failures()[f.request.Key]
	if got.Reason != "path_sensitive" || !strings.Contains(got.Detail, "/ext/locale.ts") {
		t.Errorf("failure = %+v", got)
	}
}

func TestWorkerCancelledBuildLeavesTheRequestForTheNextStart(t *testing.T) {
	f := newWorkerFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	f.w.buildSet = func(ctx context.Context, _ buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		cancel()
		return nil, ctx.Err()
	}
	f.w.tick(ctx)
	if !f.requestExists() {
		t.Error("a build cancelled by the session ending dropped its request")
	}
	if len(f.w.store.failures()) != 0 {
		t.Errorf("a cancelled build was recorded as a failure: %v", f.w.store.failures())
	}
}

func TestWorkerYieldsToAnotherProcessBuilding(t *testing.T) {
	f := newWorkerFixture(t, nil)
	if err := os.MkdirAll(f.w.store.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	held, got, err := lockFile(filepath.Join(f.w.store.dir, "build.lock"), false)
	if err != nil || !got {
		t.Fatalf("lock: %v %v", got, err)
	}
	defer held.Unlock()
	f.w.tick(context.Background())
	if f.builds.Load() != 0 || !f.requestExists() {
		t.Errorf("builds = %d, request exists = %v while another process holds the build lock", f.builds.Load(), f.requestExists())
	}
}

func TestWorkerDownloadsTheCompilerOnlyWhenItIsMissingAndNeverOffline(t *testing.T) {
	f := newWorkerFixture(t, nil)
	var downloads int
	rel := newRelease(t, "linux-x64", releaseOptions{})
	archives = rel.pinned
	f.w.installer = rel.installer(f.w.store.dir, "linux-x64")
	f.w.buildSet = func(_ context.Context, opts buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		downloads = len(rel.requested)
		return fakeBuilt(t, "image", nil), nil
	}
	t.Setenv("PIG_OFFLINE", "1")
	f.w.tick(context.Background())
	if f.builds.Load() != 0 || len(rel.requested) != 0 || !f.requestExists() {
		t.Fatalf("offline: builds %d, requests %v, request exists %v", f.builds.Load(), rel.requested, f.requestExists())
	}
	t.Setenv("PIG_OFFLINE", "")
	f.w.tick(context.Background())
	if f.builds.Load() != 1 || downloads == 0 {
		t.Fatalf("online: builds %d, downloads %d", f.builds.Load(), downloads)
	}
	// The next set builds with the installed compiler and downloads nothing more.
	f.request.Key = key("e")
	f.writeRequest(t)
	before := len(rel.requested)
	f.w.tick(context.Background())
	if f.builds.Load() != 2 || len(rel.requested) != before {
		t.Errorf("second set: builds %d, new requests %v", f.builds.Load(), rel.requested[before:])
	}
}

func TestWorkerAfterAFailedDownloadWaitsBeforeTryingAgain(t *testing.T) {
	f := newWorkerFixture(t, nil)
	rel := newRelease(t, "linux-x64", releaseOptions{listWrongDigest: true})
	archives = rel.pinned
	f.w.installer = rel.installer(f.w.store.dir, "linux-x64")
	f.w.tick(context.Background())
	first := len(rel.requested)
	if first == 0 || f.builds.Load() != 0 || !f.requestExists() {
		t.Fatalf("requests %d, builds %d, request exists %v", first, f.builds.Load(), f.requestExists())
	}
	f.w.tick(context.Background())
	if len(rel.requested) != first {
		t.Errorf("the worker downloaded again within the retry delay: %v", rel.requested[first:])
	}
}

func TestWorkerOnAnUnsupportedPlatformDoesNothing(t *testing.T) {
	f := newWorkerFixture(t, nil)
	f.w.platform = "linux-arm64"
	f.w.tick(context.Background())
	if f.builds.Load() != 0 || !f.requestExists() {
		t.Errorf("builds = %d on linux-arm64", f.builds.Load())
	}
	if _, err := os.Stat(f.w.store.manifestPath()); err == nil {
		t.Error("an unsupported platform wrote a manifest through the worker")
	}
}

func TestWorkerLoopBuildsInTheBackgroundAndStopCancelsIt(t *testing.T) {
	f := newWorkerFixture(t, nil)
	started := make(chan struct{})
	f.w.buildSet = func(ctx context.Context, _ buildOptions) (*buildResult, error) {
		f.builds.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.w.start()
	f.w.start() // idempotent
	<-started
	f.w.stop() // returns only after the loop ended and the build was cancelled
	if f.builds.Load() != 1 {
		t.Errorf("builds = %d", f.builds.Load())
	}
	if !f.requestExists() {
		t.Error("the cancelled build dropped its request")
	}
	f.w.stop() // idempotent

	// A stopped worker starts again, as a session after a reload does, and its first loop's end does not close the second loop's signal.
	restarted := make(chan struct{})
	f.w.buildSet = func(ctx context.Context, _ buildOptions) (*buildResult, error) {
		close(restarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.w.start()
	<-restarted
	f.w.stop()
}
