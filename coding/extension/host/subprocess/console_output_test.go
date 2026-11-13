package subprocess

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi redirects stdout onto stderr before extensions load in print/JSON/RPC modes (main.ts takeOverStdout). os/exec shares a child pipe only when both writer values are equal; separate copy goroutines can reorder console.log and console.error.
func TestExtensionHeadlessConsoleUsesOneOrderedPipe(t *testing.T) {
	log, err := os.Create(filepath.Join(t.TempDir(), "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	for _, mode := range []string{"print", "json", "rpc"} {
		h := &Host{mode: mode}
		stdout, stderr := h.extensionOutput(log)
		if stdout != stderr {
			t.Errorf("%s: stdout/stderr use independent pipes and can reorder extension console writes", mode)
		}
	}
	stdout, stderr := (&Host{mode: "tui"}).extensionOutput(log)
	if stdout == stderr {
		t.Error("interactive stdout and stderr must retain distinct destinations")
	}
}

// swapStd replaces PiG's own stdout and stderr with pipes for the test, and returns what each collects once the returned function restores them.
func swapStd(t *testing.T) (collect func() (stdout, stderr string)) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	type capture struct {
		r, w *os.File
		buf  bytes.Buffer
		done chan struct{}
	}
	open := func() *capture {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		c := &capture{r: r, w: w, done: make(chan struct{})}
		go func() {
			defer close(c.done)
			_, _ = io.Copy(&c.buf, r)
		}()
		return c
	}
	out, errc := open(), open()
	os.Stdout, os.Stderr = out.w, errc.w
	var once sync.Once
	var gotOut, gotErr string
	restore := func() {
		once.Do(func() {
			os.Stdout, os.Stderr = origOut, origErr
			_ = out.w.Close()
			_ = errc.w.Close()
			<-out.done
			<-errc.done
			_ = out.r.Close()
			_ = errc.r.Close()
			gotOut, gotErr = out.buf.String(), errc.buf.String()
		})
	}
	t.Cleanup(restore)
	return func() (string, string) { restore(); return gotOut, gotErr }
}

// Every extension runtime's process writes where Pi's console does, not only Node's: a Go, Rust, Python or Lisp extension's stderr (and stdout, which Pi's console.log is) reaches PiG's terminal and also the extension's log. In the headless modes stdout goes to stderr, in the one pipe, in the order the process wrote it; interactive stdout stays on stdout.
func TestExtensionOutputReachesTheTerminalInPiOrder(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	const script = `echo one; echo two >&2; echo three; echo four >&2`
	for _, tc := range []struct {
		mode                   string
		wantStdout, wantStderr string
	}{
		{"print", "", "one\ntwo\nthree\nfour\n"},
		{"json", "", "one\ntwo\nthree\nfour\n"},
		{"rpc", "", "one\ntwo\nthree\nfour\n"},
		{"tui", "one\nthree\n", "two\nfour\n"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			collect := swapStd(t)
			log := newTestStderrLog(t)
			cmd := exec.Command("sh", "-c", script)
			h := &Host{mode: tc.mode}
			cmd.Stdout, cmd.Stderr = h.extensionOutput(log)
			if err := cmd.Run(); err != nil {
				t.Fatal(err)
			}
			log.closeWriter()
			gotStdout, gotStderr := collect()
			if gotStdout != tc.wantStdout || gotStderr != tc.wantStderr {
				t.Errorf("terminal stdout = %q stderr = %q, want %q and %q", gotStdout, gotStderr, tc.wantStdout, tc.wantStderr)
			}
			// The log keeps the stderr the diagnostics read: in headless modes that includes the console.log lines, which share the pipe.
			wantLog := "two\nfour\n"
			if tc.mode != "tui" {
				wantLog = "one\ntwo\nthree\nfour\n"
			}
			if data, err := os.ReadFile(log.path); err != nil || string(data) != wantLog {
				t.Errorf("log = %q, %v, want %q", data, err, wantLog)
			}
		})
	}
}

// A sink that fails or stalls never ends the extension: the terminal write fails silently and the pipe keeps draining.
func TestExtensionConsoleSurvivesAnUnwritableTerminal(t *testing.T) {
	closed, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	n, err := (extensionConsole{terminal: closed}).Write([]byte("lost"))
	if n != 4 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
}

