package codingagent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// output-guard tests. We verify isolation (stdout
// writes redirect to stderr) by capturing both fds via a pipe
// stitched-up replacement of os.Stderr.

// withCapturedStderr swaps os.Stderr for a pipe and returns a
// func that restores + returns whatever was written to it. Used
// to assert the output-guard's stdout→stderr redirect lands in
// the expected place.
func withCapturedStderr(t *testing.T) (restore func() string) {
	t.Helper()
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	var buf bytes.Buffer
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		mu.Lock()
		_, _ = io.Copy(&buf, r)
		mu.Unlock()
		close(done)
	}()
	return func() string {
		os.Stderr = origStderr
		_ = w.Close()
		<-done
		_ = r.Close()
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

func TestOutputGuardRedirectsStdoutToStderr(t *testing.T) {
	getStderr := withCapturedStderr(t)

	if err := TakeOverStdout(); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if !IsStdoutTakenOver() {
		t.Errorf("IsStdoutTakenOver=false after takeover")
	}

	// fmt.Println resolves os.Stdout each call → our replacement
	// pipe receives this; the goroutine ferries it to os.Stderr,
	// which we've captured.
	fmt.Println("rogue tool log line")

	RestoreStdout()
	if IsStdoutTakenOver() {
		t.Errorf("IsStdoutTakenOver=true after restore")
	}

	got := getStderr()
	if !strings.Contains(got, "rogue tool log line") {
		t.Errorf("redirected line not in stderr capture:\n%q", got)
	}
}

func TestOutputGuardWriteRawStdoutBypassesRedirect(t *testing.T) {
	// Capture stdout via a separate pipe BEFORE TakeOverStdout
	// runs (so the saved-original points at our capture pipe,
	// not the real terminal).
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	var stdoutBuf bytes.Buffer
	stdoutDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stdoutBuf, r)
		close(stdoutDone)
	}()

	getStderr := withCapturedStderr(t)

	if err := TakeOverStdout(); err != nil {
		t.Fatalf("takeover: %v", err)
	}

	// Rogue write → goes to stderr.
	fmt.Println("rogue")
	// Explicit raw write → goes to *original* stdout (our capture pipe).
	_, _ = WriteRawStdout("framed-output\n")

	RestoreStdout()
	stderrOut := getStderr()
	os.Stdout = origStdout
	_ = w.Close()
	<-stdoutDone
	_ = r.Close()

	if !strings.Contains(stderrOut, "rogue") {
		t.Errorf("rogue not in stderr:\n%q", stderrOut)
	}
	if strings.Contains(stderrOut, "framed-output") {
		t.Errorf("framed-output leaked to stderr:\n%q", stderrOut)
	}
	if !strings.Contains(stdoutBuf.String(), "framed-output") {
		t.Errorf("framed-output not in stdout:\n%q", stdoutBuf.String())
	}
}

func TestOutputGuardTakeoverIsIdempotent(t *testing.T) {
	getStderr := withCapturedStderr(t)
	defer getStderr() // drain even on test failure

	if err := TakeOverStdout(); err != nil {
		t.Fatalf("first takeover: %v", err)
	}
	defer RestoreStdout()
	if err := TakeOverStdout(); err != nil {
		t.Fatalf("second takeover: %v", err)
	}
	if !IsStdoutTakenOver() {
		t.Errorf("not taken over after double-takeover")
	}
}

func TestOutputGuardRestoreWithoutTakeoverIsNoOp(t *testing.T) {
	// Should not panic / not deadlock.
	RestoreStdout()
	if IsStdoutTakenOver() {
		t.Errorf("IsStdoutTakenOver=true after no-op restore")
	}
}

func TestWriteRawStdoutWithoutTakeoverWritesToOsStdout(t *testing.T) {
	// When no takeover is active, WriteRawStdout writes directly
	// to os.Stdout (we capture it via pipe replacement here).
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	_, _ = WriteRawStdout("plain stdout write\n")

	os.Stdout = origStdout
	_ = w.Close()
	<-done
	_ = r.Close()

	if !strings.Contains(buf.String(), "plain stdout write") {
		t.Errorf("WriteRawStdout without takeover didn't reach stdout:\n%q", buf.String())
	}
}
