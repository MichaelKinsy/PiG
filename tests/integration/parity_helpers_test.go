//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bothSystems(t *testing.T) []systemConfig {
	t.Helper()
	return []systemConfig{pigConfig(t), upstreamConfig(t)}
}

func normalizeEscapedTmuxBlock(value string) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimPrefix(line, "\x1b[39m")
	}
	return strings.Join(lines, "\n")
}

func launchSystemWithReady(t *testing.T, config systemConfig, timeout time.Duration, markers ...string) (string, func()) {
	t.Helper()
	session := "parity-live-" + config.name + "-" + randID()
	if output, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "130", "-y", "36").CombinedOutput(); err != nil {
		t.Fatalf("create tmux session %s: %v\n%s", session, err, output)
	}
	cleanup := func() {
		if output, err := tmuxCommand("kill-session", "-t", session).CombinedOutput(); err != nil {
			t.Errorf("remove tmux session %s: %v\n%s", session, err, output)
		}
	}

	environment := strings.Join(config.env, " ")
	if environment != "" {
		environment += " "
	}
	command := environment + config.bin + " " + strings.Join(config.args, " ")
	if output, err := tmuxCommand("send-keys", "-t", session, "-l", command+"\n").CombinedOutput(); err != nil {
		cleanup()
		t.Fatalf("start %s: %v\n%s", config.name, err, output)
	}

	deadline := time.Now().Add(timeout)
	var pane string
	for time.Now().Before(deadline) {
		pane = capturePane(t, session)
		for _, marker := range markers {
			if marker != "" && strings.Contains(pane, marker) {
				return session, cleanup
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	cleanup()
	t.Fatalf("%s failed to start within %s:\n%s", config.name, timeout, pane)
	return "", nil
}

func capturePane(t *testing.T, session string) string {
	t.Helper()
	output, err := tmuxCommand("capture-pane", "-t", session, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("capture tmux session %s: %v\n%s", session, err, output)
	}
	return string(output)
}

func lastN(value string, count int) string {
	lines := strings.Split(value, "\n")
	if len(lines) <= count {
		return value
	}
	return strings.Join(lines[len(lines)-count:], "\n")
}

func defaultEnv() []string {
	return os.Environ()
}

// Pi paints the footer before awaiting session_start, but shows loaded resources only after bindCurrentSessionExtensions completes.
func TestPigParityLauncherWaitsForSessionStart(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	completed := filepath.Join(dir, "completed")
	ext := filepath.Join(dir, "startup-probe.mjs")
	source := `import { existsSync, writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_start", async () => {
    writeFileSync(process.env.PIG_STARTUP_STARTED, "started");
    while (!existsSync(process.env.PIG_STARTUP_RELEASE)) {
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    writeFileSync(process.env.PIG_STARTUP_COMPLETED, "completed");
  });
}
`
	if err := os.WriteFile(ext, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pigHome := t.TempDir()
	writeTestExtensionSettings(t, pigHome, ext)
	config := pigConfigWithHome(t, pigHome)
	config.args = []string{"--model", deterministicModel}
	config.env = append(config.env, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1",
		"PIG_STARTUP_STARTED="+started, "PIG_STARTUP_RELEASE="+release, "PIG_STARTUP_COMPLETED="+completed)

	// Hold the handler across multiple launcher polls within its existing 20-second deadline. Cancel and join the releaser even when launch fails.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(started); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				done <- err
				return
			}
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
			}
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			done <- nil
		case <-timer.C:
			done <- os.WriteFile(release, nil, 0o600)
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("release session_start barrier: %v", err)
		}
	})

	start := time.Now()
	session, cleanup := launchSystem(t, config)
	defer cleanup()
	if _, err := os.Stat(completed); err != nil {
		t.Fatalf("launcher returned after %s before session_start completed: %v\n%s", time.Since(start), err, capturePane(t, session))
	}
	t.Logf("extension-ready startup: %s", time.Since(start))
	pane := sendSlashAndCapture(t, session, "/hotkeys", 3*time.Second)
	if !strings.Contains(pane, "Ctrl+R            : Rename session") {
		t.Fatalf("launcher returned before command dispatch was ready:\n%s", pane)
	}
}

// The parity launch path does not use harness.startArgs; it must establish its
// own fixture trust so the checked-in .agents resources cannot block readiness.
func TestPigParityConfigTrustsOnlyItsFixtureHome(t *testing.T) {
	config := pigConfigWithHome(t, t.TempDir())
	// This trust fixture has no extensions; only extension-enabled launches await the resource listing.
	config.args = append(config.args, "--no-extensions")
	config.prompt = interactiveReadyMarker
	config.env = append(config.env, "PIG_OFFLINE=1")
	_, cleanup := launchSystemWithReady(t, config, startupReadyWait, config.prompt)
	defer cleanup()
}