func startShell(t *testing.T, script string, pipe *consolePipe, tee io.Writer) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Stderr, cmd.Stdout = pipe.write, tee
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A packed runner that wrote a failed member's cause and went on serving its siblings: flush returns once the sink holds what the process wrote before it, and
// the marker never reaches the sink.
func TestConsolePipeFlushReachesWhatTheProcessWrote(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	var sink lockedBuffer
	pipe, err := newConsolePipe(&sink)
	if err != nil {
		t.Fatal(err)
	}
	// The process writes its cause, then stays alive.
	cmd := startShell(t, `echo 'extension "x" failed to load: boom' >&2; sleep 30`, pipe, nil)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		pipe.flush()
		if strings.Contains(sink.String(), "boom") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.String(); got != "extension \"x\" failed to load: boom\n" {
		t.Fatalf("sink = %q", got)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	pipe.finish(time.Second)
	if strings.Contains(sink.String(), "pig-console-flush") {
		t.Fatalf("a flush marker reached the sink: %q", sink.String())
	}
}

// The reader holds back only a possible start of a marker, and it never loses or reorders bytes around markers, whatever the chunking: a marker split across
// reads is still removed, and a process that prints a prefix of the marker has it back.
func TestConsolePipeForwardsEveryByteAroundMarkers(t *testing.T) {
	var sink lockedBuffer
	pipe, err := newConsolePipe(&sink)
	if err != nil {
		t.Fatal(err)
	}
	marker := string(pipe.marker(7))
	half := len(marker) / 2
	chunks := []string{"before\n", marker[:half], marker[half:], "after \x00pig-console-fl", "ush:not-a-marker\n", string(pipe.marker(8)) + "tail"}
	for _, chunk := range chunks {
		if _, err := pipe.write.WriteString(chunk); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	pipe.finish(time.Second)
	want := "before\nafter \x00pig-console-flush:not-a-marker\ntail"
	if got := sink.String(); got != want {
		t.Fatalf("sink = %q, want %q", got, want)
	}
}

// With the process gone and the write end closed, flush has nothing to wait for.
func TestConsolePipeFlushAfterFinishReturns(t *testing.T) {
	var sink lockedBuffer
	pipe, err := newConsolePipe(&sink)
	if err != nil {
		t.Fatal(err)
	}
	pipe.finish(time.Second)
	done := make(chan struct{})
	go func() { pipe.flush(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("flush waited on a finished pipe")
	}
	var nilPipe *consolePipe
	nilPipe.flush()
	nilPipe.finish(0)
}

// gatedSink holds every write until released, and reports when the first one is in flight.
type gatedSink struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	buf     lockedBuffer
}

func newGatedSink() *gatedSink {
	return &gatedSink{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *gatedSink) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.buf.Write(p)
}

// A runner that fails its only member writes the cause and exits at once, so the reaper's finish has closed the write end by the time the host, which read the member's socket
// closing, flushes to read the cause. The copy may still hold what the process wrote: flush returns only once the sink has it.
func TestConsolePipeFlushAfterFinishWaitsForTheCopy(t *testing.T) {
	sink := newGatedSink()
	pipe, err := newConsolePipe(sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipe.write.WriteString("extension \"x\" load failed: boom\n"); err != nil {
		t.Fatal(err)
	}
	// The copy holds the cause: it is in flight to the sink, which has not taken it.
	<-sink.entered
	finished := make(chan struct{})
	go func() { pipe.finish(time.Minute); close(finished) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		pipe.mu.Lock()
		closed := pipe.closed
		pipe.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finish never closed the pipe")
		}
	}
	flushed := make(chan struct{})
	go func() { pipe.flush(); close(flushed) }()
	select {
	case <-flushed:
		t.Fatalf("flush returned after finish while the copy still held the process's output; sink = %q", sink.buf.String())
	case <-time.After(100 * time.Millisecond):
	}
	close(sink.release)
	select {
	case <-flushed:
	case <-time.After(10 * time.Second):
		t.Fatal("flush never returned once the copy drained")
	}
	if got := sink.buf.String(); got != "extension \"x\" load failed: boom\n" {
		t.Fatalf("sink = %q when flush returned", got)
	}
	<-finished
}

// A sink that stalls fills the pipe, and the marker write then blocks: flush still returns within consoleFlushWait, holds no lock a finish needs, and no marker reaches the sink.
type stalledSink struct {
	release chan struct{}
	buf     lockedBuffer
}

func (s *stalledSink) Write(p []byte) (int, error) {
	<-s.release
	return s.buf.Write(p)
}

func TestConsolePipeFlushIsBoundedWhileTheSinkStalls(t *testing.T) {
	sink := &stalledSink{release: make(chan struct{})}
	pipe, err := newConsolePipe(sink)
	if err != nil {
		t.Fatal(err)
	}
	// The process floods the pipe: the copy stalls in the sink, so the pipe fills and a further write blocks.
	flooded := make(chan struct{})
	go func() {
		defer close(flooded)
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for range 128 {
			if _, err := pipe.write.Write(chunk); err != nil {
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	pipe.flush()
	if took := time.Since(start); took > consoleFlushWait+time.Second {
		t.Fatalf("flush took %v with a stalled sink, want at most %v", took, consoleFlushWait+time.Second)
	}
	finished := make(chan struct{})
	go func() { pipe.finish(time.Second); close(finished) }()
	close(sink.release)
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("finish was held behind the stalled flush")
	}
	<-flooded
	if strings.Contains(sink.buf.String(), "pig-console-flush") {
		t.Fatal("a flush marker reached the sink")
	}
}
