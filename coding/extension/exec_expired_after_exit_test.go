package extension_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// lateContext expires only once the pipes drained, and Done never delivers, so the watcher cannot kill first.
type lateContext struct {
	context.Context
	drained  atomic.Bool
	deadline time.Time
	err      error
	done     chan struct{}
}

func (c *lateContext) Deadline() (time.Time, bool) {
	if !c.deadline.IsZero() && c.drained.Load() {
		return c.deadline, true
	}
	return time.Time{}, false
}

func (c *lateContext) Err() error {
	if c.drained.Load() {
		return c.err
	}
	return nil
}

func (c *lateContext) Done() <-chan struct{} { return c.done }

// A context that expired while the caller stalled past the child's exit kills the call, as in Pi.
func TestExecCommandReportsAnExpiredContextAfterTheChildExitedAsKilled(t *testing.T) {
	tests := []struct {
		name     string
		deadline time.Time
		err      error
		killed   bool
	}{
		{name: "deadline passed", deadline: time.Now().Add(-time.Second), err: nil, killed: true},
		{name: "deadline exceeded", deadline: time.Now().Add(-time.Second), err: context.DeadlineExceeded, killed: true},
		{name: "cancelled", err: context.Canceled, killed: true},
		{name: "deadline ahead", deadline: time.Now().Add(time.Hour), killed: false},
		{name: "live", killed: false},
	}
	command, args := "sh", []string{"-c", "printf out"}
	if runtime.GOOS == "windows" {
		command, args = "cmd", []string{"/c", "echo out"}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &lateContext{Context: testbudget.Context(t), deadline: tt.deadline, err: tt.err, done: make(chan struct{})}
			hooked := extension.WithExecDrainHooks(ctx, &childwait.Hooks{Released: func() { ctx.drained.Store(true) }})
			result, err := extension.ExecCommand(hooked, t.TempDir(), command, args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !ctx.drained.Load() {
				t.Fatal("the pipes were never drained")
			}
			if result.Killed != tt.killed {
				t.Fatalf("Killed = %v, want %v (result %+v)", result.Killed, tt.killed, result)
			}
		})
	}
}
