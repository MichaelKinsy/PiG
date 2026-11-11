//go:build linux

package workers

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// pi: packages/chord/src/node/workers/process.ts
// Pi forks a worker with `detached: true`, which Node starts with setsid(2): the worker leads a session and a process group of its own. The group is what
// Kill ends (TestAWorkerProcessPipesItsOutputKeepsItsStderrTailQueuesAnEarlyFrameOnItsLinkTellsControlMessages kills the grandchild with it), and the session
// leaves the worker without a controlling terminal.
func TestAWorkerProcessLeadsItsOwnSessionAndProcessGroup(t *testing.T) {
	var started atomic.Bool
	child, err := NewWorkerProcess(
		ForkOptions{Entry: fixtureEntry(t), Env: fixtureEnvironment("child"), OnOutput: func(string, []byte) {}},
		ProcessEvents{
			Control:    func(FromWorker) { started.Store(true) },
			LinkLost:   func() {},
			LinkClosed: func() {},
			Exited:     func(Exit) {},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Kill(); <-child.Exited() })
	until(t, started.Load)
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(child.Pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	// The command name is in parentheses; the fields after it are: state ppid pgrp session.
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	if group, session := fields[2], fields[3]; group != strconv.Itoa(child.Pid) || session != strconv.Itoa(child.Pid) {
		t.Fatalf("the worker (pid %d) leads process group %s and session %s, want the leader of both", child.Pid, group, session)
	}
}
