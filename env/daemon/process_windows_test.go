//go:build windows

package daemon

// pi: packages/env/daemon/src/sys/windows.rs

// Ports the Windows half of packages/env/daemon/src/sys/windows.rs that Pi leaves untested: the job object of a `process` program (group, Group::end,
// Group::wait_for_exit) and kill_tree. The cross-system contracts of the group are in process_group_test.go; these are the Windows-only ones: nesting in an
// existing job, the job ending with its owner, and the taskkill lookup.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func init() {
	// groupowner starts a sleeper as a `process` program, prints its pid, waits for a line and exits without ending the program's group: its job handle closes only with this process.
	helperModes["groupowner"] = func() {
		exe, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		started, failure := startProgram(&processRequest{argv: []string{exe}, cwd: os.TempDir(), env: map[string]string{helperEnv: "sleeper"}, inheritEnv: true})
		if failure != nil {
			fmt.Fprintln(os.Stderr, failure.toJSON())
			os.Exit(2)
		}
		fmt.Println(started.pid)
		waitForLine()
	}
}

// A Windows program is not started in a session of its own: the group is a job object.
func TestWindowsProgramAttributesAreEmpty(t *testing.T) {
	if programAttributes() != nil {
		t.Fatal("a Windows program has process attributes")
	}
}

// What a program started in the daemon's own job means: the group's job nests in it. The outer job here stands for the daemon's (and for the job of a CI runner).
func TestNewGroupNestsInAnExistingJob(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), helperEnv+"=linger")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	program := watchProcess(t, cmd.Process.Pid)

	outer, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(outer) })
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(outer, process); err != nil {
		t.Skipf("the program cannot join a job of its own: %v", err)
	}

	group, err := newGroup(cmd)
	if err != nil {
		t.Fatalf("newGroup for a program already in a job: %v", err)
	}
	if group.pid != cmd.Process.Pid || !group.live {
		t.Fatalf("group %+v", group)
	}
	if program.exited(200 * time.Millisecond) {
		t.Fatal("creating the group ended the program")
	}
	group.end()
	if !program.exited(groupWait) {
		t.Fatal("ending the group left the program running")
	}
}

// The job is closed with its owner and kills what is in it (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE): a daemon that ends without ending a program's group
// leaves nothing of it running.
func TestAProgramEndsWhenTheDaemonThatStartedItDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	owner := exec.Command(exe)
	owner.Env = append(os.Environ(), helperEnv+"=groupowner")
	stdin, err := owner.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = owner.Process.Kill(); _ = owner.Wait() })
	var pid int
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatalf("the owner printed %q: %v", line, err)
	} else if _, err := fmt.Sscan(line, &pid); err != nil {
		t.Fatalf("the owner printed %q: %v", line, err)
	}
	sleeper := watchProcess(t, pid)
	if sleeper.exited(200 * time.Millisecond) {
		t.Fatal("the program ended before its owner did")
	}
	if _, err := io.WriteString(stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Wait(); err != nil {
		t.Fatalf("the owner: %v", err)
	}
	if !sleeper.exited(groupWait) {
		t.Fatal("the program outlived the process that held its job")
	}
}

// kill_tree runs taskkill.exe under SystemRoot, or C:\Windows when SystemRoot is not set.
func TestKillTreeRunsTaskkillFromSystemRoot(t *testing.T) {
	t.Run("SystemRoot names where taskkill is", func(t *testing.T) {
		started := startHelper(t, "linger")
		program := watchProcess(t, started.pid)
		t.Setenv("SystemRoot", filepath.Join(t.TempDir(), "no-system-root"))
		killTree(started.pid)
		if program.exited(time.Second) {
			t.Fatal("killTree ran a taskkill that SystemRoot does not name")
		}
	})
	t.Run("C:\\Windows when SystemRoot is empty", func(t *testing.T) {
		if _, err := os.Stat(`C:\Windows\System32\taskkill.exe`); err != nil {
			t.Skipf("no taskkill under C:\\Windows: %v", err)
		}
		started := startHelper(t, "linger")
		program := watchProcess(t, started.pid)
		done := reapAsync(started)
		t.Setenv("SystemRoot", "")
		killTree(started.pid)
		select {
		case <-done:
		case <-time.After(groupWait):
			t.Fatal("killTree without SystemRoot left the program running")
		}
		if !program.exited(groupWait) {
			t.Fatal("the program is still running")
		}
	})
}
