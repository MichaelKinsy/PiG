//go:build linux

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
)

// The tests of app_bases_test.go that hold a base build on a named pipe (Pi's mkfifo, Linux only).

// stillOpen is whether the channel is still open after 300 ms (Pi's `pending`).
func stillOpen(done <-chan struct{}) bool {
	select {
	case <-done:
		return false
	case <-time.After(300 * time.Millisecond):
		return true
	}
}

// heldWebEntry is a web entry in a new directory importing `./held.ts`, a pipe: its build waits until the pipe is written.
func heldWebEntry(t *testing.T) (entry, pipe string) {
	t.Helper()
	dir := t.TempDir()
	pipe = filepath.Join(dir, "held.ts")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	entry = filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte("import \"./held.ts\";\nglobalThis.started = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry, pipe
}

// feedPipe writes text into the pipe from now on, each read of it getting it, until the returned stop is called: an open for
// writing succeeds only while a reader waits (a build, the watcher's first comparison).
func feedPipe(t *testing.T, pipe, text string) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			if file, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_, _ = file.WriteString(text)
				_ = file.Close()
			}
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
	t.Cleanup(stop)
	return stop
}

// untilReading returns the pipe's write end once a reader waits on it (a non-blocking open for writing succeeds then); the caller
// writes it and closes it.
func untilReading(t *testing.T, pipe string) *os.File {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if file, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			return file
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nothing read the pipe")
	return nil
}

func TestAppAnswersItsOwnerPageBeforeTheWebBaseIsBuiltAPageRequestWaitsForItsBase(t *testing.T) {
	entry, pipe := heldWebEntry(t)
	s := startUnbuilt(t, nil, withPublicWeb(entry, plainPage))
	if got := surfaceGet(t, s.port, "/_chord/owner", nil).status; got != 200 {
		t.Fatalf("owner page = %d", got)
	}
	if got := s.app.Web.Base(); got != "" {
		t.Fatalf("base = %q before its build, want none", got)
	}
	answer := make(chan reply, 1)
	go func() { answer <- surfaceGet(t, s.port, "/", nil) }()
	select {
	case got := <-answer:
		t.Fatalf("the page answered %d before its base was built", got.status)
	case <-time.After(300 * time.Millisecond):
	}
	if !stillOpen(s.app.BasesBuilt()) {
		t.Fatal("the bases are built")
	}
	if !stillOpen(s.app.BaseWatched()) {
		t.Fatal("the base code is watched before the bases are built: the scan runs after them")
	}
	stop := feedPipe(t, pipe, "export {};")
	defer stop()
	got := <-answer
	want := "<p>/_chord/web/base." + s.app.Web.Base() + ".js</p>"
	if got.status != 200 || string(got.body) != want || s.app.Web.Base() == "" {
		t.Fatalf("page = %d %q, want 200 %q", got.status, got.body, want)
	}
	<-s.app.BaseWatched()
}

// startWithAStalledBuild starts an app whose web base build is held on a pipe, once the build reads it. The pipe is written when the
// test ends (before the app's cleanup joins the build), so a test that failed early does not hold it.
func startWithAStalledBuild(t *testing.T) (s started, held *os.File) {
	t.Helper()
	entry, pipe := heldWebEntry(t)
	s = startUnbuilt(t, nil, withPublicWeb(entry, plainPage))
	held = untilReading(t, pipe)
	t.Cleanup(func() { letBuildGo(t, held) })
	return s, held
}

// closesAtOnce closes the app, failing the test when the close does not return while the build is stalled: it returns what Close does.
func closesAtOnce(t *testing.T, app *App, ctx context.Context) error {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- app.Close(ctx) }()
	select {
	case err := <-closed:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("the close did not return while a base build was stalled: it waits for the build")
		return nil
	}
}

