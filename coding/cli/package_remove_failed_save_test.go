package cli

// pi: packages/coding-agent/src/package-manager-cli.ts

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// blockedInPrint reports whether some goroutine is inside runPackageCommand, blocked in a write of fmt.Print.
func blockedInPrint() bool {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for stack := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(stack, "runPackageCommand") && strings.Contains(stack, "fmt.Print(") && strings.Contains(stack, "internal/poll.(*FD).Write") {
			return true
		}
	}
	return false
}

// D633. `pig remove` run through the command reports a package list that could not be saved: exit 1, the error on stderr and no Removed line, where Pi prints Removed and exits 0
// (package-manager-cli.ts:1061-1063 over core/package-manager.ts). TestPackageInstallAndRemoveReportAFailedSave proves the removal at RemoveAndPersist only, because a lock held when the
// command starts also refuses its read of the list. Here the command reads the list first: its stdout is a full pipe, so its "Removing ..." line (written after the read and before the
// save) blocks until the test has taken the lock and drained the pipe.
func TestPackageRemoveCommandReportsAFailedSave(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	pkg := filepath.Join(cwd, "first-pkg")
	writePackageResource(t, filepath.Join(pkg, "extensions", "index.ts"), "export default function() {}")
	if stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", pkg}) }); code != 0 {
		t.Fatalf("install: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Fill the pipe a page at a time from a goroutine until a write blocks (os.Pipe on Windows does not support SetWriteDeadline).
	var filled atomic.Int64
	var fillStop atomic.Bool
	fillDone := make(chan struct{})
	go func() {
		defer close(fillDone)
		page := make([]byte, 4096)
		for !fillStop.Load() {
			if _, err := outWrite.Write(page); err != nil {
				return
			}
			filled.Add(int64(len(page)))
		}
	}()
	for last, still := int64(-1), 0; still < 3; {
		time.Sleep(50 * time.Millisecond)
		if now := filled.Load(); now == last {
			still++
		} else {
			last, still = now, 0
		}
	}
	stdoutBefore, stderrBefore := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	restore := func() { os.Stdout, os.Stderr = stdoutBefore, stderrBefore }
	defer restore()
	var stdout, stderr bytes.Buffer
	drained := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(&stderr, errRead); drained <- struct{}{} }()
	code := make(chan int, 1)
	go func() { code <- runPackageCommand([]string{"remove", pkg}) }()
	for !blockedInPrint() {
		select {
		case c := <-code:
			t.Fatalf("the command ended with %d before it wrote its Removing line: stderr %q", c, stderr.String())
		case <-time.After(time.Millisecond):
		}
	}
	// The command holds the list it read; the lock now refuses its save.
	holdSettingsLock(t, agentDir)
	fillStop.Store(true)
	go func() { _, _ = io.Copy(&stdout, outRead); drained <- struct{}{} }()
	got := <-code
	restore()
	<-fillDone
	_ = outWrite.Close()
	_ = errWrite.Close()
	<-drained
	<-drained
	text := stdout.String()
	if got != 1 || !strings.Contains(stderr.String(), pilock.ErrLocked.Error()) || strings.Contains(text, "Removed") {
		t.Errorf("pig remove with the settings lock taken after its read: code=%d stderr=%q, want exit 1, %q and no Removed line", got, stderr.String(), pilock.ErrLocked)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil || !strings.Contains(string(data), "first-pkg") {
		t.Errorf("settings.json = %s (%v), want the package still listed: the save failed", data, err)
	}
}
