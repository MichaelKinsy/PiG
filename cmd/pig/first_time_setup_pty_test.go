//go:build linux

package main

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

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// firstTimeSetupSession drives an interactive PiG in a fresh PIG_HOME through first-time setup with keys, then proves the
// session answers a prompt. It returns PIG_HOME.
func firstTimeSetupSession(t *testing.T, keys []string) string {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	home, cwd, pigHome := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "pighome")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := slices.DeleteFunc(os.Environ(), func(value string) bool {
		name, _, _ := strings.Cut(value, "=")
		return slices.Contains([]string{"HOME", "PIG_HOME", "XDG_CONFIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PI_PACKAGE_DIR", "PIG_USE_PI_DIRS", "PI_EXPERIMENTAL", "PIG_TEST_FAUX", "PIG_TEST_FAUX_SCENARIO", "PIG_STARTUP_TRACE", "PIG_OFFLINE", "PI_OFFLINE"}, name)
	})
	env = append(env, "HOME="+home, "PIG_HOME="+pigHome, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color", "PIG_STARTUP_TRACE=1")
	master, slave := openPTY(t, 60, 160)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--offline", "--no-session", "--model", "test-faux/faux-1", "--tui-mode", "regular")
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
	var waitIn func(*ptyOutput, int, func(string) bool, string)
	waitFor := func(from int, match func(string) bool, what string) {
		t.Helper()
		waitIn(output, from, match, what)
	}
	waitIn = func(stream *ptyOutput, from int, match func(string) bool, what string) {
		t.Helper()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			text := string(tools.StripANSI(stream.since(from)))
			if match(text) {
				return
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
	waitFor(0, func(text string) bool { return strings.Contains(text, "Pick a theme.") }, "first-time setup")
	for _, key := range keys {
		if _, err := master.Write([]byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	// Startup finishes, and the editor owns input again, only after the dialog closes.
	waitIn(diagnostics, 0, func(text string) bool { return strings.Contains(text, "interactive-ready") }, "interactive readiness after setup")
	mark := output.mark()
	if _, err := master.Write([]byte("What is 20+22?\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(mark, func(text string) bool {
		for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' }) {
			if strings.TrimSpace(line) == "42" {
				return true
			}
		}
		return false
	}, "an answer after setup")
	return pigHome
}

func readSettingsFile(t *testing.T, pigHome string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(pigHome, "agent", "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

// Esc skips setup: no theme, analytics or sprite is saved, and the session runs.
func TestFirstTimeSetupEscSkipsOnInteractiveStart(t *testing.T) {
	pigHome := firstTimeSetupSession(t, []string{"\x1b[B", "\x1b"})
	settings := readSettingsFile(t, pigHome)
	for _, key := range []string{"theme", "enableAnalytics"} {
		if value, ok := settings[key]; ok {
			t.Fatalf("Esc saved %s = %v", key, value)
		}
	}
	if _, err := os.Stat(piglogin.StatePath(pigHome)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Esc saved a sprite: %v", err)
	}
}

// Completing setup saves the theme and analytics choice in settings.json and the sprite exactly as /sprite set does.
func TestFirstTimeSetupSavesThemeSpriteAndAnalytics(t *testing.T) {
	pigHome := firstTimeSetupSession(t, []string{"\x1b[B", "\r", "\x1b[B", "\r", "\x1b[B", "\r"})
	settings := readSettingsFile(t, pigHome)
	if settings["theme"] != "dark" || settings["enableAnalytics"] != false {
		t.Fatalf("settings = %v", settings)
	}
	if got := piglogin.LoadVariant(pigHome).ID; got != piglogin.Variants[1].ID {
		t.Fatalf("saved sprite = %q, want %q", got, piglogin.Variants[1].ID)
	}
}