// Pi's `#close` does not wait for the bases' builds ("what they finish after the close is dropped"): the close returns at once with
// no error, the build still stalled, and what the build finishes later publishes and reports nothing. Wait (Go's ownership of the
// build's goroutine, no Pi counterpart) then joins it.
func TestAppCloseDoesNotWaitForABaseBuildStalledOnAReadItsLateResultIsDroppedAndWaitReturnsNil(t *testing.T) {
	s, held := startWithAStalledBuild(t)
	ctx, cancel := context.WithTimeout(background, time.Minute)
	defer cancel()
	if err := closesAtOnce(t, s.app, ctx); err != nil {
		t.Fatalf("close = %v, want nil: the build is not waited for", err)
	}
	// Closed: HTTP answers no more while the build is still stalled.
	if conn, err := socketDial(s.port); err == nil {
		_ = conn.Close()
		t.Fatal("HTTP answers after the close")
	}
	if !stillOpen(s.app.BasesBuilt()) {
		t.Fatal("the bases are built while the build is stalled")
	}
	// The build's read ends only when the pipe is written: its result is dropped.
	letBuildGo(t, held)
	if err := s.app.Wait(ctx); err != nil {
		t.Fatalf("wait = %v, want nil once the build ended", err)
	}
	<-s.app.BasesBuilt()
	<-s.app.BaseWatched()
	if built := s.app.Web.built(); built == nil || built.Error == nil {
		t.Fatalf("built = %+v, want the build the close ended failed (nothing published on the closed host)", built)
	}
	if reported := s.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v after the close, want none (no base_build, no late error)", reported)
	}
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked: a record of the late build was added to what the plugins compare")
	}
}

// refusedWhileLocked fails the test unless a second server on the data directory is refused: the first one still holds its lock.
func refusedWhileLocked(t *testing.T, s started) {
	t.Helper()
	second, err := StartServer(optionsFor(t, s.dataDir, s.root), background)
	if err == nil {
		closeAndWait(t, second)
		t.Fatal("a second server started on the data directory: the lock was released")
	}
	if !strings.Contains(err.Error(), "another server runs on "+s.dataDir) {
		t.Fatalf("a second server on the data directory: %v, want it refused (the lock is held)", err)
	}
}

// startsAgain fails the test unless a second server starts on the data directory (the lock was released); it closes it.
func startsAgain(t *testing.T, s started) {
	t.Helper()
	second, err := StartServer(optionsFor(t, s.dataDir, s.root), background)
	if err != nil {
		t.Fatalf("a second server on the data directory after the close: %v, want it started (the lock was released)", err)
	}
	if err := second.Close(background); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(background); err != nil {
		t.Fatal(err)
	}
}

// letBuildGo ends the stalled build: its pipe is written (a second call finds it closed and does nothing).
func letBuildGo(t *testing.T, held *os.File) {
	t.Helper()
	if _, err := held.WriteString("export {};"); err != nil {
		return
	}
	_ = held.Close()
}

// As Pi's `#close`, the data directory's lock is released when the close returns, the build still running (it writes nothing under
// the data directory): the lock is held until then, and a second server starts right after.
func TestAppCloseReleasesTheDataDirectorysLockAtOnceWhileABuildIsStalled(t *testing.T) {
	s, held := startWithAStalledBuild(t)
	refusedWhileLocked(t, s)
	if err := closesAtOnce(t, s.app, background); err != nil {
		t.Fatal(err)
	}
	startsAgain(t, s)
	if !stillOpen(s.app.BasesBuilt()) {
		t.Fatal("the build ended before the second server started: the lock was not released while it ran")
	}
	letBuildGo(t, held)
	if err := s.app.Wait(background); err != nil {
		t.Fatal(err)
	}
}

// A repeated Close returns the first call's result (Pi's one memoised promise), at once, the build still stalled.
func TestAppARepeatedCloseWhileABuildIsStalledReturnsTheFirstResultAtOnce(t *testing.T) {
	s, held := startWithAStalledBuild(t)
	if err := closesAtOnce(t, s.app, background); err != nil {
		t.Fatal(err)
	}
	if err := closesAtOnce(t, s.app, background); err != nil {
		t.Fatalf("second close = %v, want the first one's result", err)
	}
	letBuildGo(t, held)
	if err := s.app.Wait(background); err != nil {
		t.Fatal(err)
	}
}

