//go:build parity

package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The transcript reader must not depend on the host's cat. On Ubuntu 26.04 /usr/bin/cat is uutils coreutils 0.8.0: reading tmux pipe-pane's socket it copies the first chunk and then waits in the kernel for more, so every later byte of the stream (the last frames of a run) never reached the file and the final_frame comparators read a truncated transcript. This test puts a cat with that behavior first in the PATH of a private tmux server and requires every chunk the pane writes to reach the file, and the reader to report, once the pipe is closed, that it wrote them all.
func TestTranscriptReaderKeepsEveryChunkWhenCatStalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tmux driver is not used on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	bin := t.TempDir()
	stall := "#!/bin/sh\nhead -c1 >/dev/null\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "cat"), []byte(stall), 0o755); err != nil { //nolint:gosec // a fixture script must be executable.
		t.Fatal(err)
	}
	socket := fmt.Sprintf("pig-parity-transcript-%d", os.Getpid())
	tmux := func(args ...string) *exec.Cmd {
		//nolint:gosec // G204 (class a): program is literal tmux; args are fixed options and test-local names.
		cmd := exec.Command("tmux", append([]string{"-L", socket, "-f", os.DevNull}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd
	}
	if out, err := tmux("new-session", "-d", "-s", "t", "-x", "80", "-y", "24", "sleep 0.5; printf one-; sleep 0.3; printf two-; sleep 0.3; printf three-; sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = tmux("kill-session", "-t", "t").Run() })
	path := filepath.Join(t.TempDir(), "terminal-transcript.log")
	command, err := transcriptReaderCommand(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tmux("pipe-pane", "-o", "-t", "t", command).CombinedOutput(); err != nil {
		t.Fatalf("tmux pipe-pane: %v: %s", err, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "one-two-three-") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcript = %q after 5s, want it to hold every chunk the pane wrote (one-two-three-)", raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if out, err := tmux("pipe-pane", "-t", "t").CombinedOutput(); err != nil {
		t.Fatalf("tmux pipe-pane (close): %v: %s", err, out)
	}
	if err := awaitTranscriptDone(path, "scenario", "pi", 5*time.Second); err != nil {
		t.Fatalf("after the pipe was closed: %v", err)
	}
}

// The reader writes each read at once and, only at end of input, the trailer with the count of bytes it wrote.
func TestCopyTranscriptWritesEachReadAndATrailerAtEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal-transcript.log")
	in, out := io.Pipe()
	status := make(chan int, 1)
	var diag strings.Builder
	go func() { status <- copyTranscript(path, in, &diag) }()
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			raw, _ := os.ReadFile(path)
			if string(raw) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("transcript = %q, want %q", raw, want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if _, err := out.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	waitFor("ab")
	if _, err := os.Stat(path + transcriptDoneSuffix); err == nil {
		t.Fatal("the trailer exists before end of input")
	}
	if _, err := out.Write([]byte("cde")); err != nil {
		t.Fatal(err)
	}
	waitFor("abcde")
	_ = out.Close()
	if code := <-status; code != 0 {
		t.Fatalf("exit status %d on end of input: %s", code, diag.String())
	}
	if got, _ := os.ReadFile(path + transcriptDoneSuffix); string(got) != "5\n" {
		t.Fatalf("trailer = %q, want \"5\\n\"", got)
	}
}

// A comparator reads a transcript only after the reader finished: a missing trailer, a malformed one and a count that differs from the file's size are each a named error.
func TestAwaitTranscriptDone(t *testing.T) {
	for name, tc := range map[string]struct {
		file, trailer string
		haveTrailer   bool
		want          string
	}{
		"complete":      {file: "abc", trailer: "3\n", haveTrailer: true},
		"no trailer":    {file: "abc", want: "transcript reader did not finish: 68-x pi: no trailer after "},
		"short file":    {file: "ab", trailer: "3\n", haveTrailer: true, want: "transcript reader did not finish: 68-x pi: the reader wrote 3 bytes and the transcript holds 2"},
		"long file":     {file: "abcd", trailer: "3\n", haveTrailer: true, want: "transcript reader did not finish: 68-x pi: the reader wrote 3 bytes and the transcript holds 4"},
		"malformed":     {file: "abc", trailer: "x", haveTrailer: true, want: "transcript reader did not finish: 68-x pi: malformed trailer"},
		"missing  file": {trailer: "0\n", haveTrailer: true, want: "transcript reader did not finish: 68-x pi: "},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "terminal-transcript.log")
			if tc.file != "" {
				if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.haveTrailer {
				if err := os.WriteFile(path+transcriptDoneSuffix, []byte(tc.trailer), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := awaitTranscriptDone(path, "68-x", "pi", 30*time.Millisecond)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
				t.Fatalf("error = %v, want prefix %q", err, tc.want)
			}
		})
	}
}
