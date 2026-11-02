//go:build linux || darwin

package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

const foregroundHelperEnv = "PIG_TUI_FOREGROUND_HELPER"

// TestStdinForegroundHelper is the helper process of TestStdinInForegroundFollowsTheTerminalProcessGroup.
func TestStdinForegroundHelper(t *testing.T) {
	role := os.Getenv(foregroundHelperEnv)
	if role == "" {
		t.Skip("helper process")
	}
	report := func(label string) {
		foreground, err := StdinInForeground()
		fmt.Printf("%s foreground=%t errno=%v\n", label, foreground, errnoOf(err))
	}
	if role == "member" {
		report("member")
		os.Exit(0)
	}
	if role == "raw" {
		// The process of a background job that resumes: the termios ioctl of raw mode stops it with SIGTTOU until fg.
		restore, err := EnterRawModeForHandoff()
		fmt.Printf("raw errno=%v\n", errnoOf(err))
		if err == nil {
			restore()
		}
		os.Exit(0)
	}
	if role == "fg-cycle" {
		runForegroundCycle()
		return
	}
	// The leader is the session leader that owns the terminal as its controlling terminal; its group is the foreground group.
	// Closing the master hangs the session leader up; it must outlive that to report what the terminal answers.
	signal.Ignore(syscall.SIGHUP)
	report("leader")
	member := exec.Command(os.Args[0], "-test.run=^TestStdinForegroundHelper$")
	member.Env = append(os.Environ(), foregroundHelperEnv+"=member")
	member.Stdin = os.Stdin
	member.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := member.Output()
	if err != nil {
		fmt.Printf("member failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(out))
	fmt.Println("ready")
	// The parent closes the master side and then writes to descriptor 3.
	if _, err := bufio.NewReader(os.NewFile(3, "gate")).ReadString('\n'); err != nil {
		fmt.Printf("gate failed: %v\n", err)
		os.Exit(1)
	}
	report("hung-up")
	os.Exit(0)
}

func errnoOf(err error) any {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno.Error()
	}
	return err
}

// tcgetpgrp(stdin) against the process group decides whether a group may draw: a background group that re-enters the terminal stops
// with SIGTTOU or SIGTTIN (#396), and a terminal that went away answers with an error Pi treats as a dead terminal (#397).
func TestStdinInForegroundFollowsTheTerminalProcessGroup(t *testing.T) {
	master, slave := openTestPTY(t)
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer gateRead.Close()
	defer gateWrite.Close()
	leader := exec.Command(os.Args[0], "-test.run=^TestStdinForegroundHelper$")
	leader.Env = append(os.Environ(), foregroundHelperEnv+"=leader")
	leader.Stdin = slave
	leader.ExtraFiles = []*os.File{gateRead}
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	stdout, err := leader.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = leader.Process.Kill()
			_ = leader.Wait()
		}
	})
	var lines []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() && scanner.Text() != "ready" {
		lines = append(lines, scanner.Text())
	}
	want := []string{"leader foreground=true errno=<nil>", "member foreground=false errno=<nil>"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("helper reported %q, want %q", lines, want)
	}
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gateWrite.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	var last string
	for scanner.Scan() {
		last = scanner.Text()
	}
	waited = true
	if err := leader.Wait(); err != nil {
		t.Fatalf("helper: %v (last line %q)", err, last)
	}
	// EIO and ENOTTY are the errors Pi's DEAD_TERMINAL_ERROR_CODES names for a terminal that went away.
	if last != "hung-up foreground=false errno=input/output error" && last != "hung-up foreground=false errno=inappropriate ioctl for device" {
		t.Fatalf("after the master closed the helper reported %q, want EIO or ENOTTY", last)
	}
}

// runForegroundCycle is the shell in TestRawModeEntryFromABackgroundGroupSurvivesTheStopAndFg: the session leader that starts a job in its own
// process group, waits for the job to stop, then does what `fg` does (tcsetpgrp, then SIGCONT to the group).
func runForegroundCycle() {
	read, write, err := os.Pipe()
	if err != nil {
		fmt.Printf("pipe: %v\n", err)
		os.Exit(1)
	}
	job := exec.Command(os.Args[0], "-test.run=^TestStdinForegroundHelper$")
	job.Env = append(os.Environ(), foregroundHelperEnv+"=raw")
	job.Stdin = os.Stdin
	job.Stdout = write
	job.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := job.Start(); err != nil {
		fmt.Printf("start: %v\n", err)
		os.Exit(1)
	}
	_ = write.Close()
	pid := job.Process.Pid
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &status, syscall.WUNTRACED, nil); err != nil {
		fmt.Printf("wait: %v\n", err)
		os.Exit(1)
	}
	if !status.Stopped() {
		fmt.Printf("job ended without stopping: %v\n", status)
		_, _ = io.Copy(os.Stdout, read)
		os.Exit(1)
	}
	fmt.Printf("stopped by %v\n", status.StopSignal())
	if err := unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, pid); err != nil {
		fmt.Printf("tcsetpgrp: %v\n", err)
		os.Exit(1)
	}
	if err := syscall.Kill(-pid, syscall.SIGCONT); err != nil {
		fmt.Printf("kill: %v\n", err)
		os.Exit(1)
	}
	if _, err := syscall.Wait4(pid, &status, 0, nil); err != nil || !status.Exited() || status.ExitStatus() != 0 {
		fmt.Printf("job did not exit cleanly: %v %v\n", status, err)
		os.Exit(1)
	}
	_, _ = io.Copy(os.Stdout, read)
	os.Exit(0)
}

// A Ctrl+Z, bg, fg cycle for the raw-mode ioctl: the resumed job is a background group, its first termios ioctl stops it with SIGTTOU, and fg
// continues it inside that call. Node's libuv retries the ioctl, so setRawMode returns; where the kernel reports EINTR to the continued call
// (macOS), an ioctl that is not retried ended the session with "interrupted system call" (#397).
func TestRawModeEntryFromABackgroundGroupSurvivesTheStopAndFg(t *testing.T) {
	master, slave := openTestPTY(t)
	defer master.Close()
	leader := exec.Command(os.Args[0], "-test.run=^TestStdinForegroundHelper$")
	leader.Env = append(os.Environ(), foregroundHelperEnv+"=fg-cycle")
	leader.Stdin = slave
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	output, err := leader.CombinedOutput()
	if err != nil {
		t.Fatalf("shell: %v\n%s", err, output)
	}
	if got := string(output); !strings.Contains(got, "stopped by stopped (tty output)") || !strings.Contains(got, "raw errno=<nil>") {
		t.Fatalf("job-control cycle reported %q, want the SIGTTOU stop and then raw mode entered", got)
	}
}
