//go:build parity

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// transcriptHoldEnv makes the transcript reader wait, before it reads anything, on a tmux channel: "<socket> <channel>". A test sets it before the private tmux server starts, so that the server's environment, and the reader's with it, carries it. TestMain calls holdTranscriptReader.
const transcriptHoldEnv = "PIG_PARITY_TRANSCRIPT_HOLD"

// holdTranscriptReader blocks until the channel named by spec is signalled (tmux wait-for -S). A signal that comes first is kept.
func holdTranscriptReader(spec string) {
	fields := strings.Fields(spec)
	if len(fields) != 2 {
		return
	}
	_ = exec.Command("tmux", "-L", fields[0], "wait-for", fields[1]).Run()
}

// heldReader is a private tmux server whose transcript readers wait for release before they read anything, and the temp directory the runs under test make their roots in.
type heldReader struct {
	root    string
	release func()
}

func holdTheTranscriptReader(t *testing.T) heldReader {
	t.Helper()
	skipWithoutTmux(t)
	root := t.TempDir()
	socket := "pig-parity-held-reader-" + uniqueID()
	hold := "pig-parity-hold-" + uniqueID()
	t.Setenv("TMPDIR", root)
	t.Setenv(transcriptHoldEnv, socket+" "+hold)
	useTmuxSocket(t, socket)
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if out, err := exec.Command("tmux", tmuxArgs("wait-for", "-S", hold)...).CombinedOutput(); err != nil {
				t.Errorf("release the transcript reader: %v: %s", err, out)
			}
		})
	}
	t.Cleanup(release)
	return heldReader{root: root, release: release}
}

// runRoots lists the temp roots the runs of heldReaderScenario made.
func (h heldReader) runRoots() []string {
	roots, _ := filepath.Glob(filepath.Join(h.root, "parity-tmux-held-reader-pig-*"))
	return roots
}

func heldReaderScenario(t *testing.T) (BinaryRef, *Scenario) {
	t.Helper()
	bin := BinaryRef{Label: "pig", Path: "sh", Args: []string{"-c", "printf 'READY\\n'; sleep 999"}}
	return bin, &Scenario{Name: "held-reader", Tmux: TmuxDriverConfig{Width: 80, Height: 8, ReadyPatternPig: "READY", ReadyTimeoutSeconds: int(testbudget.Wait(t) / time.Second)}}
}

// The transcript reader is a child of the tmux server, not of the pane, and it writes its trailer into the run's temp root when tmux closes the pane's pipe, which the teardown's kill does. A run that ended once the pane's processes had exited left the reader free to create the trailer while the root was being removed (unlinkat: directory not empty). Here the reader cannot read, so it cannot finish, until the teardown's wait for it has begun: the run must wait, and so must end with the trailer in its root. Run without the wait, it ends with the reader still held and no trailer.
func TestTmuxRunWaitsForTheTranscriptReaderBeforeItEnds(t *testing.T) {
	held := holdTheTranscriptReader(t)
	driver := tmuxDriver{teardownClock: captureClock{
		now: time.Now,
		wait: func(ctx context.Context, d time.Duration) error {
			held.release()
			return wallClock.wait(ctx, d)
		},
	}}
	bin, sc := heldReaderScenario(t)
	t.Run("run", func(t *testing.T) {
		result := driver.Run(t.Context(), t, bin, sc)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		trailers, _ := filepath.Glob(filepath.Join(held.root, "parity-tmux-held-reader-pig-*", "terminal-transcript.log"+transcriptDoneSuffix))
		if len(trailers) != 1 {
			t.Fatalf("the run ended with %d transcript trailers in its temp root, want 1: it did not wait for its reader", len(trailers))
		}
	})
	if left := held.runRoots(); len(left) != 0 {
		t.Fatalf("temp roots left after the run: %v", left)
	}
}

// A reader that does not finish within the budget fails the run with an error that names it, and its temp root stays: removing the directory under a writer that is still alive would add a second, unrelated error to the first. The clock here spends the budget at once and the reader stays held.
func TestTmuxRunKeepsItsTempRootFromAReaderThatDoesNotFinish(t *testing.T) {
	held := holdTheTranscriptReader(t)
	driver := tmuxDriver{teardownClock: (&fakeClock{}).clock()}
	bin, sc := heldReaderScenario(t)
	t.Run("run", func(t *testing.T) {
		result := driver.Run(t.Context(), t, bin, sc)
		if !errors.Is(result.Err, errTranscriptReaderLive) || !strings.Contains(result.Err.Error(), "transcript reader did not finish: held-reader pig: no trailer after ") {
			t.Fatalf("run error = %v, want the reader that did not finish named", result.Err)
		}
	})
	roots := held.runRoots()
	if len(roots) != 1 {
		t.Fatalf("temp roots after the run = %v, want the one it kept", roots)
	}
	// Let the reader go and finish before the directory is removed under it.
	held.release()
	if err := awaitTranscriptDone(filepath.Join(roots[0], "terminal-transcript.log"), "held-reader", "pig", testbudget.Wait(t)); err != nil {
		t.Fatalf("after the reader was let go: %v", err)
	}
}

