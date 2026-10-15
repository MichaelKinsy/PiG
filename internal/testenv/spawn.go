package testenv

import (
	"bytes"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// A test child must not outlive its test. Start, Run, Output and CombinedOutput are the one spawn path for a test child
// that can start descendants: the child leads a process group (Unix) or belongs to a kill-on-close Job Object (Windows),
// so a descendant that detaches from the child's stdio still dies with the group; Wait is bounded by WaitDelay, so a
// grandchild that holds the stdout pipe open cannot block the test; and the test's cleanup stops the whole group with
// SIGTERM, then SIGKILL after a grace period (Windows ends the job at once).
//
// On Unix the containment is the process group. A descendant that calls setsid or setpgid itself (Node's detached
// spawn, a daemonizing tool) leaves the group and is neither stopped nor reported. The child runs in a background group,
// so a child whose stdin is the test's controlling terminal stops on its first read; give it a pipe or a file. A
// Windows Job Object is inherited by every descendant, so neither limit applies there.
const (
	// spawnWaitDelay bounds how long Wait lingers for stdio that a descendant still holds after the child exits.
	spawnWaitDelay = 10 * time.Second
	// spawnGrace is how long a SIGTERM to the group may take before the group receives SIGKILL.
	spawnGrace = 2 * time.Second
)

// testWaitDelay replaces spawnWaitDelay in the tests of this package.
var testWaitDelay time.Duration

// errSurvivors is the error Run, Output or CombinedOutput reports through the test when the child exited but descendants
// were still running; the spawn path has already stopped them.
var errSurvivors = errors.New("test child exited but left descendants running")

// Child is a started test child with its containment.
type Child struct {
	cmd  *exec.Cmd
	tree *tree
	t    testing.TB
	once sync.Once

	waitOnce sync.Once
	waitErr  error
}

// Start starts cmd in its own process group or job and registers its cleanup on t. The caller waits with Child.Wait.
func Start(t testing.TB, cmd *exec.Cmd) (*Child, error) {
	t.Helper()
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = spawnWaitDelay
		if testWaitDelay != 0 {
			cmd.WaitDelay = testWaitDelay
		}
	}
	tr, err := startTree(cmd)
	if err != nil {
		return nil, err
	}
	c := &Child{cmd: cmd, tree: tr, t: t}
	t.Cleanup(func() { c.reap(false) })
	return c, nil
}

// wait reaps the child once. A group that still lists the exited, unreaped child as a member reads as alive, so the
// cleanup reaps the child itself, and every caller waits through Child.Wait.
func (c *Child) wait() error {
	c.waitOnce.Do(func() { c.waitErr = c.cmd.Wait() })
	return c.waitErr
}

// Wait waits for the child, then stops any descendant it left behind and fails the test for each such group. Wait is
// bounded by the command's WaitDelay. Call it instead of cmd.Wait.
func (c *Child) Wait() error {
	c.t.Helper()
	err := c.wait()
	c.reap(true)
	return err
}

// Cmd returns the started command.
func (c *Child) Cmd() *exec.Cmd { return c.cmd }

// reap stops the group once. After a Wait, descendants of an exited child fail the test: a test child must not leave
// processes behind. In the cleanup of a child still running, the stop is the expected end of the test.
func (c *Child) reap(afterWait bool) {
	c.once.Do(func() {
		if !afterWait {
			reaped := make(chan struct{})
			go func() { _ = c.wait(); close(reaped) }()
			defer func() {
				select {
				case <-reaped:
				case <-time.After(spawnGrace):
				}
			}()
		}
		left := c.tree.stop(spawnGrace)
		if left && afterWait {
			c.t.Errorf("%v: %v (stopped)", c.cmd.Args, errSurvivors)
		}
	})
}

// Run starts cmd and waits for it.
func Run(t testing.TB, cmd *exec.Cmd) error {
	t.Helper()
	child, err := Start(t, cmd)
	if err != nil {
		return err
	}
	return child.Wait()
}

// Output is exec.Cmd.Output with the containment of Start.
func Output(t testing.TB, cmd *exec.Cmd) ([]byte, error) {
	t.Helper()
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	if cmd.Stderr == nil {
		cmd.Stderr = &stderr
	}
	err := Run(t, cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Stderr == nil {
		exit.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

// CombinedOutput is exec.Cmd.CombinedOutput with the containment of Start.
func CombinedOutput(t testing.TB, cmd *exec.Cmd) ([]byte, error) {
	t.Helper()
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if cmd.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := Run(t, cmd)
	return out.Bytes(), err
}
