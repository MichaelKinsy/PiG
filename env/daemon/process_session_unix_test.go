//go:build linux || darwin

package daemon

// pi: packages/env/daemon/src/sys/unix.rs (killing_a_session_kills_its_jobs_in_groups_of_their_own)
// pi: packages/env/daemon/src/pty.rs

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestKillingASessionKillsItsJobsInGroupsOfTheirOwn(t *testing.T) {
	// A shell with job control, as a terminal's is: its background job gets a process group of its own (bash: a POSIX
	// `sh` may not do job control without a terminal).
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found in PATH")
	}
	script := "set -m; sleep 30 </dev/null >/dev/null 2>&1 & echo $!; wait"
	cmd := exec.Command(bashPath, "-c", script)
	cmd.SysProcAttr = programAttributes()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := startCommand(cmd); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	job, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("unexpected job pid %q: %v", line, err)
	}
	pgid, err := unix.Getpgid(job)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("getpgid(%d): %v", job, err)
	}
	if pgid == cmd.Process.Pid {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("expected job %d to have a process group of its own, got pgid=%d == pid", job, pgid)
	}
	killTree(cmd.Process.Pid)
	_ = cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(job, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(job, 0) == nil {
		_ = syscall.Kill(job, syscall.SIGKILL)
		t.Fatalf("the job %d outlived its session", job)
	}
}

func TestPtyShellSelfExitKillsBackgroundJobInOwnGroup(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found in PATH")
	}
	req := &ptyRequest{
		hasArgv:    true,
		argv:       []string{bashPath, "-c", "set -m; sleep 30 </dev/null >/dev/null 2>&1 & echo JOB=$!"},
		cwd:        t.TempDir(),
		env:        map[string]string{},
		inheritEnv: true,
		cols:       80,
		rows:       24,
	}
	proc, failure := startPty(req)
	if failure != nil {
		t.Fatal(failure.toJSON())
	}
	defer proc.close()

	done := make(chan struct{})
	outCh := make(chan string, 1)
	go func() {
		var buf strings.Builder
		proc.read(done, func(b []byte) bool {
			buf.Write(b)
			return !strings.Contains(buf.String(), "\n")
		})
		outCh <- buf.String()
	}()

	code, err := proc.wait()
	close(done)
	if err != nil || code != 0 {
		t.Fatalf("pty wait = (%d, %v), want (0, nil)", code, err)
	}
	out := <-outCh
	_, after, ok := strings.Cut(out, "JOB=")
	if !ok {
		t.Fatalf("missing JOB= in pty output %q", out)
	}
	fields := strings.Fields(after)
	if len(fields) == 0 {
		t.Fatalf("missing pid after JOB= in %q", out)
	}
	job, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("parse job pid from %q: %v", out, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(job, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(job, 0) == nil {
		_ = syscall.Kill(job, syscall.SIGKILL)
		t.Fatalf("background job %d outlived exited pty shell", job)
	}
}
