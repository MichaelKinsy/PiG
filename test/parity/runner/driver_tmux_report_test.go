//go:build parity

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func useTmuxSocket(t *testing.T, name string) {
	t.Helper()
	tmuxServerMu.Lock()
	previous, previousPID := tmuxSocketName, tmuxServerPID.Load()
	previousStarted, previousErr, previousFormat := tmuxKeeperStarted, tmuxKeeperErr, tmuxExtendedKeysFormatSupported
	tmuxServerMu.Unlock()
	tmuxLostMu.Lock()
	previousLost := tmuxLostServers
	tmuxLostServers = nil
	tmuxLostMu.Unlock()
	tmuxSocketName = name
	tmuxServerPID.Store(0)
	t.Cleanup(func() {
		for _, name := range []string{"keeper", "work", tmuxKeeperSession} {
			_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", name)...).Run()
		}
		// A server killed by a test leaves its socket file behind; nothing else will remove it.
		_ = os.Remove(tmuxSocketPath())
		tmuxSocketName = previous
		tmuxServerPID.Store(previousPID)
		tmuxServerMu.Lock()
		tmuxKeeperStarted, tmuxKeeperErr, tmuxExtendedKeysFormatSupported = previousStarted, previousErr, previousFormat
		tmuxServerMu.Unlock()
		tmuxLostMu.Lock()
		tmuxLostServers = previousLost
		tmuxLostMu.Unlock()
	})
}

// A capture that fails because the isolated server never existed, or was removed, says so rather than only "exit status 1".
func TestTmuxServerReportNamesAnUnreachableServer(t *testing.T) {
	useTmuxSocket(t, "pig-parity-report-"+uniqueID())
	report := tmuxServerReport(context.Background(), "parity-session")
	for _, want := range []string{"server unreachable", "socket missing", "session parity-session"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q lacks %q", report, want)
		}
	}
}

