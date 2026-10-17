//go:build parity && linux

package runner

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The stand-in programs print the ready line (or not), then become `sleep <mark>`. ignoreTermination makes the program survive the
// SIGHUP that ending its tmux session delivers and the SIGTERM the exiting pane shell sends a stopped job, as a program that hangs
// while it shuts down does; only an explicit kill then ends it.
func suspendStandIn(t *testing.T, mark string, ready, ignoreTermination bool) string {
	t.Helper()
	script := "#!/bin/sh\n"
	if ignoreTermination {
		script += "trap '' HUP TERM\n"
	}
	if ready {
		script += "printf ' SUSPEND_READY\\n'\n"
	}
	script += "exec sleep " + mark + "\n"
	program := filepath.Join(t.TempDir(), "program.sh")
	if err := os.WriteFile(program, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return program
}

func suspendScenarioScript(t *testing.T) string {
	return filepath.Join(testenv.ModuleRoot(t), "test", "parity", "testdata", "suspend-session-native.sh")
}

// QUALITY-AUDIT-050 item 16: a SUSPEND_READY pig process outlived its parity scenario by 79 minutes. The scenario script suspends the
// program with Ctrl-Z in a tmux pane, resumes it, and waits for the resumed answer; when that wait fails the script exits through
// its cleanup. Ending the tmux session sends the job SIGHUP (and SIGTERM while it is stopped), which a program that hangs while it
// shuts down survives, reparented to init. The cleanup must end the job it started itself. The stand-in program ignores both signals
// and never answers after the resume, so the script fails on its last wait.
func TestSuspendSessionNativeKillsTheSuspendedJobWhenTheScenarioFails(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("the parity scenarios need tmux: %v", err)
	}
	mark := strconv.Itoa(900000 + os.Getpid()%90000)
	program := suspendStandIn(t, mark, true, true)
	cmd := exec.CommandContext(t.Context(), "bash", suspendScenarioScript(t), "pig")
	cmd.Env = append(os.Environ(), "PIG_PARITY_PIG_BIN="+program, "SUSPEND_PARITY_WAIT_SECONDS=3")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	t.Cleanup(func() { killSleepers(mark); killScenarioSession(cmd) })
	if err := cmd.Run(); err == nil {
		t.Fatalf("the script passed although the program never answered after the resume:\n%s", output.String())
	}
	waitScenarioGone(t, mark, cmd.Process.Pid, 5*time.Second)
}

// The runner ends a scenario that overruns its budget by killing the script, and a killed script runs no cleanup. The tmux server it
// started on its own socket, the shell in its pane and the program then stayed behind until the host rebooted. The script
// must see that it was abandoned and remove all three, whether the program was still starting or already suspended.
func TestSuspendSessionNativeCleansUpWhenTheScriptIsKilled(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("the parity scenarios need tmux: %v", err)
	}
	for _, tc := range []struct {
		name string
		// ready: the program prints its ready line, and the script is killed once the pane shell reports the suspended job;
		// otherwise the script is killed while it still waits for that line.
		ready, ignoreTermination bool
		offset                   int
	}{
		{name: "while the program starts", offset: 800000},
		{name: "while the job is suspended or resumed", ready: true, ignoreTermination: true, offset: 600000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mark := strconv.Itoa(tc.offset + os.Getpid()%90000)
			program := suspendStandIn(t, mark, tc.ready, tc.ignoreTermination)
			cmd := exec.Command("bash", suspendScenarioScript(t), "pig")
			cmd.Env = append(os.Environ(), "PIG_PARITY_PIG_BIN="+program)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			t.Cleanup(func() { killSleepers(mark); killScenarioSession(cmd) })
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan struct{})
			go func() { _ = cmd.Wait(); close(exited) }()
			session := "parity-suspend-pig-" + strconv.Itoa(cmd.Process.Pid)
			started := func() bool {
				if !tc.ready {
					return len(sleepers(mark)) > 0
				}
				pane, err := exec.Command("tmux", "-L", session, "capture-pane", "-p", "-t", session).Output()
				return err == nil && strings.Contains(string(pane), "SUSPEND_SHELL_READY")
			}
			deadline := time.Now().Add(30 * time.Second)
			for !started() {
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					<-exited
					t.Fatal("the scenario never reached the state under test")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			<-exited
			waitScenarioGone(t, mark, cmd.Process.Pid, 15*time.Second)
		})
	}
}

// waitScenarioGone fails unless the program, the tmux server and its socket file of the script with PID scriptPID are gone within the
// timeout. The server exits with its last session, and the pane shell is that session's only other process.
func waitScenarioGone(t *testing.T, mark string, scriptPID int, timeout time.Duration) {
	t.Helper()
	session := "parity-suspend-pig-" + strconv.Itoa(scriptPID)
	// The server is found by its command line, not its socket: a removed socket hides a running server from tmux -L.
	serverRunning := func() bool { return len(tmuxServers(session)) > 0 }
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(sleepers(mark)) == 0 && !serverRunning() && !socketExists(session) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pids := tmuxServers(session); len(pids) > 0 {
		t.Errorf("the scenario's tmux server %s outlived the script: pids %v", session, pids)
	}
	if socketExists(session) {
		t.Errorf("the scenario's tmux socket %s outlived the script", scenarioSocket(session))
	}
	if pids := sleepers(mark); len(pids) > 0 {
		t.Errorf("the program (sleep %s) outlived the script: pids %v", mark, pids)
	}
}

// scenarioSocket is the socket path tmux -L <session> uses, as the script computes it.
func scenarioSocket(session string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), session)
}

func socketExists(session string) bool {
	_, err := os.Lstat(scenarioSocket(session))
	return err == nil
}

// sleepers returns the processes running `sleep <mark>`.
func sleepers(mark string) []int {
	return processesWhere(func(cmdline string) bool { return cmdline == "sleep\x00"+mark+"\x00" })
}

// tmuxServers returns the tmux processes started with `-L <session>`; the forked server keeps the client's command line.
func tmuxServers(session string) []int {
	return processesWhere(func(cmdline string) bool {
		return strings.Contains("\x00"+cmdline, "\x00-L\x00"+session+"\x00")
	})
}

func processesWhere(match func(cmdline string) bool) []int {
	matches, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	var pids []int
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if match(string(data)) {
			pid, err := strconv.Atoi(filepath.Base(filepath.Dir(path)))
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// killScenarioSession ends the tmux session the script started on its private socket (parity-suspend-pig-<script pid>), so a
// failing run of these tests does not leave the tmux server and its pane shell behind, the leak they guard against. A server whose
// socket is already gone is killed by its process ID; its pane shell then gets SIGHUP.
func killScenarioSession(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	name := "parity-suspend-pig-" + strconv.Itoa(cmd.Process.Pid)
	_ = exec.Command("tmux", "-L", name, "kill-session", "-t", name).Run()
	for _, pid := range tmuxServers(name) {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
	_ = os.Remove(scenarioSocket(name))
}

func killSleepers(mark string) {
	for _, pid := range sleepers(mark) {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
}