// Wait is Go's join of the builds and the scan the app started: it returns the context's error while a stalled build outlives the
// context (the build goes on), and nil once the builds ended.
func TestAppWaitReturnsTheContextsErrorWhileAStalledBuildOutlivesItAndNilOnceItEnded(t *testing.T) {
	s, held := startWithAStalledBuild(t)
	ctx, cancel := context.WithCancel(background)
	cancel()
	if err := s.app.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %v, want the context's error while the build is stalled", err)
	}
	if !stillOpen(s.app.BasesBuilt()) {
		t.Fatal("the build ended with the abandoned wait")
	}
	if err := closesAtOnce(t, s.app, background); err != nil {
		t.Fatal(err)
	}
	timed, stop := context.WithTimeout(background, 100*time.Millisecond)
	defer stop()
	if err := s.app.Wait(timed); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait after the close = %v, want the context's error while the build is stalled", err)
	}
	letBuildGo(t, held)
	if err := s.app.Wait(background); err != nil {
		t.Fatalf("wait = %v, want nil once the build ended", err)
	}
}

// Without a build or a scan started, Wait returns nil at once (a context that ended is no error).
func TestAppWaitWithoutAnyBuildStartedReturnsNilAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(background)
	cancel()
	if err := (&App{}).Wait(ctx); err != nil {
		t.Fatalf("wait = %v, want nil", err)
	}
}

func TestAppAnAbortedApplyWaitingAtTheWebStepForTheInitialBuildStopsThereTheBuildGoesOn(t *testing.T) {
	entry, pipe := heldWebEntry(t)
	s := startUnbuilt(t, nil, withPublicWeb(entry, func(chordhttp.HttpRequest, nodesurfaces.PageAssets) (string, error) { return "<p>page</p>", nil }))
	ctx, cancel := context.WithCancelCause(background)
	defer cancel(nil)
	applied := make(chan plugins.RolloutResult, 1)
	go func() {
		result, err := s.app.ApplyChanges(ctx)
		if err != nil {
			t.Errorf("ApplyChanges: %v", err)
		}
		applied <- result
	}()
	select {
	case <-applied:
		t.Fatal("the Apply ended before the build did")
	case <-time.After(300 * time.Millisecond):
	}
	cancel(errors.New("cancel Apply"))
	result := <-applied
	if result.Stopped == nil || result.Stopped.Step != "web" || result.Stopped.Error == nil || !strings.Contains(result.Stopped.Error.Error(), "cancel Apply") {
		t.Fatalf("stopped = %+v, want the web step, cancelled", result.Stopped)
	}
	stop := feedPipe(t, pipe, "export {};")
	defer stop()
	if got := surfaceGet(t, s.port, "/", nil).status; got != 200 {
		t.Fatalf("page = %d, want 200: the build went on", got)
	}
	<-s.app.BaseWatched()
}

// D604: a link's upgrade is held until the bases are built, where Pi's connects at once.
func TestAppHoldsALinkUntilTheBasesAreBuilt(t *testing.T) {
	entry, pipe := heldWebEntry(t)
	s := startUnbuilt(t, nil, withPublicWeb(entry, plainPage))
	headers := ownerHeaders(t, s.app)
	status := make(chan int, 1)
	go func() {
		request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(s.port)+"/chord", nil)
		if err != nil {
			t.Error(err)
			status <- 0
			return
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
		request.Header.Set("Sec-WebSocket-Version", "13")
		request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		request.Header.Set("Origin", "http://127.0.0.1:"+strconv.Itoa(s.port))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			status <- 0
			return
		}
		_ = response.Body.Close()
		status <- response.StatusCode
	}()
	select {
	case got := <-status:
		t.Fatalf("the link answered %d before the bases were built", got)
	case <-time.After(300 * time.Millisecond):
	}
	stop := feedPipe(t, pipe, "export {};")
	defer stop()
	if got := <-status; got != http.StatusSwitchingProtocols {
		t.Fatalf("link = %d, want 101 once the bases are built", got)
	}
	<-s.app.BaseWatched()
}
