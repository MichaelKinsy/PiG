//go:build integration && !windows

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// deadTerminalRuns is how many terminals each setup of TestDeadTerminalEmitsSessionShutdownToExtensions kills: which of the
// hangup's events reaches pig first (the input's end, the shell's SIGHUP, a failed write) is a race, so one run proves little.
const deadTerminalRuns = 20

// deadTerminalProbeExtension is the Node fixture; testdata/dead-terminal/deadterm is the same extension on the Go SDK.
const deadTerminalProbeExtension = `import { appendFileSync } from "node:fs";
const mark = (line) => appendFileSync(process.env.PIG_DEAD_TERMINAL_MARKER, line + "\n");
export default function (pi) {
  pi.on("session_start", () => mark("ready pid=" + process.pid));
  pi.on("session_shutdown", async (event) => {
    mark("session_shutdown reason=" + event.reason);
    await new Promise((resolve) => setTimeout(resolve, 50));
    mark("session_shutdown settled reason=" + event.reason);
  });
}
`

// TestDeadTerminalEmitsSessionShutdownToExtensions kills the terminal of a real interactive pig with tmux kill-session and
// requires what Pi's extensions get there: session_shutdown with the reason "quit", awaited, before the exit, and exit 129.
//
// Pi 1.5.2, measured with the same extension (a 200 ms wait) after one turn, 4 runs per cell; handler reason / awaited
// write landed / exit code / ms from the kill to the exit:
//
//	                                        Pi, git repository          Pi, plain directory   PiG before the fix
//	tmux kill-session, pi under bash        quit 4/4, 4/4, 129, 212     none, 0, 18-20        none, 129, 4-13
//	tmux kill-pane                          quit 4/4, 4/4, 129, 211     none, 0, 14-19        none, 129, 4-13
//	pty master closed, pi session leader    quit 4/4, 4/4, 129, 213-258 none, 0, 11-61        none, 129, 37-45
//	pty master closed, pi under sh          quit 4/4, 4/4, 129, 211-253 none, 0, 14-53        none, 129, 38-43
//	kill -HUP on a live terminal            quit 4/4, 4/4, 0, 262-277                         quit, 0, 254-257
//
// Pi's order in the repository: stdin end, SIGHUP, session_shutdown, the awaited write, stdout EIO, exit 129
// (interactive-mode.ts registerSignalHandlers, shutdown fromSignal, emergencyTerminalExit). In a plain directory an idle
// Pi's event loop is empty at the hangup and it exits 0 before any handler; PiG exits 129 there and emits the event (D186).
// Pi awaits a 3 s handler in full when idle (8 of 8, exit 129 after 3.01-3.06 s), where PiG waits one second. While a
// turn is in flight Pi's next terminal write fails and ends it under the handler (started 8 of 8, awaited write 0 of 8,
// exit 129 after 23-28 ms); PiG's handler finishes there, which the "turn in flight" setup holds (D186).
// PiG's input reader sees the dead terminal before any signal, and its emergency exit used to skip the event and kill the
// extension processes (#25 of the copiglet filing: every captured session ended `terminal gone: EOF (hung up)`).
//
// The fixtures are a Node factory, two Node factories in one process (a packed cell), the Go SDK factory as a subprocess of
// a Piglet, and the same factory fused into a Piglet Binary. Each runs under an interactive shell, which dies of the hangup as a user's does, and under a shell that ignores
// SIGHUP and records pig's exit status, idle and with a turn in flight (the faux provider's bash call that sleeps). No
// process of the session, and no extension process, is left behind.
func TestDeadTerminalEmitsSessionShutdownToExtensions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH:", err)
	}
	bin := buildBinary(t)
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name string
		// members is how many extensions write to the marker.
		members int
		args    func(dir string) []string
		// fused runs a Piglet Binary built from the Go fixture, which registers the extension in the pig process itself.
		fused bool
	}{
		{"node", 1, func(dir string) []string { return []string{"-e", writeDeadTerminalProbe(t, dir, "probe.mjs")} }, false},
		// Two factories share one Node process (a packed cell); each gets the event.
		{"node packed pair", 2, func(dir string) []string {
			return []string{"-e", writeDeadTerminalProbe(t, dir, "first.mjs"), "-e", writeDeadTerminalProbe(t, dir, "second.mjs")}
		}, false},
		{"go sdk", 1, func(string) []string { return []string{"--piglet", deadTerminalPiglet(root)} }, false},
		{"go sdk fused", 1, func(string) []string { return nil }, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// One agent directory for the fixture's runs, so the Go fixture is built once.
			home := t.TempDir()
			seedIntegrationSettings(t, home)
			if err := os.WriteFile(filepath.Join(home, "agent", "models.json"), []byte(uiViewModels), 0o600); err != nil {
				t.Fatal(err)
			}
			cwd := t.TempDir()
			trustIntegrationProjectAt(t, home, cwd)
			args := append(fixture.args(home), "--model", "test-faux/faux-1")
			bin := bin
			if fixture.fused {
				bin = buildDeadTerminalPigletBinary(t, bin, deadTerminalPiglet(root))
			}
			for _, setup := range []struct {
				name                  string
				recordStatus, working bool
			}{
				{"interactive shell", false, false},
				{"shell ignoring SIGHUP", true, false},
				{"turn in flight", true, true},
			} {
				t.Run(setup.name, func(t *testing.T) {
					failures := 0
					var slowest time.Duration
					for run := range deadTerminalRuns {
						took, problem := killTerminalUnderOnePig(t, bin, home, cwd, args, fixture.members, setup.recordStatus, setup.working)
						slowest = max(slowest, took)
						if problem != "" {
							failures++
							t.Errorf("run %d: %s", run, problem)
						}
					}
					t.Logf("slowest exit after kill-session: %s", slowest)
					if failures > 0 {
						t.Errorf("%d of %d dead terminals did not give the extension its session_shutdown and exit 129", failures, deadTerminalRuns)
					}
				})
			}
		})
	}
}

