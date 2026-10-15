//go:build !windows

package testenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// lateDoneContext is open for exec.Cmd.Start's own checks of Done and done from the context watcher's first check on,
// so exec calls Cancel while Start is still returning to startTree.
type lateDoneContext struct {
	context.Context
	calls  atomic.Int32
	open   chan struct{}
	closed chan struct{}
}

func (c *lateDoneContext) Done() <-chan struct{} {
	if c.calls.Add(1) > 2 {
		return c.closed
	}
	return c.open
}

func (c *lateDoneContext) Err() error {
	if c.calls.Load() > 2 {
		return context.Canceled
	}
	return nil
}

// A Cancel that runs before startTree has recorded the group must stop the child's group, not read a zero group id:
// kill(0, SIGKILL) would end the test's own process group. Run under -race this reports the unsynchronised read.
func TestCancelDuringStartStopsOnlyTheChildGroup(t *testing.T) {
	for range 20 {
		closed := make(chan struct{})
		close(closed)
		ctx := &lateDoneContext{Context: context.Background(), open: make(chan struct{}), closed: closed}
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = append(os.Environ(), "TESTENV_FIXTURE=sleep")
		if err := Run(t, cmd); err == nil {
			t.Fatal("the sleeping child exited cleanly; Cancel did not stop it")
		}
		if ctx.calls.Load() <= 2 {
			t.Fatalf("exec checked Done %d times; the context never reached the watcher", ctx.calls.Load())
		}
	}
}

// A child started as a session leader (a pseudo-terminal test sets Setsid and Setctty) already leads its own group:
// setpgid on a session leader fails with EPERM, so Start must not ask for a new group, and the session's group still
// holds the child's descendants.
func TestStartContainsASessionLeader(t *testing.T) {
	rec := &recorder{TB: t}
	cmd := fixtureCommand("orphan")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := Output(rec, cmd)
	if err != nil {
		t.Fatalf("a Setsid child did not run: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("fixture output %q: %v", out, err)
	}
	waitGone(t, pid)
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], errSurvivors.Error()) {
		t.Fatalf("failures = %v, want one report of the surviving descendant", rec.errs)
	}
}

// A command that asks to join another process group would leave the group Start stops and checks without the child, so
// Start refuses it.
func TestStartRefusesAnotherProcessGroup(t *testing.T) {
	rec := &recorder{TB: t}
	cmd := fixtureCommand("sleep")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: syscall.Getpgrp()}
	if child, err := Start(rec, cmd); err == nil {
		_ = child.Cmd().Process.Kill()
		_ = child.Wait()
		t.Fatal("Start ran a child in a process group it does not lead")
	}
	rec.runCleanups()
}
