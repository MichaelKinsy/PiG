package subprocess

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// execCancelFixture runs exec calls through the production UI bridge against an exec action that waits until its context ends or the test releases it.
type execCancelFixture struct {
	*callOrderFixture
	started chan context.Context
	release chan struct{}
}

func newExecCancelFixture(t *testing.T) *execCancelFixture {
	t.Helper()
	f := &execCancelFixture{started: make(chan context.Context, 4), release: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{ExecContext: func(ctx context.Context, _ string, _ []string, _ *extension.ExecOptions) (extension.ExecResult, error) {
		extension.CallInitiated(ctx)
		f.started <- ctx
		select {
		case <-ctx.Done():
			return extension.ExecResult{Killed: true}, nil
		case <-f.release:
			return extension.ExecResult{}, nil
		}
	}})
	f.callOrderFixture = newBridgeCallOrderFixture(t, bridge)
	return f
}

func execResultKilled(t *testing.T, result *CallResultPayload) bool {
	t.Helper()
	if result.Error != nil {
		t.Fatalf("exec failed: %s", result.Error.Message)
	}
	var decoded extension.ExecResult
	if err := json.Unmarshal(result.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Killed
}

// core/exec.ts kills the child when options.signal aborts, and the call has no other signal. An extension that owns the signal names
// the call by its exec id and reports the abort with exec.cancel, which ends the command's context whichever of the two frames the
// host takes first.
func TestExecCancelEndsTheCommandOfAnOwnedSignal(t *testing.T) {
	f := newExecCancelFixture(t)
	f.call("c1", "exec", "", ExecArgs{Command: "sleep", ExecID: "exec-1", OwnSignal: true})
	ctx := <-f.started
	if ctx.Err() != nil {
		t.Fatalf("the command's context ended before the abort: %v", ctx.Err())
	}
	f.call("c2", CallExecCancel, "", ExecCancel{ExecID: "exec-1"})
	if got := f.result("c2"); got.Error != nil {
		t.Fatalf("exec.cancel failed: %s", got.Error.Message)
	}
	if !execResultKilled(t, f.result("c1")) {
		t.Error("the exec call did not report the command killed")
	}
	if ctx.Err() == nil {
		t.Error("the command's context did not end")
	}
}

// An SDK sends the cancel of a signal that was aborted before the call right behind the call's frame. The cancel must end the command
// whether it reaches the host before or after the command started.
func TestExecCancelSentBehindTheCallStillEndsTheCommand(t *testing.T) {
	f := newExecCancelFixture(t)
	f.call("c1", "exec", "", ExecArgs{Command: "sleep", ExecID: "exec-1", OwnSignal: true})
	f.call("c2", CallExecCancel, "", ExecCancel{ExecID: "exec-1"})
	if !execResultKilled(t, f.result("c1")) {
		t.Error("the exec call did not report the command killed")
	}
}

// A cancel names one call: another exec id, and a call that did not pass a signal, are not ended.
func TestExecCancelNamesOneOwnedCall(t *testing.T) {
	f := newExecCancelFixture(t)
	f.call("c1", "exec", "", ExecArgs{Command: "sleep", ExecID: "exec-1", OwnSignal: true})
	owned := <-f.started
	f.call("c2", "exec", "", ExecArgs{Command: "sleep", ExecID: "exec-2"})
	unowned := <-f.started
	f.call("c3", CallExecCancel, "", ExecCancel{ExecID: "exec-2"})
	f.call("c4", CallExecCancel, "", ExecCancel{ExecID: "exec-9"})
	f.result("c3")
	f.result("c4")
	if owned.Err() != nil || unowned.Err() != nil {
		t.Fatalf("a cancel ended a call it did not name (owned: %v, unowned: %v)", owned.Err(), unowned.Err())
	}
	close(f.release)
	for _, id := range []string{"c1", "c2"} {
		if execResultKilled(t, f.result(id)) {
			t.Errorf("call %s reports killed", id)
		}
	}
}
