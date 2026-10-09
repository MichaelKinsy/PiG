//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// reloadTrustSession starts the real binary in a project without trust-requiring resources and with legacy credentials,
// waits for the startup notices, adds a project prompt and runs /reload. It returns the transcript text after startup,
// the text after /reload, the agent directory and the project directory.
func reloadTrustSession(t *testing.T, extraArgs ...string) (startup, reload, agentDir, cwd string) {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	home, pigHome := filepath.Join(root, "home"), filepath.Join(root, "pighome")
	cwd, agentDir = filepath.Join(root, "project"), filepath.Join(pigHome, "agent")
	for _, dir := range []string{home, cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// settings.json skips first-time setup; its legacy apiKeys and oauth.json migrate in Object.entries order.
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"apiKeys":{"zeta-legacy":"k1","alpha-legacy":"k2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "oauth.json"), []byte(`{"mid-legacy":{"refresh":"r"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := slices.DeleteFunc(os.Environ(), func(value string) bool {
		name, _, _ := strings.Cut(value, "=")
		return slices.Contains([]string{"HOME", "PIG_HOME", "XDG_CONFIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PI_PACKAGE_DIR", "PIG_USE_PI_DIRS", "PI_EXPERIMENTAL", "PIG_TEST_FAUX", "PIG_TEST_FAUX_SCENARIO", "PIG_STARTUP_TRACE", "PIG_OFFLINE", "PI_OFFLINE"}, name)
	})
	env = append(env, "HOME="+home, "PIG_HOME="+pigHome, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color", "PIG_STARTUP_TRACE=1")
	master, slave := openPTY(t, 60, 200)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	args := append([]string{"--offline", "--no-session", "--no-extensions", "--model", "test-faux/faux-1", "--tui-mode", "regular"}, extraArgs...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = cwd, env
	output, diagnostics := &ptyOutput{}, &ptyOutput{}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, diagnostics
	captureCtx, cancelCapture := context.WithCancel(ctx)
	captured := make(chan struct{})
	fd := int(master.Fd())
	go func() {
		defer close(captured)
		buffer := make([]byte, 4096)
		for captureCtx.Err() == nil {
			ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 20)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil {
				return
			}
			if ready == 0 {
				continue
			}
			n, err := unix.Read(fd, buffer)
			if n > 0 {
				_, _ = output.Write(buffer[:n])
			}
			if err != nil || n == 0 {
				return
			}
		}
	}()
	defer func() { cancelCapture(); <-captured }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan error, 1)
	go func() { defer close(done); done <- cmd.Wait() }()
	defer func() { cancel(); <-done }()
	waitIn := func(stream *ptyOutput, from int, needle, what string) string {
		t.Helper()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			text := string(tools.StripANSI(stream.since(from)))
			if strings.Contains(text, needle) {
				return text
			}
			select {
			case err := <-done:
				t.Fatalf("PiG exited before %s: %v\n%s", what, err, text)
			case <-ctx.Done():
				t.Fatalf("no %s: %v\n%s", what, ctx.Err(), text)
			case <-tick.C:
			}
		}
	}
	waitIn(diagnostics, 0, "interactive-ready", "interactive readiness")
	startup = waitIn(output, 0, "Migrated credentials to auth.json", "the migration warning")
	if err := os.MkdirAll(filepath.Join(codingagent.ProjectConfigDir(cwd), "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	mark := output.mark()
	if _, err := master.Write([]byte("/reload\r")); err != nil {
		t.Fatal(err)
	}
	reload = waitIn(output, mark, "Reloaded keybindings", "the reload status")
	return startup, reload, agentDir, cwd
}

// main.ts:666 passes migrateAuthToAuthJson's providers and main.ts:719-722 arms autoTrustOnReloadCwd for a start without a
// trust override and without trust-requiring resources; interactive-mode.ts:1199-1201 warns about the migration, and
// handleReloadCommand (6463-6472) saves the trust once the project gains a resource and names it in the status.
func TestReloadSavesImplicitProjectTrustInTheBinary(t *testing.T) {
	startup, reload, agentDir, cwd := reloadTrustSession(t)
	if !strings.Contains(startup, "Migrated credentials to auth.json: mid-legacy, zeta-legacy, alpha-legacy") {
		t.Fatalf("startup lacks the ordered migration warning:\n%s", startup)
	}
	if !strings.Contains(reload, "Reloaded keybindings, extensions, skills, prompts, themes, and context files; saved project trust") {
		t.Fatalf("reload status does not name the saved trust:\n%s", reload)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	var trust map[string]bool
	if err := json.Unmarshal(data, &trust); err != nil || !trust[cwd] {
		t.Fatalf("trust.json = %s (%v), want %s trusted", data, err, cwd)
	}
}

// A trust override (--approve) leaves autoTrustOnReloadCwd unset (main.ts:719-722), so /reload saves nothing.
func TestReloadWithTrustOverrideSavesNoProjectTrustInTheBinary(t *testing.T) {
	_, reload, agentDir, _ := reloadTrustSession(t, "--approve")
	if strings.Contains(reload, "saved project trust") {
		t.Fatalf("an overridden start saved project trust:\n%s", reload)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "trust.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trust.json written: %v", err)
	}
}