// The teardown waits for a transcript reader only when one was started, and a wait that ends without its trailer is an error that names the reader and is bounded by the budget.
func TestTmuxTeardownWaitsOnlyForAReaderThatWasStarted(t *testing.T) {
	budget := processExitTimeout(t)
	finished := filepath.Join(t.TempDir(), "terminal-transcript.log")
	if err := os.WriteFile(finished, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finished+transcriptDoneSuffix, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		path          string
		readerStarted bool
		wantLive      bool
		wantWaits     bool
	}{
		{"no reader started", filepath.Join(t.TempDir(), "terminal-transcript.log"), false, false, false},
		{"reader finished", finished, true, false, false},
		{"reader not finished", filepath.Join(t.TempDir(), "terminal-transcript.log"), true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{}
			err := tmuxTeardown{
				session: "parity-teardown-" + uniqueID(), transcriptPath: tc.path, scenario: "teardown", side: "pig",
				readerStarted: tc.readerStarted, clock: clock.clock(), budget: budget,
			}.end(t.Context())
			if live := errors.Is(err, errTranscriptReaderLive); live != tc.wantLive || (err != nil) != tc.wantLive {
				t.Fatalf("teardown error = %v, want a reader that did not finish: %t", err, tc.wantLive)
			}
			if tc.wantLive && !strings.Contains(err.Error(), "transcript reader did not finish: teardown pig: no trailer after ") {
				t.Fatalf("teardown error = %v, want the reader named", err)
			}
			if waited := clock.waits > 0; waited != tc.wantWaits {
				t.Fatalf("waited %d times, want waits: %t", clock.waits, tc.wantWaits)
			}
			if tc.wantWaits && clock.t.Sub(time.Time{}) < budget {
				t.Fatalf("gave up after %s, want the whole budget %s", clock.t.Sub(time.Time{}), budget)
			}
		})
	}
}

// startPipedPane starts a session whose pane stays alive and, when reader is not empty, makes it the pane's pipe the way startTmuxSession does for the transcript.
func startPipedPane(t *testing.T, reader string) string {
	t.Helper()
	session := "parity-teardown-" + uniqueID()
	registerSession(session)
	t.Cleanup(func() { killSession(session) })
	if out, err := exec.Command("tmux", tmuxArgs(createTmuxSessionArgs(session, "sleep 999", TmuxDriverConfig{Width: 80, Height: 8})...)...).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	if reader != "" {
		if out, err := exec.Command("tmux", tmuxArgs("pipe-pane", "-o", "-t", session, reader)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux pipe-pane: %v: %s", err, out)
		}
	}
	return session
}

// A start that succeeded ran its pipe-pane. A start that failed after the session was created may or may not have: the pane says, and a session that does not exist started no reader.
func TestTranscriptReaderAttachedAfterAFailedStart(t *testing.T) {
	skipWithoutTmux(t)
	useTmuxSocket(t, "pig-parity-attached-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	failed := errors.New("start failed")
	if !transcriptReaderAttached("parity-no-such-session", nil) {
		t.Fatal("a start that succeeded ran its pipe-pane, which starts the reader")
	}
	if transcriptReaderAttached("parity-no-such-session", failed) {
		t.Fatal("a session that does not exist started no reader")
	}
	if transcriptReaderAttached(startPipedPane(t, ""), failed) {
		t.Fatal("a pane without a pipe has no reader")
	}
	if !transcriptReaderAttached(startPipedPane(t, "cat >/dev/null"), failed) {
		t.Fatal("a pane with a pipe has a reader")
	}
}

// A run that asks for its root to be kept leaves it; any other run removes it.
func TestTokenContextKeepsARootItWasToldToKeep(t *testing.T) {
	var kept, removed string
	t.Run("kept", func(t *testing.T) {
		tc := newTokenContext(t, "keep")
		kept = tc.tempRoot
		tc.keep("a writer may still be using it")
	})
	t.Cleanup(func() { _ = os.RemoveAll(kept) })
	t.Run("removed", func(t *testing.T) {
		removed = newTokenContext(t, "remove").tempRoot
	})
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the root of a run that asked to keep it: %v", err)
	}
	if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the root of a run that did not ask to keep it: %v, want it removed", err)
	}
}
