package codingagent

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// freshEnding gives a test its own process-wide ending record and restores the previous one.
func freshEnding(t *testing.T) {
	t.Helper()
	saved := ending
	ending = newProcessEnding()
	t.Cleanup(func() { ending = saved })
}

func exitLogLines(agentDir string) []string {
	text := strings.TrimSuffix(exitLogText(agentDir), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// Every path that sees the process end calls RecordEnding; one line comes out (exit_ending.go, step 1).
func TestRecordEndingLeavesOneLineForConcurrentEndings(t *testing.T) {
	freshEnding(t)
	agentDir := t.TempDir()
	const callers = 16
	var wg sync.WaitGroup
	wins := make(chan string, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Go(func() {
			reason := fmt.Sprintf("ending %d", i)
			<-start
			if RecordEnding(agentDir, reason) {
				wins <- reason
			}
		})
	}
	close(start)
	wg.Wait()
	close(wins)
	if len(wins) != 1 {
		t.Fatalf("%d callers own the record, want 1", len(wins))
	}
	winner := <-wins
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.HasSuffix(lines[0], " "+winner) {
		t.Fatalf("exit.log = %q, want one line naming %q", exitLogText(agentDir), winner)
	}
}

// holdOwner starts the owner of the record and holds it before its write until release is called. The owner blocks on a
// channel, which a synctest bubble sees as durably blocked, so the checks that follow do not depend on scheduling.
func holdOwner(agentDir string) (release func()) {
	gate := make(chan struct{})
	ending.hold = gate
	go RecordEnding(agentDir, "owner")
	synctest.Wait()
	return func() { close(gate) }
}

// A caller that lost the claim returns only after the owner's line is on disk (step 4): os.Exit after RecordEnding never
// overtakes the record. A release of the claim before the write (the owner closing `flushed` early) fails here on every run.
func TestRecordEndingLoserBlocksUntilTheOwnersLineIsOnDisk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshEnding(t)
		agentDir := t.TempDir()
		release := holdOwner(agentDir)
		var loserWon, returned bool
		var linesAtReturn int
		var loser sync.WaitGroup
		loser.Go(func() {
			loserWon = RecordEnding(agentDir, "loser")
			linesAtReturn = len(exitLogLines(agentDir))
			returned = true
		})
		synctest.Wait()
		if returned {
			t.Fatal("the loser returned while the owner was still writing")
		}
		release()
		loser.Wait()
		if loserWon || linesAtReturn != 1 {
			t.Fatalf("loser won = %v, lines on disk when it returned = %d; want false and 1", loserWon, linesAtReturn)
		}
	})
}

// BeforeExit waits for the line another path has in flight, and returns at once when nothing is ending.
func TestBeforeExitWaitsForTheLineInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshEnding(t)
		agentDir := t.TempDir()
		began := time.Now()
		BeforeExit()
		if time.Since(began) != 0 {
			t.Fatal("BeforeExit waited with no ending in flight")
		}
		release := holdOwner(agentDir)
		var returned bool
		var waiter sync.WaitGroup
		waiter.Go(func() {
			BeforeExit()
			returned = len(exitLogLines(agentDir)) == 1
		})
		synctest.Wait()
		if returned {
			t.Fatal("BeforeExit returned before the line was on disk")
		}
		release()
		waiter.Wait()
		if !returned {
			t.Fatal("BeforeExit returned without the line on disk")
		}
	})
}

// parkedForever counts goroutines blocked in `select {}`, which is where a second emergency exit and a path that yields
// to a running emergency exit wait for the first to end the process.
func parkedForever() int {
	buf := make([]byte, 1<<20)
	return strings.Count(string(buf[:runtime.Stack(buf, true)]), "[select (no cases)")
}

// waitParked yields until n goroutines are parked forever. It waits on state, not on time.
func waitParked(n int) {
	for parkedForever() < n {
		runtime.Gosched()
	}
}

// emergencyChildHolding starts an emergency exit that owns the record and is held inside its write, and returns the
// release that lets it finish. The child test then drives a second ending while the first is in flight.
func emergencyChildHolding(m *InteractiveMode) (release func()) {
	gate := make(chan struct{})
	ending.hold = gate
	go m.emergencyTerminalExit("terminal gone: write /dev/stdout: input/output error")
	for !emergencyExiting.Load() || !ending.claimed() {
		runtime.Gosched()
	}
	return func() { close(gate) }
}

func emergencyChildMode(agentDir string) *InteractiveMode {
	return NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
}

// Concurrent emergency exits run once: one line, exit 129 (step 2).
func TestConcurrentEmergencyExitsLeaveOneLine(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "dead-terminal-concurrent", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.Contains(lines[0], "terminal gone: path ") || !strings.HasSuffix(lines[0], "(exit 129)") {
		t.Fatalf("exit.log = %q, want one terminal-gone line", exitLogText(agentDir))
	}
}

// A signal that already owns the record keeps it: the dead terminal's exit adds no line and still exits 129.
func TestDeadTerminalExitAfterTheSignalAddsNoLine(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "dead-terminal-after-signal", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.HasSuffix(lines[0], " received SIGHUP") {
		t.Fatalf("exit.log = %q, want only received SIGHUP", exitLogText(agentDir))
	}
}

// Pi's emergencyTerminalExit is `never`. A second dead-terminal exit while the first is in flight (a crash on a dead
// terminal in a goroutine the owner loop does not run) blocks behind it and does not carry on to exit 1 with an empty
// exit.log, which is what a returning second caller did (the review of 818ba64253: 5 of 5 runs). The scenario holds the
// first exit inside its write until the second is parked, so the order does not depend on scheduling.
func TestSecondDeadTerminalExitBlocksBehindTheFirst(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "dead-terminal-while-emergency", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.HasSuffix(lines[0], "(exit 129)") || !strings.Contains(lines[0], "terminal gone: write /dev/stdout") {
		t.Fatalf("exit.log = %q, want the first exit's one line", exitLogText(agentDir))
	}
}

// emergencyTerminalExit never returns, however many callers there are: a returning second caller is what ended the
// process with status 1 in the review of 818ba64253.
func TestSecondEmergencyExitNeverReturns(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "second-emergency-never-returns", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129 (3 means the second caller returned)\n%s", code, output)
	}
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.Contains(lines[0], "terminal gone: write /dev/stdout") {
		t.Fatalf("exit.log = %q, want the first exit's one line", exitLogText(agentDir))
	}
}

// Any other path that ends the process (a crash that is not a dead terminal exits 1) yields to a running emergency exit
// (step 3): the status is Pi's 129 and the first exit's line is complete.
func TestCrashExitYieldsToARunningEmergencyExit(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "crash-while-emergency", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.HasSuffix(lines[0], "(exit 129)") {
		t.Fatalf("exit.log = %q, want one terminal-gone line", exitLogText(agentDir))
	}
}

// The extension-requested shutdown and a dead terminal are two endings that can race; the first claim leaves the only line.
func TestExtensionShutdownAndDeadTerminalLeaveOneLine(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "extension-shutdown-then-dead-terminal", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if lines := exitLogLines(agentDir); len(lines) != 1 || !strings.Contains(lines[0], "extension requested shutdown") {
		t.Fatalf("exit.log = %q, want only the extension shutdown", exitLogText(agentDir))
	}
}
