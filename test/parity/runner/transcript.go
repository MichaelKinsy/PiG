//go:build parity

package runner

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"context"
)

// transcriptCopyEnv names the file the transcript reader appends to. tmux pipe-pane starts the test binary with it set, and TestMain turns that process into the reader (copyTranscript) instead of running tests.
const transcriptCopyEnv = "PIG_PARITY_TRANSCRIPT_COPY"

// transcriptDoneSuffix is appended to the transcript's path for the trailer the reader writes at end of input: the number of bytes it appended.
const transcriptDoneSuffix = ".done"

// transcriptDoneTimeout bounds the wait for the reader's trailer once the pane's pipe is closed: the reader only has to see the end of its input.
const transcriptDoneTimeout = 5 * time.Second

// transcriptReaderCommand is the shell command tmux pipe-pane starts to append everything the pane's program writes to path. The reader is this test binary and not a host utility: on Ubuntu 26.04 /usr/bin/cat is uutils coreutils 0.8.0, which copies one chunk of pipe-pane's socket and then waits, so the rest of a run's output never reached the file (TestTranscriptReaderKeepsEveryChunkWhenCatStalls).
func transcriptReaderCommand(path string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the test binary that reads the transcript: %w", err)
	}
	return transcriptCopyEnv + "=" + shellQuote(path) + " " + shellQuote(exe), nil
}

// copyTranscript appends everything read from in to the file at path, one write per read so nothing waits in a buffer, and on end of input writes the trailer with the byte count. It returns the process exit status.
func copyTranscript(path string, in io.Reader, diag io.Writer) int {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(diag, "transcript reader: %v\n", err)
		return 1
	}
	buf := make([]byte, 64<<10)
	var total int64
	for {
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				_, _ = fmt.Fprintf(diag, "transcript reader: %v\n", err)
				return 1
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_, _ = fmt.Fprintf(diag, "transcript reader: %v\n", readErr)
			return 1
		}
	}
	if err := f.Close(); err != nil {
		_, _ = fmt.Fprintf(diag, "transcript reader: %v\n", err)
		return 1
	}
	done := path + transcriptDoneSuffix
	if err := os.WriteFile(done+".tmp", []byte(strconv.FormatInt(total, 10)+"\n"), 0o600); err != nil {
		_, _ = fmt.Fprintf(diag, "transcript reader: %v\n", err)
		return 1
	}
	if err := os.Rename(done+".tmp", done); err != nil {
		_, _ = fmt.Fprintf(diag, "transcript reader: %v\n", err)
		return 1
	}
	return 0
}

// finishTranscript closes the session's pipe, so the reader sees end of input, and returns once the reader has reported that it wrote every byte it read. A comparator reads the transcript only after this returns nil. The pipe is the transcript's only reader of the pane: the frame watch and the final capture read the screen with capture-pane and are not affected.
func finishTranscript(ctx context.Context, session, path, scenario, side string, timeout time.Duration) error {
	//nolint:gosec // G204 (class a): program is literal tmux; args are fixed pipe-pane -t <harness session> with no command, which closes the pipe.
	if out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("pipe-pane", "-t", session)...).CombinedOutput(); err != nil {
		return fmt.Errorf("transcript reader did not finish: %s %s: close the pipe: %w: %s", scenario, side, err, strings.TrimSpace(string(out)))
	}
	return awaitTranscriptDone(path, scenario, side, timeout)
}

// awaitTranscriptDone waits for the reader's trailer and checks the transcript holds exactly the bytes the trailer counts.
func awaitTranscriptDone(path, scenario, side string, timeout time.Duration) error {
	prefix := "transcript reader did not finish: " + scenario + " " + side + ": "
	deadline := time.Now().Add(timeout)
	for {
		raw, err := os.ReadFile(path + transcriptDoneSuffix)
		if err == nil {
			want, parseErr := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
			if parseErr != nil {
				return errors.New(prefix + "malformed trailer " + strconv.Quote(string(raw)))
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				return errors.New(prefix + statErr.Error())
			}
			if info.Size() != want {
				return fmt.Errorf("%sthe reader wrote %d bytes and the transcript holds %d", prefix, want, info.Size())
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return errors.New(prefix + err.Error())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%sno trailer after %s", prefix, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
