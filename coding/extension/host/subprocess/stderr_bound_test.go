package subprocess

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func newTestStderrLog(t *testing.T) *processStderrLog {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	return &processStderrLog{path: file.Name(), writer: file}
}

// A log under the head bound is the bytes written, with nothing added.
func TestStderrLogUnderTheBoundIsVerbatim(t *testing.T) {
	log := newTestStderrLog(t)
	for _, line := range []string{"one\n", "two\n", "three\n"} {
		if n, err := log.Write([]byte(line)); n != len(line) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	log.closeWriter()
	got, err := os.ReadFile(log.path)
	if err != nil || string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("log = %q, %v", got, err)
	}
}

// A flood keeps the first stderrHeadMax bytes (a startup failure) and the last
// stderrTailMax bytes (a crash), names how many it dropped, and the file stays
// bounded. Every Write reports success, so the pipe it drains never stops.
func TestStderrLogKeepsHeadAndTail(t *testing.T) {
	log := newTestStderrLog(t)
	var all bytes.Buffer
	for i := 0; all.Len() < 4*stderrHeadMax; i++ {
		line := fmt.Sprintf("line %07d %s\n", i, strings.Repeat("x", 100))
		all.WriteString(line)
		if n, err := log.Write([]byte(line)); n != len(line) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	log.closeWriter()
	got, err := os.ReadFile(log.path)
	if err != nil {
		t.Fatal(err)
	}
	if limit := stderrHeadMax + stderrTailMax + 256; len(got) > limit {
		t.Fatalf("log is %d bytes, want at most %d", len(got), limit)
	}
	flood := all.Bytes()
	if !bytes.HasPrefix(got, flood[:stderrHeadMax]) {
		t.Fatal("the log lost the head of the output")
	}
	if !bytes.HasSuffix(got, flood[len(flood)-stderrTailMax:]) {
		t.Fatal("the log lost the tail of the output")
	}
	marker := fmt.Sprintf("[... %d bytes of extension stderr dropped ...]", len(flood)-stderrHeadMax-stderrTailMax)
	if !bytes.Contains(got, []byte(marker)) {
		t.Fatalf("the log does not say what it dropped (want %q)", marker)
	}
}

// A Write after the log closed is dropped without an error.
func TestStderrLogWriteAfterCloseIsDropped(t *testing.T) {
	log := newTestStderrLog(t)
	log.closeWriter()
	if n, err := log.Write([]byte("late")); n != 4 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
}

const floodHelperEnv = "PIG_TEST_FLOOD_STDERR"

// TestHelperFloodStderr is the child of TestFloodingChildNeitherBlocksNorFillsTheDisk: it writes floodHelperEnv bytes to stderr.
func TestHelperFloodStderr(t *testing.T) {
	var size int
	if _, err := fmt.Sscan(os.Getenv(floodHelperEnv), &size); err != nil || size == 0 {
		t.Skip("helper process")
	}
	line := []byte(strings.Repeat("flood ", 20) + "\n")
	for written := 0; written < size; written += len(line) {
		_, _ = os.Stderr.Write(line)
	}
	_, _ = os.Stderr.WriteString("final crash line\n")
}

// A child that writes far more stderr than the log bound finishes, its pipe
// drained, and the log keeps the bounded head and the crash line at the tail.
func TestFloodingChildNeitherBlocksNorFillsTheDisk(t *testing.T) {
	log := newTestStderrLog(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperFloodStderr$")
	cmd.Env = append(os.Environ(), floodHelperEnv+"=40000000")
	cmd.Stderr = log
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		t.Fatalf("flooding child: %v", err)
	}
	log.closeWriter()
	got, err := os.ReadFile(log.path)
	if err != nil {
		t.Fatal(err)
	}
	if limit := stderrHeadMax + stderrTailMax + 256; len(got) > limit {
		t.Fatalf("log is %d bytes for a 40 MB flood, want at most %d", len(got), limit)
	}
	if !bytes.HasSuffix(got, []byte("final crash line\n")) {
		t.Fatalf("the crash line at the end of stderr is missing; log ends %q", got[max(0, len(got)-64):])
	}
}