func deadTerminalPiglet(root string) string {
	return filepath.Join(root, "test", "integration", "testdata", "dead-terminal", "piglet.yaml")
}

// buildDeadTerminalPigletBinary builds the Piglet Binary of the Go fixture with `pig piglet build --format binary`.
func buildDeadTerminalPigletBinary(t *testing.T, bin, piglet string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pig-dead-terminal")
	cmd := exec.Command(bin, "piglet", "build", piglet, "--format", "binary", "--builder", "native", "--out", out)
	// The agent-directory variables the test process scopes would override PIG_HOME; empty them as the tmux panes do.
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(),
		"PIG_CODING_AGENT_DIR=", "PIG_CODING_AGENT_SESSION_DIR=", "PI_CODING_AGENT_DIR=", "PI_CODING_AGENT_SESSION_DIR=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig piglet build: %v\n%s", err, output)
	}
	return out
}

func writeDeadTerminalProbe(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(deadTerminalProbeExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// killTerminalUnderOnePig starts pig in a tmux pane under a shell, kills the session once every extension is ready (with working,
// once the turn's tool runs), and returns how long the pane's processes took to exit and what is wrong with the ending, or "".
func killTerminalUnderOnePig(t *testing.T, bin, home, cwd string, args []string, members int, recordStatus, working bool) (took time.Duration, problem string) {
	t.Helper()
	// ai.TestFauxToolStartedMarker: the faux provider's sleeping bash call creates it in the working directory.
	toolStarted := filepath.Join(cwd, ".pig-faux-tool-started")
	if err := os.Remove(toolStarted); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	marker, status, exitLog := filepath.Join(scratch, "marker"), filepath.Join(scratch, "status"), filepath.Join(home, "agent", "exit.log")
	if err := os.Remove(exitLog); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	command := "cd '" + cwd + "' && env PIG_HOME='" + home + "' PIG_OFFLINE=1 PIG_TEST_FAUX=1 PIG_QUIET_STARTUP=1 PIG_DEAD_TERMINAL_MARKER='" + marker + "' '" + bin + "' " + strings.Join(args, " ")
	session := "pig-dead-" + randID()
	if recordStatus {
		// The shell survives the hangup, so pig gets no SIGHUP from it and its exit status is readable.
		command = "trap '' HUP; " + command + "; echo $? > '" + status + "'"
		if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34", command).CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session: %v\n%s", err, out)
		}
	} else {
		if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34", "bash --norc --noprofile -i").CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session: %v\n%s", err, out)
		}
		for _, keys := range [][]string{{"-l", command}, {"Enter"}} {
			if out, err := tmuxCommand(append([]string{"send-keys", "-t", session}, keys...)...).CombinedOutput(); err != nil {
				t.Fatalf("tmux send-keys: %v\n%s", err, out)
			}
		}
	}
	pane, err := paneSessionID(session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = killSession(t, session) }()

	var extensions []int
	// The first start of the Go fixture builds it.
	if !waitFor(4*time.Minute, func() bool {
		extensions = extensions[:0]
		data, _ := os.ReadFile(marker)
		for line := range strings.Lines(string(data)) {
			if pid, ok := strings.CutPrefix(strings.TrimSpace(line), "ready pid="); ok {
				n, _ := strconv.Atoi(pid)
				extensions = append(extensions, n)
			}
		}
		return len(extensions) == members
	}) {
		out, _ := tmuxCommand("capture-pane", "-p", "-t", session).Output()
		t.Fatalf("%d of %d extensions ready; pane:\n%s", len(extensions), members, out)
	}

	if working {
		for _, keys := range [][]string{{"-l", "Run: sleep for a while"}, {"Enter"}} {
			if out, err := tmuxCommand(append([]string{"send-keys", "-t", session}, keys...)...).CombinedOutput(); err != nil {
				t.Fatalf("tmux send-keys: %v\n%s", err, out)
			}
		}
		if !waitFor(startupReadyWait, func() bool { _, err := os.Stat(toolStarted); return err == nil }) {
			out, _ := tmuxCommand("capture-pane", "-p", "-t", session).Output()
			t.Fatalf("the turn's tool never started; pane:\n%s", out)
		}
	}

	start := time.Now()
	// hangUpSession fails the test if a process of the pane's session is still running 30 s after kill-session.
	if err := hangUpSession(t, session, pane, livePaneProcesses); err != nil {
		t.Fatal(err)
	}
	took = time.Since(start)
	for _, pid := range extensions {
		if !waitFor(5*time.Second, func() bool { return processExited(pid) }) {
			return took, "extension process " + strconv.Itoa(pid) + " outlived pig"
		}
	}

	// The handlers are awaited before the exit, so the marker is complete once pig is gone.
	data, _ := os.ReadFile(marker)
	if got := strings.Count(string(data), "session_shutdown reason=quit\n"); got != members {
		return took, "session_shutdown reached " + strconv.Itoa(got) + " of " + strconv.Itoa(members) + " extensions; marker: " + strconv.Quote(string(data))
	}
	if got := strings.Count(string(data), "session_shutdown settled reason=quit\n"); got != members {
		return took, "the exit did not wait for the handler; marker: " + strconv.Quote(string(data))
	}
	log, err := os.ReadFile(exitLog)
	if err != nil {
		return took, "no exit.log: " + err.Error()
	}
	lines := strings.Split(strings.TrimSuffix(string(log), "\n"), "\n")
	gone := len(lines) == 1 && strings.Contains(lines[0], " terminal gone: ") && strings.HasSuffix(lines[0], " (exit 129)")
	switch {
	case len(lines) != 1:
		return took, "exit.log is " + strings.Join(lines, " | ")
	case recordStatus && !gone:
		return took, "exit.log names no dead terminal: " + lines[0]
	case !gone && !strings.HasSuffix(lines[0], " received SIGHUP"):
		return took, "exit.log names no hangup cause: " + lines[0]
	}
	if recordStatus {
		raw, err := os.ReadFile(status)
		if err != nil {
			return took, "no exit status: " + err.Error()
		}
		if code := strings.TrimSpace(string(raw)); code != "129" {
			return took, "exit status " + code + ", want 129"
		}
	}
	return took, ""
}
