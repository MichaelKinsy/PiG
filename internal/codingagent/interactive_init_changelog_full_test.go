//go:build linux && !pig_strip_changelog

package codingagent_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const initChangelogFullChildEnv = "PIG_TEST_INIT_CHANGELOG_FULL_CHILD"

// interactive-mode.ts init() prepares the changelog data first and the bind shows it (showStartupNoticesIfNeeded, 853-905): a Session without messages started
// by a binary newer than the recorded version shows the "What's New" notice, and the version is recorded. Init needs a terminal for raw mode, so it runs in a
// child on a pty against a real Session.
func TestInitShowsTheStartupChangelogOfAnUpdate(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 100}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report")
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInitShowsTheStartupChangelogOfAnUpdateChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), initChangelogFullChildEnv+"="+report, "TERM=xterm-256color", "PIG_HOME="+home, "PI_OFFLINE=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&captured, master)
	}()
	waitErr := cmd.Wait()
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
		_ = master.Close()
		<-copied
	}
	failure, _ := os.ReadFile(report)
	if waitErr != nil || len(failure) != 0 || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestInitShowsTheStartupChangelogOfAnUpdateChild")) {
		t.Fatalf("child failed: %v\n%s\n%q", waitErr, failure, captured.Bytes()[max(0, captured.Len()-600):])
	}
	if !strings.Contains(captured.String(), "What's New") {
		t.Fatalf("Init showed no What's New notice for an update:\n%q", captured.String()[:min(captured.Len(), 1500)])
	}
}

func TestInitShowsTheStartupChangelogOfAnUpdateChild(t *testing.T) {
	report := os.Getenv(initChangelogFullChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitShowsTheStartupChangelogOfAnUpdate")
	}
	// The production startup this child runs selects a theme and replaces the registry; TestMain fails a binary that leaves them.
	icodingagent.RestoreStartupTheme(t)
	fail := func(format string, args ...any) {
		_ = os.WriteFile(report, fmt.Appendf(nil, format, args...), 0o600)
		t.Fatalf(format, args...)
	}
	agentDir := filepath.Join(os.Getenv("PIG_HOME"), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"lastChangelogVersion":"0.0.1"}`), 0o600); err != nil {
		fail("%v", err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		fail("%v", err)
	}
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		fail("%v", err)
	}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: manager})
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = session.Close() }()
	mode := icodingagent.NewInteractiveMode(nil, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session, NoThemes: true, AppVersion: "999.0.0",
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(),
	})
	if err := mode.Init(t.Context()); err != nil {
		_ = mode.Stop()
		fail("Init: %v", err)
	}
	if got := services.SettingsManager().GetLastChangelogVersion(); got != "999.0.0" {
		fail("recorded changelog version = %q, want 999.0.0", got)
	}
	if err := mode.Stop(); err != nil {
		fail("Stop: %v", err)
	}
}
