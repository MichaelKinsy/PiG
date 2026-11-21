package cli

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// flushGatedSettingsStorage is a settings storage whose write waits in entered, then fails with fail or reports stored.
type flushGatedSettingsStorage struct {
	entered func()
	stored  func()
	fail    error
}

func (s *flushGatedSettingsStorage) WithLock(_ string, fn func(current *string) (*string, error)) error {
	next, err := fn(nil)
	if err != nil || next == nil {
		return err
	}
	s.entered()
	if s.fail != nil {
		return s.fail
	}
	s.stored()
	return nil
}

const mainFlushHelper = "PIG_TEST_MAIN_FLUSH_HELPER"

// mainFlushReleaseDelay is how long the gated write is held once the child has started Main: far longer than a faux print run, so a Main that does not wait for the write returns first.
const mainFlushReleaseDelay = 700 * time.Millisecond

// runMainFlushHelper is the child of the two tests below: a setter on another goroutine is writing settings when Main is called, the write is released 700 ms after Main has started, long after a faux print run is done, and the trace file records the order of what happens. A write that failed is recorded for DrainErrors, and the trace says what was recorded after Main returned.
func runMainFlushHelper(t *testing.T, mode string) (trace string, output []byte, err error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main-flush.txt")
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$")
	home := t.TempDir()
	command.Env = append(os.Environ(), mainFlushHelper+"="+mode+":"+path, "HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_OFFLINE=1")
	output, err = command.CombinedOutput()
	data, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
	return string(data), output, err
}

func mainFlushHelperBody(t *testing.T, failure error, path string) {
	appendTrace := func(text string) {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		if _, err := file.WriteString(text + "\n"); err != nil {
			panic(err)
		}
		if err := file.Close(); err != nil {
			panic(err)
		}
	}
	entered, release, stored := make(chan struct{}), make(chan struct{}), make(chan struct{})
	manager := codingagent.NewSettingsManagerFromStorage(&flushGatedSettingsStorage{
		entered: func() { close(entered); <-release },
		stored:  func() { appendTrace("settings write stored"); close(stored) },
		fail:    failure,
	}, codingagent.SettingsManagerCreateOptions{})
	go func() { _ = manager.SetTheme("dark") }()
	<-entered
	time.AfterFunc(mainFlushReleaseDelay, func() { close(release) })
	// A print run that succeeds returns from Main, which runs its deferred calls.
	Main([]string{"--model", "test-faux/faux-1", "--no-extensions", "--no-session", "--offline", "-p", "reply with exactly: ok"}, nil)
	appendTrace("main returned")
	if failure == nil {
		// Without the flush the write is still going: wait for it, so that the trace says in which order the two ended.
		select {
		case <-stored:
		case <-time.After(testbudget.Wait(t)):
			appendTrace("settings write never stored")
		}
	}
	for _, recorded := range manager.DrainErrors() {
		appendTrace("recorded " + recorded.Scope + ": " + recorded.Error.Error())
	}
	// Exit before the testing package prints PASS and coverage lines into the captured output.
	os.Exit(0)
}

// A settings write that a setter on another goroutine is performing when Main returns is finished first, as a Node process does not end while writeQueue has work (settings-manager.ts:576-588, flush :670-672): Main's deferred FlushSettingsWrites (main.go) waits for it. The write is released 700 ms after Main has started, long after a faux print run is done, so a Main that does not wait returns before it.
func TestMainReturnsAfterASettingsWriteInProgressIsStored(t *testing.T) {
	if path, ok := strings.CutPrefix(os.Getenv(mainFlushHelper), "ok:"); ok {
		mainFlushHelperBody(t, nil, path)
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	trace, output, err := runMainFlushHelper(t, "ok")
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	if trace != "settings write stored\nmain returned\n" {
		t.Fatalf("order = %q, want the settings write stored before Main returns", trace)
	}
}

// A settings write that fails while Main waits for it does not change what Main does: flush() awaits a queue whose rejections enqueueWrite has already caught and recorded (settings-manager.ts:576-588,670-672), so no exit code changes and no text is printed at the flush; the error waits for drainErrors, which a settings diagnostics reader makes.
func TestMainFlushOfAFailedSettingsWriteRecordsItAndPrintsNothing(t *testing.T) {
	if path, ok := strings.CutPrefix(os.Getenv(mainFlushHelper), "fail:"); ok {
		mainFlushHelperBody(t, errors.New("EACCES: permission denied, mkdir '/agent'"), path)
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	trace, output, err := runMainFlushHelper(t, "fail")
	if err != nil {
		t.Fatalf("child exit: %v\n%s", err, output)
	}
	if want := "main returned\nrecorded global: EACCES: permission denied, mkdir '/agent'\n"; trace != want {
		t.Fatalf("trace = %q, want %q", trace, want)
	}
	if string(output) != "ok\n" {
		t.Fatalf("output = %q, want the reply of the print run and nothing about the failed write", output)
	}
}
