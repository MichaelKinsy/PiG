//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A terminal that stops taking output (the user's XOFF, a stalled ssh link) blocks the owner loop in its write to the terminal. Pi's process is blocked
// the same way (its event loop is in the synchronous write), so its signal handler waits for the terminal and its exit follows the resume: exit 0 after
// 0.18 to 0.20 s on Pi 1.5.2 under the same probe, for SIGTERM and SIGHUP, as on a healthy terminal. PiG must not make the extensions wait for the
// terminal, because its signal goroutine is not the loop: the dispose gives the loop the second that shutdownExtensionsBriefly gives extension
// handlers (a loop that cannot write cannot do the title write either) and then goes on, so session_shutdown is delivered while the terminal is
// still stopped, and the process ends with exit 0 when the terminal takes output again.
//
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:4483-4494 (shutdown({ fromSignal }) awaits runtimeHost.dispose(), drains,
// stops the TUI and calls process.exit(0)).
func TestInteractiveSignalWithAStalledTerminalDeliversShutdownAndEndsOnTheResume(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			root := t.TempDir()
			agent, project := filepath.Join(root, "agent"), filepath.Join(root, "project")
			for _, dir := range []string{agent, project} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			boot, shutdown, pidFile := filepath.Join(root, "boot"), filepath.Join(root, "shutdown"), filepath.Join(root, "pid")
			paths, _ := json.Marshal(map[string]string{"boot": boot, "shutdown": shutdown, "pid": pidFile})
			script := `import fs from 'node:fs';const paths=` + string(paths) + `;export default pi=>{pi.registerProvider('signal-faux',{api:'openai-completions',baseUrl:'https://faux.invalid/v1',apiKey:'faux-key',models:[{id:'signal-test-model',name:'signal-test-model',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:8192,maxTokens:128}]});pi.on('session_start',()=>{fs.writeFileSync(paths.pid,String(process.pid));fs.writeFileSync(paths.boot,'boot')});pi.on('session_shutdown',async()=>{fs.appendFileSync(paths.shutdown,'shutdown\n')});};`
			extension := filepath.Join(root, "stalled.mjs")
			if err := os.WriteFile(extension, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			seedFirstRunDone(t, agent)
			master, slave := openPTY(t, 40, 160)
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--no-session", "--no-extensions", "-e", extension, "--approve", "--model", "signal-faux/signal-test-model")
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agent, "PI_CODING_AGENT_DIR="+agent, "PIG_OFFLINE=1", "PI_OFFLINE=1", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			output := &ptyOutput{}
			stopCapture, captured := make(chan struct{}), make(chan struct{})
			go func() { defer close(captured); capturePTY(master, output, stopCapture) }()
			if err := cmd.Start(); err != nil {
				close(stopCapture)
				<-captured
				_ = slave.Close()
				_ = master.Close()
				t.Fatal(err)
			}
			_ = slave.Close()
			done := make(chan error, 1)
			go func() { defer close(done); done <- cmd.Wait() }()
			defer func() { cancel(); <-done; close(stopCapture); <-captured; _ = master.Close() }()
			waitFor := func(what string, ok func() bool) {
				t.Helper()
				tick := time.NewTicker(5 * time.Millisecond)
				defer tick.Stop()
				for !ok() {
					select {
					case err := <-done:
						t.Fatalf("the process ended (%v) while the test waited for %s\n%q", err, what, output.since(0))
					case <-ctx.Done():
						t.Fatalf("timed out waiting for %s\n%q", what, output.since(max(0, output.mark()-600)))
					case <-tick.C:
					}
				}
			}
			exists := func(path string) func() bool {
				return func() bool { _, err := os.Stat(path); return err == nil }
			}
			waitFor("the extension's session_start", exists(boot))
			waitFor("the model on screen", func() bool { return bytes.Contains(output.since(0), []byte("signal-test-model")) })

			// Stop the terminal's output (XOFF with IXON), and type a key: the loop's render blocks in its write.
			fd := int(master.Fd())
			termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			termios.Iflag |= unix.IXON
			if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
				t.Fatal(err)
			}
			if _, err := master.WriteString("\x13"); err != nil {
				t.Fatal(err)
			}
			if _, err := master.WriteString("x"); err != nil {
				t.Fatal(err)
			}
			waitFor("a thread of the process blocked in write(1)", func() bool { return threadBlockedInWrite(cmd.Process.Pid) })

			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			waitFor("session_shutdown while the terminal is stopped", exists(shutdown))
			select {
			case err := <-done:
				t.Fatalf("the process ended (%v) while its terminal was stopped; its loop is blocked in the write", err)
			default:
			}

			// The signal's dispose waited for the loop at most the bound it gives extension handlers, and went on: the root context ended, which ends
			// the extension's process, though the terminal is still stopped and the loop is still in its write. A dispose without that bound never
			// gets there.
			rawPid, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatalf("the extension's process id: %v", err)
			}
			extensionPid, err := strconv.Atoi(string(rawPid))
			if err != nil {
				t.Fatal(err)
			}
			waitFor("the extension's process to end while the terminal is stopped", func() bool { return errors.Is(syscall.Kill(extensionPid, 0), syscall.ESRCH) })
			select {
			case err := <-done:
				t.Fatalf("the process ended (%v) while its terminal was stopped", err)
			default:
			}

			// The terminal takes output again (XON): the loop finishes its write and the process ends as Pi's does, exit 0.
			if _, err := master.WriteString("\x11"); err != nil {
				t.Fatal(err)
			}
			var exit error
			select {
			case exit = <-done:
			case <-ctx.Done():
				t.Fatalf("the process did not end after the terminal resumed\n%q", output.since(max(0, output.mark()-600)))
			}
			var exitErr *exec.ExitError
			if errors.As(exit, &exitErr) || exit != nil {
				t.Fatalf("exit %v after %s on a terminal that stopped and resumed, want exit 0 as Pi's\n%q", exit, sig, output.since(max(0, output.mark()-300)))
			}
			if got, err := os.ReadFile(shutdown); err != nil || string(got) != "shutdown\n" {
				t.Fatalf("session_shutdown events %q (%v), want exactly one", got, err)
			}
		})
	}
}

// threadBlockedInWrite reports whether a thread of the process is inside write(2) on descriptor 1 (/proc/<pid>/task/<tid>/syscall: the syscall number and
// its first argument): the owner loop writing to a terminal that takes no output.
func threadBlockedInWrite(pid int) bool {
	tasks, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
	if err != nil {
		return false
	}
	for _, task := range tasks {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + task.Name() + "/syscall")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(raw))
		if len(fields) > 1 && fields[0] == strconv.Itoa(unix.SYS_WRITE) && fields[1] == "0x1" {
			return true
		}
	}
	return false
}
