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

func TestAppClosesWithoutWaitingForABaseBuildStalledOnAReadItsLateResultIsDropped(t *testing.T) {
	entry, pipe := heldWebEntry(t)
	s := startUnbuilt(t, nil, withPublicWeb(entry, plainPage))
	held := untilReading(t, pipe)
	at := time.Now()
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(at); took > 2*time.Second {
		t.Fatalf("close took %v, it waited for the build", took)
	}
	// Closed: HTTP answers no more while the build is still stalled.
	if conn, err := socketDial(s.port); err == nil {
		_ = conn.Close()
		t.Fatal("HTTP answers after the close")
	}
	// The build's read ends only when the pipe is written: its result is dropped.
	if _, err := held.WriteString("export {};"); err != nil {
		t.Fatal(err)
	}
	_ = held.Close()
	<-s.app.BasesBuilt()
	<-s.app.BaseWatched()
	if reported := s.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v after the close, want none", reported)
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
