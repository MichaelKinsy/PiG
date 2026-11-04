package codingagent

import (
	"fmt"
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

// Every path that sees the process end calls RecordEnding; one line comes out, and no caller returns before it is on disk
// (exit_ending.go, steps 1 and 4).
func TestRecordEndingLeavesOneLineAndNoCallerReturnsBeforeItIsOnDisk(t *testing.T) {
	freshEnding(t)
	agentDir := t.TempDir()
	const callers = 16
	var wg sync.WaitGroup
	wins := make(chan string, callers)
	missing := make(chan string, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reason := fmt.Sprintf("ending %d", i)
			<-start
			if RecordEnding(agentDir, reason) {
				wins <- reason
			}
			if text := exitLogText(agentDir); !strings.Contains(text, " ending ") {
				missing <- reason
			}
		}()
	}
	close(start)
	wg.Wait()
	close(wins)
	close(missing)
	if len(wins) != 1 {
		t.Fatalf("%d callers own the record, want 1", len(wins))
	}
	for reason := range missing {
		t.Errorf("%s returned before the record was on disk", reason)
	}
	winner := <-wins
	if lines := strings.Split(strings.TrimSuffix(exitLogText(agentDir), "\n"), "\n"); len(lines) != 1 || !strings.HasSuffix(lines[0], " "+winner) {
		t.Fatalf("exit.log = %q, want one line naming %q", exitLogText(agentDir), winner)
	}
}

// A dead terminal's exit does not claim the record while the kernel's SIGHUP may still be on its way: it waits for the
// signal goroutine's claim, which then names the cause. synctest.Wait returns only when the waiter is durably blocked, so
// this does not depend on scheduling.
func TestDeadTerminalExitWaitsForASignalThatClaimsTheRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshEnding(t)
		agentDir := t.TempDir()
		began := time.Now()
		var waiting sync.WaitGroup
		waiting.Go(func() {
			awaitEndingClaim()
			RecordEnding(agentDir, "terminal gone: EOF (hung up) (exit 129)")
		})
		synctest.Wait()
		if text := exitLogText(agentDir); text != "" {
			t.Fatalf("the dead terminal recorded before the signal could: %q", text)
		}
		if !RecordEnding(agentDir, "received SIGHUP") {
			t.Fatal("the signal goroutine did not own the record")
		}
		waiting.Wait()
		if elapsed := time.Since(began); elapsed != 0 {
			t.Fatalf("the exit waited %v for a claim that had already come", elapsed)
		}
		if lines := strings.Split(strings.TrimSuffix(exitLogText(agentDir), "\n"), "\n"); len(lines) != 1 || !strings.HasSuffix(lines[0], " received SIGHUP") {
			t.Fatalf("exit.log = %q, want only received SIGHUP", exitLogText(agentDir))
		}
	})
}

// A terminal that died with no signal (a session in a background process group) still ends the process: after hangupGrace the
// dead terminal is the cause.
func TestDeadTerminalExitClaimsTheRecordWhenNoSignalComes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freshEnding(t)
		agentDir := t.TempDir()
		began := time.Now()
		awaitEndingClaim()
		if elapsed := time.Since(began); elapsed != hangupGrace {
			t.Fatalf("waited %v, want hangupGrace %v", elapsed, hangupGrace)
		}
		if !RecordEnding(agentDir, "terminal gone: EOF (hung up) (exit 129)") {
			t.Fatal("the dead terminal did not own the record")
		}
	})
}

// Concurrent emergency exits run once: one line, exit 129.
func TestConcurrentEmergencyExitsLeaveOneLine(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "dead-terminal-concurrent", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if lines := strings.Split(strings.TrimSuffix(exitLogText(agentDir), "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "terminal gone: path ") || !strings.HasSuffix(lines[0], "(exit 129)") {
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
	if lines := strings.Split(strings.TrimSuffix(exitLogText(agentDir), "\n"), "\n"); len(lines) != 1 || !strings.HasSuffix(lines[0], " received SIGHUP") {
		t.Fatalf("exit.log = %q, want only received SIGHUP", exitLogText(agentDir))
	}
}
