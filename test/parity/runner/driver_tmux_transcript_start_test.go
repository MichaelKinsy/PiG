//go:build parity

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// firstBytesMarker is what the programs below write before anything else.
const firstBytesMarker = "FIRST-BYTES-MARKER"

// firstBytesRuns is how many sessions each program starts. A program that is first to write 2 ms after its session exists loses its first bytes to a late attach in most runs, and a Go binary such as pig writes its first terminal setup within 20 ms of its start, so one run proves nothing.
const firstBytesRuns = 200

func skipWithoutTmux(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the tmux driver is not used on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

// The terminal transcript is the byte stream from the program's first write. Terminal mode setup (alternate screen, bracketed paste, the keyboard protocol queries, the first frame) is written at startup, and a transcript that begins after it makes the waits and comparators that read the stream see a run that Pi's transcript, started a little differently on a slower or faster host, does not. The program writes the marker as its first bytes, once staying and once exiting at once; the session is started by the driver's own start path, and the marker must be the whole transcript every time.
func TestTranscriptHoldsTheProgramsFirstBytes(t *testing.T) {
	skipWithoutTmux(t)
	useTmuxSocket(t, "pig-parity-first-bytes-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	for _, program := range []struct {
		name    string
		command string
		exits   bool
	}{
		{"stays", "printf " + firstBytesMarker + "; sleep 30", false},
		{"exits at once", "printf " + firstBytesMarker, true},
	} {
		t.Run(program.name, func(t *testing.T) {
			dir := t.TempDir()
			var (
				wg       sync.WaitGroup
				mu       sync.Mutex
				failures []string
				slots    = make(chan struct{}, 8)
			)
			for range firstBytesRuns {
				slots <- struct{}{}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-slots }()
					if msg := startAndReadTranscript(dir, program.command, program.exits); msg != "" {
						mu.Lock()
						failures = append(failures, msg)
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if len(failures) > 0 {
				t.Fatalf("%d of %d runs did not hold the program's first bytes; first: %s", len(failures), firstBytesRuns, failures[0])
			}
		})
	}
}

// startAndReadTranscript starts a session that runs command through startTmuxSession and returns what is wrong with its finished transcript, or "".
func startAndReadTranscript(dir, command string, exits bool) string {
	ctx := context.Background()
	session := "parity-first-bytes-" + uniqueID()
	registerSession(session)
	defer killSession(session)
	path := filepath.Join(dir, session+".log")
	if _, _, err := startTmuxSession(ctx, session, command, path, TmuxDriverConfig{Width: 80, Height: 24}); err != nil {
		return "start: " + err.Error()
	}
	if err := finishedTranscript(ctx, session, path, exits); err != nil {
		return err.Error()
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	if string(got) != firstBytesMarker {
		return fmt.Sprintf("transcript = %q, want %q", got, firstBytesMarker)
	}
	return ""
}

// finishedTranscript waits until the transcript's reader has written everything the pane produced: the reader's trailer once the pane's program has exited, or the marker on the screen and then the closed pipe's trailer.
func finishedTranscript(ctx context.Context, session, path string, exits bool) error {
	if exits {
		return awaitTranscriptDone(path, "first-bytes", "pig", 10*time.Second)
	}
	if !waitForPaneState(ctx, session, []string{firstBytesMarker}, nil, 10*time.Second) {
		return fmt.Errorf("the program's marker never reached the screen")
	}
	return finishTranscript(ctx, session, path, "first-bytes", "pig", 10*time.Second)
}

// The first-bytes test passes only if it can fail. Here the pipe is attached the old way, after the program has written and the session shows it: the marker is on the screen and the transcript does not hold it.
func TestTranscriptAttachedAfterTheFirstBytesLacksThem(t *testing.T) {
	skipWithoutTmux(t)
	useTmuxSocket(t, "pig-parity-late-attach-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	ctx := context.Background()
	session := "parity-late-attach-" + uniqueID()
	registerSession(session)
	t.Cleanup(func() { killSession(session) })
	if out, err := exec.Command("tmux", tmuxArgs(createTmuxSessionArgs(session, "printf "+firstBytesMarker+"; sleep 30", TmuxDriverConfig{Width: 80, Height: 24})...)...).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	if !waitForPaneState(ctx, session, []string{firstBytesMarker}, nil, 10*time.Second) {
		t.Fatal("the program's marker never reached the screen")
	}
	path := filepath.Join(t.TempDir(), "terminal-transcript.log")
	reader, err := transcriptReaderCommand(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("tmux", tmuxArgs("pipe-pane", "-o", "-t", session, reader)...).CombinedOutput(); err != nil {
		t.Fatalf("tmux pipe-pane: %v: %s", err, out)
	}
	if err := finishTranscript(ctx, session, path, "late-attach", "pig", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || len(got) != 0 {
		t.Fatalf("transcript = %q (%v), want empty: the pipe was attached after the marker was written", got, err)
	}
}
