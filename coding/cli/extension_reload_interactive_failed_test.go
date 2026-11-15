//go:build linux

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const reloadFailureInteractiveProbeNode = `import { appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  const log = (line) => appendFileSync(process.env.RELOAD_PROBE_LOG, line + "\n");
  pi.on("session_start", async (event) => log("start:" + event.reason));
  pi.on("session_shutdown", async (event) => log("shutdown:" + event.reason));
  pi.registerCommand("reloadfail", {
    description: "Reload with a settings file Pi's resource loader rejects, then use the captured pi",
    handler: async (_args, ctx) => {
      writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "settings.json"), JSON.stringify({ quietStartup: true, prompts: ["file:///a%2Fb"] }));
      try {
        await ctx.reload();
        log("resolved");
      } catch (error) {
        log("rejected:" + error.message);
      }
      try {
        pi.getActiveTools();
        log("live");
      } catch (error) {
        log("stale:" + error.message);
      }
      log("done");
    },
  });
  pi.registerCommand("probe", {
    description: "Use the captured pi",
    handler: async () => {
      try {
        pi.getActiveTools();
        log("probe:live");
      } catch (error) {
        log("probe:stale:" + error.message);
      }
    },
  });
}
`

// Pi's interactive /reload catches a reload that throws and shows the error (interactive-mode.ts:6670-6704 `try { await this.session.reload(...)`), so the command that awaited ctx.reload() resolves. session.reload() has invalidated the old runner before the step that throws (agent-session.ts:3825-3826, 3838) and builds no new one, so the old instance's captured pi throws the stale message and no session_start(reload) is emitted.
//
// A later /reload whose settings load builds a new runner (agent-session.ts:3844): the stale instance gets session_shutdown(reload) once more (:3825, emit does not look at staleness), the new instance gets session_start(reload), and its pi is live.
// mutation-checked: without the invalidation after session_shutdown in the interactive reload, the log has "live" where Pi's has the stale message; with it and without Host.Reload ending the staleness a failed reload left, the reloaded instance logs "probe:stale:...".
func TestFailedInteractiveReloadLeavesTheOldInstanceStale(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-fail-interactive.mjs")
	if err := os.WriteFile(extension, []byte(reloadFailureInteractiveProbeNode), 0o600); err != nil {
		t.Fatal(err)
	}
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "reload.log")
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath, "TERM=xterm-256color"}
	master, slave := openPTY(t, 40, 160)
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension, "--no-session", "--approve")
	cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	output := &ptyOutput{}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				_, _ = output.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan struct{})
	go func() { defer close(done); _ = cmd.Wait() }()
	t.Cleanup(func() { cancel(); <-done; _ = master.Close() })
	output.waitQuiet(0, []byte("faux-1"), 200*time.Millisecond, testbudget.Wait(t))
	if _, err := master.WriteString("/reloadfail\r"); err != nil {
		t.Fatal(err)
	}
	// Pi still sends session_shutdown(quit) to the stale instance (TestFailedExtensionReloadLeavesTheOldInstanceStale).
	want := strings.Join([]string{"start:startup", "shutdown:reload", "resolved", "stale:" + (&inproc.StaleError{}).Error(), "done", "shutdown:reload", "start:reload", "probe:live", "shutdown:quit"}, "\n")
	read := func() string {
		data, _ := os.ReadFile(logPath)
		return strings.TrimRight(string(data), "\n")
	}
	deadline := time.After(testbudget.Wait(t))
	for !strings.Contains(read(), "\ndone") {
		select {
		case <-done:
			t.Fatalf("pig exited early; log:\n%s\nscreen:\n%s", read(), output.since(0))
		case <-deadline:
			t.Fatalf("the command never ended; log:\n%s\nscreen:\n%s", read(), output.since(0))
		case <-time.After(50 * time.Millisecond):
		}
	}
	// The settings load again.
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A key written while a reload runs is dropped, as in Pi (a focused component without handleInput gets nothing: packages/tui/src/tui.ts:1153-1158), so /probe waits for the end of the second reload: the status line it ends with (interactive-mode.ts:6693-6698).
	const reloaded = "Reloaded keybindings, extensions, skills, prompts, themes, and context files"
	for _, step := range []struct{ input, logged, shown string }{{"/reload\r", "\nstart:reload", reloaded}, {"/probe\r", "\nprobe:", ""}} {
		mark := output.mark()
		if _, err := master.WriteString(step.input); err != nil {
			t.Fatal(err)
		}
		for !strings.Contains(read(), step.logged) || !strings.Contains(string(output.since(mark)), step.shown) {
			select {
			case <-done:
				t.Fatalf("pig exited early; log:\n%s\nscreen:\n%s", read(), output.since(0))
			case <-deadline:
				t.Fatalf("%q never logged %q and showed %q; log:\n%s\nscreen:\n%s", step.input, step.logged, step.shown, read(), output.since(0))
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	if _, err := master.WriteString("/quit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-deadline:
		t.Fatalf("pig did not quit; log:\n%s\nscreen:\n%s", read(), output.since(0))
	}
	if got := read(); got != want {
		t.Fatalf("extension log:\n%s\nwant (Pi):\n%s", got, want)
	}
}
