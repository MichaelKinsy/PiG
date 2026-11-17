//go:build parity

package runner

import (
	"bytes"
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

// The terminal transcript is the byte stream from the program's first write. Terminal mode setup (alternate screen, bracketed paste, the keyboard protocol queries, the first frame) is written at startup, and a transcript that begins after it makes the waits that read the stream see a run that Pi's transcript, started a little differently on a slower or faster host, does not. The program writes the marker as its first bytes, once staying and once exiting at once; the session is started by the driver's own start path, and the marker must be the whole transcript every time.
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
	}{
		{"stays", "printf " + firstBytesMarker + "; sleep 30"},
		{"exits at once", "printf " + firstBytesMarker},
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
					if msg := startAndReadTranscript(dir, program.command); msg != "" {
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

// startAndReadTranscript starts a session that runs command through startTmuxSession and returns what is wrong with its transcript, or "". The pipe's reader copies what the pane wrote as it arrives, so the file is polled until it holds the marker; a transcript that is still empty or short after the deadline never will.
func startAndReadTranscript(dir, command string) string {
	ctx := context.Background()
	session := "parity-first-bytes-" + uniqueID()
	registerSession(session)
	defer killSession(session)
	path := filepath.Join(dir, session+".log")
	if _, _, err := startTmuxSession(ctx, session, command, path, TmuxDriverConfig{Width: 80, Height: 24}); err != nil {
		return "start: " + err.Error()
	}
	var got []byte
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		got, _ = os.ReadFile(path)
		if bytes.Equal(got, []byte(firstBytesMarker)) {
			return ""
		}
	}
	return fmt.Sprintf("transcript = %q, want %q", got, firstBytesMarker)
}
