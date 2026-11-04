//go:build !windows

package env

// Ports the daemon-kill case of packages/env/test/remote.test.ts (b6d5c2ec) and sends the daemon each signal it
// handles. The daemon is a real process, the test binary started as `serve`, and its commands are real processes.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitProcessGone(t *testing.T, name string, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); processAlive(pid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s (pid %d) is still running", name, pid)
		}
	}
}

// startSleeper runs `sleep 30` through the daemon and returns the pids of the daemon (the command's parent) and the command,
// and a channel of the command's result.
func startSleeper(t *testing.T, environment *RemoteExecutionEnv) (daemonPid, commandPid int, finished <-chan error) {
	t.Helper()
	pids := make(chan [2]int, 1)
	done := make(chan error, 1)
	go func() {
		_, err := environment.Exec(background, []string{"sh", "-c", "echo $PPID $$; exec sleep 30"}, &durableenv.ShellExecOptions{
			OnOutput: func(_ context.Context, text string, _ durableenv.ShellOutputInfo) {
				fields := strings.Fields(text)
				if len(fields) != 2 {
					return
				}
				parent, _ := strconv.Atoi(fields[0])
				command, _ := strconv.Atoi(fields[1])
				select {
				case pids <- [2]int{parent, command}:
				default:
				}
			},
		})
		done <- err
	}()
	select {
	case found := <-pids:
		return found[0], found[1], done
	case err := <-done:
		t.Fatalf("the command ended before it printed its pids: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("the command printed no pids")
	}
	return 0, 0, nil
}

// remote.test.ts: "kills running commands when the connection closes". Connection.close() ends stdin and sends SIGTERM at once,
// and the daemon used to die on it before it killed its commands.
// mutation-checked: dropping the daemon's SIGTERM handler leaves the command running after the connection closes.
func TestConnectionCloseKillsRunningCommands(t *testing.T) {
	environment, connection := remoteEnvironment(t)
	_, commandPid, _ := startSleeper(t, environment)
	connection.Close()
	waitProcessGone(t, "the command", commandPid)
}

// b6d5c2ec: the daemon blocks SIGTERM, SIGINT and SIGHUP and waits for them on its own thread, which kills every process group
// it started and exits, with 128 + the signal as its status. Each signal is sent to the daemon alone, not to its commands.
// mutation-checked: dropping SIGHUP (or SIGINT) from the handled signals leaves the command running after that signal.
func TestDaemonKillsItsCommandsOnTerminationSignals(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(fmt.Sprint(signal), func(t *testing.T) {
			environment, _ := remoteEnvironment(t)
			daemonPid, commandPid, finished := startSleeper(t, environment)
			if err := syscall.Kill(daemonPid, signal); err != nil {
				t.Fatal(err)
			}
			waitProcessGone(t, "the command", commandPid)
			waitProcessGone(t, "the daemon", daemonPid)
			select {
			case <-finished:
			case <-time.After(20 * time.Second):
				t.Fatal("the exec never settled after the daemon ended")
			}
		})
	}
}

// startTree runs a shell through the daemon that starts two background sleeps of its own, and returns the pids of the daemon,
// the shell and the two sleeps, which are in the shell's process group.
func startTree(t *testing.T, environment *RemoteExecutionEnv) (daemonPid int, tree []int, finished <-chan error) {
	t.Helper()
	pids := make(chan []int, 1)
	done := make(chan error, 1)
	go func() {
		_, err := environment.Exec(background, []string{"sh", "-c", "sleep 30 & first=$!; sleep 31 & second=$!; echo $PPID $$ $first $second; wait"}, &durableenv.ShellExecOptions{
			OnOutput: func(_ context.Context, text string, _ durableenv.ShellOutputInfo) {
				fields := strings.Fields(text)
				if len(fields) != 4 {
					return
				}
				found := make([]int, len(fields))
				for i, field := range fields {
					found[i], _ = strconv.Atoi(field)
				}
				select {
				case pids <- found:
				default:
				}
			},
		})
		done <- err
	}()
	select {
	case found := <-pids:
		return found[0], found[1:], done
	case err := <-done:
		t.Fatalf("the command ended before it printed its pids: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("the command printed no pids")
	}
	return 0, nil, nil
}

// b6d5c2ec with a process tree: after SIGTERM to the daemon, the shell a command started and the children that shell started
// are all gone, not only the process the daemon spawned.
// mutation-checked: dropping the daemon's SIGTERM handler leaves the shell and both sleeps running.
func TestDaemonTerminationKillsTheWholeProcessTreeOfACommand(t *testing.T) {
	environment, _ := remoteEnvironment(t)
	daemonPid, tree, finished := startTree(t, environment)
	for _, pid := range tree {
		if !processAlive(pid) {
			t.Fatalf("pid %d of the tree is not running before the signal", pid)
		}
	}
	if err := syscall.Kill(daemonPid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for i, pid := range tree {
		waitProcessGone(t, fmt.Sprintf("process %d of the tree", i), pid)
	}
	waitProcessGone(t, "the daemon", daemonPid)
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("the exec never settled after the daemon ended")
	}
}