// A server that was killed after it was recorded is reported as gone, with its pid; a server that is alive reports its sessions.
func TestTmuxServerReportDistinguishesALostServerFromALostSession(t *testing.T) {
	useTmuxSocket(t, "pig-parity-report-"+uniqueID())
	if out, err := exec.Command("tmux", tmuxArgs("-f", "/dev/null", "new-session", "-d", "-s", "keeper", "sleep 300", ";", "new-session", "-d", "-s", "work", "sleep 300")...).CombinedOutput(); err != nil {
		t.Skipf("tmux unavailable: %v: %s", err, out)
	}
	ctx := context.Background()
	recordTmuxServerPID(ctx)
	pid := int(tmuxServerPID.Load())
	if pid == 0 {
		t.Fatal("server pid was not recorded")
	}
	live := tmuxServerReport(ctx, "work")
	for _, want := range []string{"server alive", "session work present", "keeper", "work"} {
		if !strings.Contains(live, want) {
			t.Fatalf("live report %q lacks %q", live, want)
		}
	}
	_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", "work")...).Run()
	if report := tmuxServerReport(ctx, "work"); !strings.Contains(report, "server alive") || !strings.Contains(report, "session work absent") {
		t.Fatalf("a lost session on a live server is reported as %q", report)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && tmuxProcessRunning(pid) {
		time.Sleep(10 * time.Millisecond)
	}
	report := tmuxServerReport(ctx, "keeper")
	if !strings.Contains(report, "server pid") || !strings.Contains(report, "exited") || !strings.Contains(report, "server unreachable") {
		t.Fatalf("a killed server is reported as %q", report)
	}
}

// An isolated server that is killed mid-run is replaced by the next session's server, which starts with tmux's defaults. ensureTmuxServer must notice the loss and configure the replacement again, or every later scenario runs without the keyboard protocol the first server was given.
func TestEnsureTmuxServerConfiguresAReplacementForALostServer(t *testing.T) {
	useTmuxSocket(t, "pig-parity-report-"+uniqueID())
	tmuxServerMu.Lock()
	defer tmuxServerMu.Unlock()
	tmuxKeeperErr, tmuxKeeperStarted = nil, false
	if err := ensureTmuxServer(); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	first := int(tmuxServerPID.Load())
	process, err := os.FindProcess(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && tmuxProcessRunning(first); {
		time.Sleep(10 * time.Millisecond)
	}
	// What the next scenario does: it creates its session on whatever server answers.
	if out, err := exec.Command("tmux", tmuxArgs("new-session", "-d", "-s", "work", "sleep 300")...).CombinedOutput(); err != nil {
		t.Fatalf("replacement session: %v: %s", err, out)
	}
	if err := ensureTmuxServer(); err != nil {
		t.Fatal(err)
	}
	if got := int(tmuxServerPID.Load()); got == first || got == 0 {
		t.Fatalf("recorded server pid = %d, want the replacement (was %d)", got, first)
	}
	if report := tmuxServerReport(context.Background(), "work"); !strings.Contains(report, fmt.Sprintf("server pid %d was lost and replaced", first)) {
		t.Fatalf("the report does not name the lost server: %q", report)
	}
	if out, err := exec.Command("tmux", tmuxArgs("show", "-gv", "extended-keys")...).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "on" {
		t.Fatalf("replacement server extended-keys = %q (%v), want on", out, err)
	}
	if out, err := exec.Command("tmux", tmuxArgs("has-session", "-t", "="+tmuxKeeperSession)...).CombinedOutput(); err != nil {
		t.Fatalf("replacement server has no keeper: %v: %s", err, out)
	}
	_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", tmuxKeeperSession)...).Run()
}

// A configuration failure on a server whose keeper is still there fails every later scenario, as it did when the server was set up once; it must not pass silently on the next call.
func TestEnsureTmuxServerKeepsAConfigurationError(t *testing.T) {
	useTmuxSocket(t, "pig-parity-report-"+uniqueID())
	tmuxServerMu.Lock()
	defer tmuxServerMu.Unlock()
	tmuxKeeperErr, tmuxKeeperStarted = nil, false
	if err := ensureTmuxServer(); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	// The state ensureTmuxServer leaves when set-option extended-keys-format fails for a reason other than an old tmux.
	configureErr := errors.New("configure isolated tmux server: exit status 1")
	tmuxKeeperErr = configureErr
	if err := ensureTmuxServer(); !errors.Is(err, configureErr) {
		t.Fatalf("second call = %v, want the configuration error %v", err, configureErr)
	}
	_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", tmuxKeeperSession)...).Run()
}

// A keeper session that ends while the server survives is restarted on the same server; the report must not claim the server was lost.
func TestEnsureTmuxServerRestartsALostKeeperOnTheSameServer(t *testing.T) {
	useTmuxSocket(t, "pig-parity-report-"+uniqueID())
	tmuxServerMu.Lock()
	defer tmuxServerMu.Unlock()
	tmuxKeeperErr, tmuxKeeperStarted = nil, false
	if err := ensureTmuxServer(); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	first := int(tmuxServerPID.Load())
	if out, err := exec.Command("tmux", tmuxArgs("new-session", "-d", "-s", "work", "sleep 300", ";", "kill-session", "-t", "="+tmuxKeeperSession)...).CombinedOutput(); err != nil {
		t.Fatalf("drop keeper: %v: %s", err, out)
	}
	if err := ensureTmuxServer(); err != nil {
		t.Fatal(err)
	}
	if got := int(tmuxServerPID.Load()); got != first {
		t.Fatalf("server pid = %d, want the surviving server %d", got, first)
	}
	report := tmuxServerReport(context.Background(), "work")
	if strings.Contains(report, "was lost and replaced") {
		t.Fatalf("a surviving server is reported as replaced: %q", report)
	}
	if want := fmt.Sprintf("keeper session on server pid %d was lost and restarted", first); !strings.Contains(report, want) {
		t.Fatalf("report %q lacks %q", report, want)
	}
	_ = exec.Command("tmux", tmuxArgs("kill-session", "-t", tmuxKeeperSession)...).Run()
}
